package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"sourcecode.mingdao.net/private/MDIS/server/alert/database"
	"sourcecode.mingdao.net/private/MDIS/server/alert/httputil"
)

const monitorPanelKind = "promQLPanel"
const maxMonitorExpression = 4096

type monitorPanel struct {
	ID     string `bson:"_id,omitempty" json:"id,omitempty"`
	Kind   string `bson:"kind" json:"-"`
	Name   string `bson:"name" json:"name"`
	PromQL string `bson:"promql" json:"promql"`
	View   string `bson:"view" json:"view"`
}

func listMonitorPanels(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	panels, err := loadMonitorPanels(ctx)
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_query_failed")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, panels)
}

func loadMonitorPanels(ctx context.Context) ([]monitorPanel, error) {
	cur, err := database.Config().Find(ctx, bson.M{"kind": monitorPanelKind}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	panels := []monitorPanel{}
	if err := cur.All(ctx, &panels); err != nil {
		return nil, err
	}
	return panels, nil
}

func saveMonitorPanel(w http.ResponseWriter, r *http.Request) {
	var panel monitorPanel
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	if err := httputil.ReadJSON(r, &panel); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_param")
		return
	}
	panel.Name = strings.TrimSpace(panel.Name)
	panel.PromQL = strings.TrimSpace(panel.PromQL)
	if panel.Name == "" || len([]rune(panel.Name)) > 80 ||
		panel.PromQL == "" || len(panel.PromQL) > maxMonitorExpression ||
		(panel.View != "chart" && panel.View != "table") {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_param")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	panel.ID = r.PathValue("id")
	panel.Kind = monitorPanelKind
	if panel.ID == "" {
		panel.ID = bson.NewObjectID().Hex()
		if _, err := database.Config().InsertOne(ctx, panel); err != nil {
			httputil.WriteErr(w, http.StatusInternalServerError, "common_create_failed")
			return
		}
	} else {
		// Keep the identifier and discriminator out of the update document.
		result, err := database.Config().UpdateOne(ctx, bson.M{"kind": monitorPanelKind, "_id": panel.ID}, bson.M{"$set": bson.M{
			"name": panel.Name, "promql": panel.PromQL, "view": panel.View,
		}})
		if err != nil {
			httputil.WriteErr(w, http.StatusInternalServerError, "common_update_failed")
			return
		}
		if result.MatchedCount == 0 {
			httputil.WriteErr(w, http.StatusNotFound, "monitor_panel_not_found")
			return
		}
	}
	httputil.WriteJSON(w, http.StatusOK, panel)
}

func deleteMonitorPanel(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	_, err := database.Config().DeleteOne(ctx, bson.M{"kind": monitorPanelKind, "_id": r.PathValue("id")})
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_delete_failed")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func runMonitorQuery(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PromQL       string `json:"promql"`
		RangeSeconds int64  `json:"rangeSeconds"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	if err := httputil.ReadJSON(r, &req); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_param")
		return
	}
	req.PromQL = strings.TrimSpace(req.PromQL)
	if req.PromQL == "" || len(req.PromQL) > maxMonitorExpression || req.RangeSeconds < 0 || req.RangeSeconds > 7*86400 {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_param")
		return
	}

	end := time.Now().Unix()
	path := "/api/v1/query"
	limit := 100
	var step int64
	params := url.Values{"query": {req.PromQL}, "timeout": {"10s"}}
	if req.RangeSeconds > 0 {
		path = "/api/v1/query_range"
		limit = 20
		step = max(int64(1), (req.RangeSeconds+299)/300)
		params.Set("start", strconv.FormatInt(end-req.RangeSeconds, 10))
		params.Set("end", strconv.FormatInt(end, 10))
		params.Set("step", strconv.FormatInt(step, 10))
	}
	// Newer Prometheus versions honor the query limit. Apply it locally as
	// well for the bundled version, retaining one extra series to detect truncation.
	params.Set("limit", strconv.Itoa(limit+1))
	data, ok := monitorRead(w, r, http.MethodPost, path, params)
	if !ok {
		return
	}
	var result struct {
		ResultType string          `json:"resultType"`
		Result     json.RawMessage `json:"result"`
		Start      int64           `json:"start,omitempty"`
		End        int64           `json:"end,omitempty"`
		Step       int64           `json:"step,omitempty"`
		Truncated  bool            `json:"truncated,omitempty"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		httputil.WriteErr(w, http.StatusBadGateway, "common_query_failed")
		return
	}
	if result.ResultType == "matrix" || result.ResultType == "vector" {
		var series []json.RawMessage
		if err := json.Unmarshal(result.Result, &series); err != nil {
			httputil.WriteErr(w, http.StatusBadGateway, "common_query_failed")
			return
		}
		if len(series) > limit {
			result.Result, _ = json.Marshal(series[:limit])
			result.Truncated = true
		}
	}
	if step > 0 {
		result.Start, result.End, result.Step = end-req.RangeSeconds, end, step
	}
	httputil.WriteJSON(w, http.StatusOK, result)
}

func listMonitorJobs(w http.ResponseWriter, r *http.Request) {
	data, ok := monitorRead(w, r, http.MethodGet, "/api/v1/label/job/values", url.Values{
		"start": {strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10)},
	})
	if ok {
		httputil.WriteJSON(w, http.StatusOK, data)
	}
}

func listMonitorMetrics(w http.ResponseWriter, r *http.Request) {
	search, job := strings.TrimSpace(r.URL.Query().Get("search")), strings.TrimSpace(r.URL.Query().Get("job"))
	if len(search) > 128 || len(job) > 128 {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_param")
		return
	}
	matcher := `{__name__=~".+"}`
	if search != "" {
		matcher = `{__name__=~` + strconv.Quote("(?i).*"+regexp.QuoteMeta(search)+".*") + `}`
	}
	if job != "" {
		matcher = strings.TrimSuffix(matcher, "}") + ",job=" + strconv.Quote(job) + "}"
	}
	data, ok := monitorRead(w, r, http.MethodGet, "/api/v1/label/__name__/values", url.Values{
		"match[]": {matcher}, "limit": {"201"}, "start": {strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10)},
	})
	if ok {
		httputil.WriteJSON(w, http.StatusOK, data)
	}
}

func monitorRead(w http.ResponseWriter, r *http.Request, method, path string, params url.Values) (json.RawMessage, bool) {
	ctx, cancel := context.WithTimeout(r.Context(), 14*time.Second)
	defer cancel()
	client, err := builtinPrometheusClient(ctx)
	if err != nil {
		key := "common_query_failed"
		if errors.Is(err, mongo.ErrNoDocuments) {
			key = "alert_datasource_not_found"
		}
		httputil.WriteErr(w, http.StatusServiceUnavailable, key)
		return nil, false
	}
	data, err := client.ReadMonitorData(ctx, method, path, params)
	if err != nil {
		httputil.WriteErr(w, http.StatusBadGateway, "common_query_failed", err.Error())
		return nil, false
	}
	return data, true
}
