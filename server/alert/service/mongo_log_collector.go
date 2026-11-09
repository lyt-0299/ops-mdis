package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
	"golang.org/x/sync/errgroup"

	"sourcecode.mingdao.net/private/MDIS/server/alert/database"
	"sourcecode.mingdao.net/private/MDIS/server/alert/model"
)

const mongoLogStateKind = "mongoLogState"
const mongoLogPollInterval = 15 * time.Second

var mongoLogHTTP = &http.Client{Timeout: 10 * time.Second}

type mongoLogBuffer struct {
	Written int64    `bson:"totalLinesWritten"`
	Lines   []string `bson:"log"`
}

type mongoLogCursor struct {
	Node    string `bson:"node"`
	Written int64  `bson:"written"`
	Anchor  string `bson:"anchor"`
}

type mongoLogCollectionState struct {
	Nodes []mongoLogCursor `bson:"nodes"`
}

type MongoLogStatus struct {
	CheckedAt time.Time `bson:"checkedAt" json:"checkedAt"`
	State     string    `bson:"state" json:"state"`
}

func MongoLogStatuses(ctx context.Context) map[string]MongoLogStatus {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out := make(map[string]MongoLogStatus)
	cur, err := database.State().Find(ctx, bson.M{
		"kind": mongoLogStateKind, "status.checkedAt": bson.M{"$gte": time.Now().Add(-3 * time.Minute)},
	}, options.Find().SetProjection(bson.M{"sourceId": 1, "status": 1}))
	if err != nil {
		return out
	}
	defer cur.Close(ctx)
	for cur.Next(ctx) {
		var row struct {
			SourceID string         `bson:"sourceId"`
			Status   MongoLogStatus `bson:"status"`
		}
		if err := cur.Decode(&row); err == nil {
			out[row.SourceID] = row.Status
		}
	}
	return out
}

// StartMongoLogCollector polls opt-in sources. Loki stores the logs; ops-mongo
// stores only cursors and a short lease to avoid overlapping gateway writers.
func StartMongoLogCollector() {
	for {
		collectMongoLogRound()
		time.Sleep(mongoLogPollInterval)
	}
}

func collectMongoLogRound() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	cur, err := database.Config().Find(ctx, bson.M{
		"kind": model.KindMongoInstance, "enabled": true, "purposes": model.MongoLogPurpose,
	})
	var sources []model.MongoInstance
	if err == nil {
		err = cur.All(ctx, &sources)
		_ = cur.Close(ctx)
	}
	cancel()
	if err != nil {
		log.Printf("[mongo-logs] cannot read source settings: %v", err)
		return
	}

	var group errgroup.Group
	group.SetLimit(4)
	for _, source := range sources {
		group.Go(func() error {
			collectMongoSourceLogs(source)
			return nil
		})
	}
	_ = group.Wait()
}

