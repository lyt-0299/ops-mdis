package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"testing"
	"time"
)

func TestNewMongoLogLines(t *testing.T) {
	cases := []struct {
		name     string
		snapshot mongoLogBuffer
		prior    *mongoLogCursor
		want     []string
		lost     int64
	}{
		{"first poll", mongoLogBuffer{6, []string{"a", "b"}}, nil, []string{"a", "b"}, 0},
		{"retry already acknowledged", mongoLogBuffer{6, []string{"a", "b"}}, &mongoLogCursor{Written: 6, Anchor: mongoLogHash("b")}, nil, 0},
		{"one new line", mongoLogBuffer{7, []string{"b", "c"}}, &mongoLogCursor{Written: 6, Anchor: mongoLogHash("b")}, []string{"c"}, 0},
		{"buffer overwritten", mongoLogBuffer{9, []string{"d", "e"}}, &mongoLogCursor{Written: 6, Anchor: mongoLogHash("b")}, []string{"d", "e"}, 1},
		{"server restarted", mongoLogBuffer{2, []string{"new", "next"}}, &mongoLogCursor{Written: 6, Anchor: mongoLogHash("b")}, []string{"new", "next"}, 0},
		{"anchor changed", mongoLogBuffer{6, []string{"new", "next"}}, &mongoLogCursor{Written: 6, Anchor: mongoLogHash("b")}, []string{"new", "next"}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lines, lost, err := newMongoLogLines(c.snapshot, c.prior)
			if err != nil || lost != c.lost || !slices.Equal(lines, c.want) {
				t.Fatalf("lines=%v lost=%d err=%v", lines, lost, err)
			}
		})
	}
}

func TestMongoLogValuesAndLokiPush(t *testing.T) {
	start := time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC)
	values := mongoLogValues([]string{
		`{"t":{"$date":"2026-11-09T09:00:02.001+00:00"},"msg":"later"}`,
		`{"t":{"$date":"2026-11-09T17:00:01.001+0800"},"msg":"earlier"}`,
		`{"t":{"$date":"2026-11-09T08:00:00Z"},"msg":"old"}`,
		"no timestamp",
	}, start)
	if len(values) != 2 || values[0][0] != strconv.FormatInt(start.Add(time.Second+time.Millisecond).UnixNano(), 10) {
		t.Fatalf("unexpected Loki values: %v", values)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/loki/api/v1/push" {
			t.Errorf("unexpected Loki request: %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			Streams []struct {
				Stream map[string]string `json:"stream"`
				Values [][2]string       `json:"values"`
			} `json:"streams"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Streams) != 1 {
			t.Errorf("invalid Loki body: %v, %v", body, err)
		} else if body.Streams[0].Stream["source_id"] != "fixture" || !slices.Equal(body.Streams[0].Values, values) {
			t.Errorf("unexpected log stream: %v", body.Streams[0])
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	t.Setenv("ENV_LOKI_URL", server.URL)
	if err := pushMongoLogs(context.Background(), map[string]string{"log_source": "mongodb", "source_id": "fixture"}, values); err != nil {
		t.Fatal(err)
	}
}