func collectMongoSourceLogs(source model.MongoInstance) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	key := "mongo-log:" + source.ID
	lease := bson.NewObjectID().Hex()
	now := time.Now()
	filter := bson.M{"_id": key, "$or": bson.A{
		bson.M{"leaseUntil": bson.M{"$lte": now}},
		bson.M{"leaseUntil": bson.M{"$exists": false}},
	}}
	update := bson.M{"$set": bson.M{
		"kind": mongoLogStateKind, "sourceId": source.ID, "lease": lease, "leaseUntil": now.Add(time.Minute),
	}}
	var state mongoLogCollectionState
	err := database.State().FindOneAndUpdate(ctx, filter, update,
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)).Decode(&state)
	if mongo.IsDuplicateKeyError(err) {
		return
	}
	if err != nil {
		log.Printf("[mongo-logs] %s: cannot acquire collector state", source.ID)
		return
	}
	collectionStatus := "error"
	defer func() {
		saveCtx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		_, err := database.State().UpdateOne(saveCtx, bson.M{"_id": key, "lease": lease}, bson.M{
			"$set": bson.M{"nodes": state.Nodes, "status": MongoLogStatus{
				State: collectionStatus, CheckedAt: time.Now(),
			}},
			"$unset": bson.M{"lease": "", "leaseUntil": ""},
		})
		if err != nil {
			log.Printf("[mongo-logs] %s: cannot save collector state", source.ID)
		}
	}()

	decryptMongoInstance(&source)
	pool, err := NewMongoPoolClient(&source)
	if err != nil {
		log.Printf("[mongo-logs] %s: MongoDB connection failed", source.ID)
		return
	}
	nodes, err := mongoLogNodes(ctx, pool)
	if err != nil {
		log.Printf("[mongo-logs] %s: MongoDB topology discovery failed", source.ID)
		return
	}

	type nodeResult struct {
		cursor mongoLogCursor
		err    error
	}
	results := make([]nodeResult, len(nodes))
	var group errgroup.Group
	group.SetLimit(4)
	for i, node := range nodes {
		group.Go(func() error {
			index := slices.IndexFunc(state.Nodes, func(cursor mongoLogCursor) bool { return cursor.Node == node })
			var previous *mongoLogCursor
			if index >= 0 {
				previous = &state.Nodes[index]
			}
			results[i].cursor, results[i].err = readAndPushMongoLogs(ctx, pool, node, previous)
			return nil
		})
	}
	_ = group.Wait()

	var successful int
	for i, result := range results {
		if result.err != nil {
			// Keep the last successful cursor so a transient failure is retryable.
			log.Printf("[mongo-logs] %s node %q: %v", source.ID, nodes[i], result.err)
			continue
		}
		successful++
		index := slices.IndexFunc(state.Nodes, func(cursor mongoLogCursor) bool { return cursor.Node == result.cursor.Node })
		if index >= 0 {
			state.Nodes[index] = result.cursor
		} else {
			state.Nodes = append(state.Nodes, result.cursor)
		}
	}
	if successful == len(nodes) {
		collectionStatus = "active"
	} else if successful > 0 {
		collectionStatus = "partial"
	}

}

func mongoLogNodes(ctx context.Context, pool *MongoPoolClient) ([]string, error) {
	var topology struct {
		Hosts    []string `bson:"hosts"`
		Passives []string `bson:"passives"`
		Me       string   `bson:"me"`
	}
	admin := pool.client.Database("admin")
	cmdOptions := options.RunCmd().SetReadPreference(readpref.PrimaryPreferred())
	err := admin.RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}, cmdOptions).Decode(&topology)
	var commandErr mongo.CommandError
	if errors.As(err, &commandErr) && commandErr.Code == 59 {
		err = admin.RunCommand(ctx, bson.D{{Key: "isMaster", Value: 1}}, cmdOptions).Decode(&topology)
	}
	if err != nil {
		return nil, err
	}

	nodes := append(topology.Hosts, topology.Passives...)
	if topology.Me != "" {
		nodes = append(nodes, topology.Me)
	}
	if len(nodes) == 0 {
		// Standalone or mongos: read the server selected by the configured URI.
		return []string{""}, nil
	}
	slices.Sort(nodes)
	return slices.Compact(nodes), nil
}

func readAndPushMongoLogs(ctx context.Context, pool *MongoPoolClient, node string, previous *mongoLogCursor) (mongoLogCursor, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()

	client := pool.client
	if node != "" {
		var err error
		client, err = pool.directMemberClient(node)
		if err != nil {
			return mongoLogCursor{}, errors.New("could not open direct MongoDB connection")
		}
	}
	var snapshot mongoLogBuffer
	err := client.Database("admin").RunCommand(ctx, bson.D{{Key: "getLog", Value: "global"}},
		options.RunCmd().SetReadPreference(readpref.PrimaryPreferred())).Decode(&snapshot)
	if err != nil {
		return mongoLogCursor{}, errors.New("getLog failed; check connectivity and getLog privilege")
	}

	lines, lost, err := newMongoLogLines(snapshot, previous)
	if err != nil {
		return mongoLogCursor{}, err
	}
	if lost > 0 {
		log.Printf("[mongo-logs] %s node %q: %d unread lines overwritten in MongoDB buffer", pool.instance.ID, node, lost)
	}
	cursor := mongoLogCursor{Node: node, Written: snapshot.Written}
	if len(snapshot.Lines) > 0 {
		cursor.Anchor = mongoLogHash(snapshot.Lines[len(snapshot.Lines)-1])
	}
	if len(lines) == 0 {
		return cursor, nil
	}

	now := time.Now()
	start := now.Add(-7 * 24 * time.Hour)
	if previous == nil {
		// Bootstrap with recent history only, within Loki's accepted ingestion window.
		start = now.Add(-15 * time.Minute)
	}
	values := mongoLogValues(lines, start)
	if len(values) == 0 {
		return cursor, nil
	}
	nodeLabel := node
	if nodeLabel == "" {
		nodeLabel = "configured-target"
	}
	err = pushMongoLogs(ctx, map[string]string{
		"log_source": "mongodb", "mongo_source": pool.instance.Name,
		"source_id": pool.instance.ID, "mongo_node": nodeLabel,
	}, values)
	if err != nil {
		return mongoLogCursor{}, err
	}
	return cursor, nil
}

// A monotonically increasing total plus a hash of the last acknowledged line
// distinguishes repeated polls, circular-buffer gaps and server restarts.
func newMongoLogLines(snapshot mongoLogBuffer, previous *mongoLogCursor) ([]string, int64, error) {
	first := snapshot.Written - int64(len(snapshot.Lines))
	if first < 0 {
		return nil, 0, errors.New("invalid MongoDB log buffer count")
	}
	if previous == nil || previous.Written > snapshot.Written {
		return snapshot.Lines, 0, nil
	}
	if previous.Written <= first {
		return snapshot.Lines, first - previous.Written, nil
	}
	offset := previous.Written - first
	if mongoLogHash(snapshot.Lines[offset-1]) != previous.Anchor {
		return snapshot.Lines, 0, nil
	}
	return snapshot.Lines[offset:], 0, nil
}

func mongoLogHash(line string) string {
	sum := sha256.Sum256([]byte(line))
	return hex.EncodeToString(sum[:])
}

func mongoLogValues(lines []string, since time.Time) [][2]string {
	type timedLine struct {
		time time.Time
		line string
	}
	entries := make([]timedLine, 0, len(lines))
	for _, line := range lines {
		var header struct {
			Time struct {
				Date string `json:"$date"`
			} `json:"t"`
		}
		if err := json.Unmarshal([]byte(line), &header); err != nil {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, header.Time.Date)
		if err != nil {
			at, err = time.Parse("2006-01-02T15:04:05.999999999Z0700", header.Time.Date)
		}
		if err == nil && !at.Before(since) {
			entries = append(entries, timedLine{at, line})
		}
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].time.Before(entries[j].time) })
	values := make([][2]string, 0, len(entries))
	for _, entry := range entries {
		values = append(values, [2]string{strconv.FormatInt(entry.time.UnixNano(), 10), entry.line})
	}
	return values
}

func pushMongoLogs(ctx context.Context, labels map[string]string, values [][2]string) error {
	base := strings.TrimRight(os.Getenv("ENV_LOKI_URL"), "/")
	if base == "" {
		base = "http://ops-loki:3100"
	}
	type stream struct {
		Stream map[string]string `json:"stream"`
		Values [][2]string       `json:"values"`
	}
	body, err := json.Marshal(struct {
		Streams []stream `json:"streams"`
	}{Streams: []stream{{Stream: labels, Values: values}}})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/loki/api/v1/push", bytes.NewReader(body))
	if err != nil {
		return errors.New("invalid Loki endpoint")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := mongoLogHTTP.Do(req)
	if err != nil {
		return errors.New("Loki push failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Loki push returned HTTP %d", resp.StatusCode)
	}
	return nil
}
