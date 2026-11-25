package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type metricsModelTarget struct {
	SampleID, ReleaseID, GroupID, RecordingID, ArtistID string
	Fields                                              map[string]string
}

type metricsModelUsage struct {
	Calls        int `json:"calls"`
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type metricsModel struct {
	mode    string
	server  *httptest.Server
	db      *sql.DB
	gate    sync.RWMutex
	mu      sync.Mutex
	targets map[string]metricsModelTarget
	counts  map[string]map[string]metricsModelUsage
}

func newMetricsModel(t *testing.T) *metricsModel {
	t.Helper()
	mode := os.Getenv("ROOMUSIC_CURATION_METRICS_MODEL")
	switch mode {
	case "":
		mode = "oracle"
	case "oracle", "first_candidate":
	default:
		t.Fatalf("未知度量模型模式 %q", mode)
	}
	m := &metricsModel{mode: mode, targets: map[string]metricsModelTarget{}, counts: map[string]map[string]metricsModelUsage{}}
	m.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		m.gate.RLock()
		defer m.gate.RUnlock()
		if err := m.respond(w, r); err != nil && r.Context().Err() == nil {
			t.Errorf("度量模型驱动失败：%v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}))
	t.Cleanup(m.server.Close)
	return m
}

func (m *metricsModel) setTargets(targets map[string]metricsModelTarget) {
	m.mu.Lock()
	defer m.mu.Unlock()
	maps.Copy(m.targets, targets)
}

// 程序补写专辑或曲目署名后可能产生扫描时还没有的艺人身份。
func (m *metricsModel) registerEffectiveArtists(t *testing.T, app *integrationTestApplication, entry metricEntry) {
	t.Helper()
	rows, err := app.database.QueryContext(t.Context(), `SELECT DISTINCT c.artist_id,n.spelling
        FROM artist_effective_credits c JOIN artist_names n ON n.id=c.name_id
        LEFT JOIN tracks t ON t.id=c.track_id LEFT JOIN media m ON m.id=t.medium_id
        WHERE coalesce(m.origin_release_id,m.release_id,c.release_id)=$1 ORDER BY n.spelling,c.artist_id`, entry.release)
	requireCurationOK(t, err)
	defer rows.Close()
	m.mu.Lock()
	defer m.mu.Unlock()
	for rows.Next() {
		var id, name string
		requireCurationOK(t, rows.Scan(&id, &name))
		if _, known := m.targets[id]; !known {
			m.targets[id] = metricsModelTarget{SampleID: entry.sample.ID, ArtistID: entry.sample.Expect.Artists[name]}
		}
	}
	requireCurationOK(t, rows.Err())
}

func (m *metricsModel) configure(t *testing.T, app *integrationTestApplication, admin *integrationTestUser, targets map[string]metricsModelTarget, upstreamURL string) {
	t.Helper()
	m.db = app.database
	m.setTargets(targets)
	resp := app.request(t, http.MethodPut, stewardBase+"/settings", map[string]string{
		"endpoint": m.server.URL, "protocol": "openai", "model": "metrics-main", "review_model": "metrics-review",
		"search_source": "searxng", "searxng_url": upstreamURL + "/searxng",
	}, admin, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("度量模型配置 HTTP %d %s", resp.Code, resp.Body)
	}
}

func (m *metricsModel) usage() map[string]map[string]metricsModelUsage {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := map[string]map[string]metricsModelUsage{}
	for id, counts := range m.counts {
		result[id] = maps.Clone(counts)
	}
	return result
}

type metricsChatRequest struct {
	Model    string
	Messages []struct {
		Role, Content string
		ToolCalls     []struct{ Function struct{ Name string } } `json:"tool_calls"`
	}
}

type metricsToolCall struct {
	name string
	args any
}

type metricsModelReply struct {
	category  string
	sampleIDs []string
	calls     []metricsToolCall
}

func (m *metricsModel) respond(w http.ResponseWriter, r *http.Request) error {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	var q metricsChatRequest
	if err := json.Unmarshal(body, &q); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var rep metricsModelReply
	switch q.Model {
	case "metrics-main":
		rep, err = m.pendingReply(r.Context(), q)
	case "metrics-review":
		rep, err = m.reviewReply(r.Context(), q)
	default:
		return fmt.Errorf("意外的模型名 %q", q.Model)
	}
	if err != nil {
		return err
	}
	wire, used, err := metricsFrame(rep.calls, len(body))
	if err != nil {
		return err
	}
	if len(rep.sampleIDs) == 0 {
		return fmt.Errorf("模型调用缺少样本归属：%s", rep.category)
	}
	// 驱动每批领一项；较大批次按项平分用量，余数与本次调用次数计入首项。
	for i, id := range rep.sampleIDs {
		if m.counts[id] == nil {
			m.counts[id] = map[string]metricsModelUsage{}
		}
		use := m.counts[id][rep.category]
		use.InputTokens += used.InputTokens / len(rep.sampleIDs)
		use.OutputTokens += used.OutputTokens / len(rep.sampleIDs)
		if i == 0 {
			use.Calls++
			use.InputTokens += used.InputTokens % len(rep.sampleIDs)
			use.OutputTokens += used.OutputTokens % len(rep.sampleIDs)
		}
		m.counts[id][rep.category] = use
	}
	w.Header().Set("Content-Type", "text/event-stream")
	_, err = w.Write(wire)
	return err
}

// 将 SSE 帧与结束标记一并计入响应字节；用量字段的位数也计入，迭代到自洽。
func metricsFrame(calls []metricsToolCall, inputBytes int) ([]byte, metricsModelUsage, error) {
	delta := map[string]any{"content": "处理完成"}
	reason := "stop"
	if len(calls) != 0 {
		var functions []any
		for i, call := range calls {
			args, err := json.Marshal(call.args)
			if err != nil {
				return nil, metricsModelUsage{}, err
			}
			functions = append(functions, map[string]any{"index": i, "id": uuid.New(), "type": "function", "function": map[string]string{"name": call.name, "arguments": string(args)}})
		}
		delta = map[string]any{"tool_calls": functions}
		reason = "tool_calls"
	}
	used := metricsModelUsage{Calls: 1, InputTokens: inputBytes / 4}
	for {
		raw, err := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": reason}},
			"usage": map[string]int{"prompt_tokens": used.InputTokens, "completion_tokens": used.OutputTokens}})
		if err != nil {
			return nil, used, err
		}
		wire := []byte("data: " + string(raw) + "\n\ndata: [DONE]\n\n")
		if n := len(wire) / 4; used.OutputTokens == n {
			return wire, used, nil
		} else {
			used.OutputTokens = n
		}
	}
}

type metricsPending struct {
	ID         string `json:"item_id"`
	Kind       string
	Candidates []struct {
		ID string `json:"external_id"`
	}
	Evidence struct{ Names []string }
}

var metricsPendingLine = regexp.MustCompile(`(?m)^item_id=([a-f0-9-]{36}) kind=([^ ]+) `)

func metricsToolBody(content, name string) (string, bool) {
	prefix := `<roomusic-source kind="tool" name="` + name + `">`
	body, ok := strings.CutPrefix(content, prefix)
	if !ok {
		return "", false
	}
	return strings.TrimSpace(html.UnescapeString(strings.TrimSuffix(body, "</roomusic-source>"))), true
}

func (m *metricsModel) pendingReply(ctx context.Context, q metricsChatRequest) (metricsModelReply, error) {
	rep := metricsModelReply{category: "pending_step"}
	targets := map[string]metricsModelTarget{}
	var pending []metricsPending
	var lastTool string
	var firstArtist string
	for _, msg := range q.Messages {
		switch msg.Role {
		case "user":
			for _, fields := range metricsPendingLine.FindAllStringSubmatch(msg.Content, -1) {
				var id string
				if err := m.db.QueryRowContext(ctx, `SELECT coalesce(target_id,artist_id)::text FROM curation_pending_items WHERE id=$1`, fields[1]).Scan(&id); err != nil {
					return rep, err
				}
				target, ok := m.targets[id]
				if !ok {
					return rep, fmt.Errorf("待定项 %s 本地目标 %s 没有样本标注", fields[1], id)
				}
				targets[fields[1]] = target
				rep.sampleIDs = append(rep.sampleIDs, target.SampleID)
				if fields[2] == "artist_identity" {
					rep.category = "artist_identity"
				}
			}
		case "assistant":
			for _, tc := range msg.ToolCalls {
				lastTool = tc.Function.Name
			}
		case "tool":
			if body, ok := metricsToolBody(msg.Content, "read_pending_items"); ok {
				if err := json.Unmarshal([]byte(body), &pending); err != nil {
					return rep, fmt.Errorf("待定项读取结果：%w", err)
				}
			}
			if body, ok := metricsToolBody(msg.Content, "web_search"); ok {
				var response struct{ Results []struct{ URL string } }
				if err := json.Unmarshal([]byte(body), &response); err != nil {
					return rep, err
				}
				for _, result := range response.Results {
					id, ok := strings.CutPrefix(result.URL, "https://musicbrainz.org/artist/")
					if ok && uuid.Valid(id) {
						firstArtist = id
						break
					}
				}
			}
		}
	}
	if lastTool == "" {
		rep.calls = []metricsToolCall{{"read_pending_items", map[string]any{}}}
		return rep, nil
	}
	if lastTool != "read_pending_items" && lastTool != "web_search" {
		return rep, nil
	}
	for _, item := range pending {
		if item.Kind == "artist_identity" && lastTool == "read_pending_items" && len(item.Evidence.Names) > 0 {
			rep.calls = []metricsToolCall{{"web_search", map[string]string{"q": strings.Join(item.Evidence.Names, " ")}}}
			return rep, nil
		}
	}
	for _, item := range pending {
		target := targets[item.ID]
		selected := target.ReleaseID
		switch item.Kind {
		case "recording_match":
			selected = target.RecordingID
		case "artist_identity":
			selected = target.ArtistID
		case "release_match":
		default:
			selected = ""
		}
		if m.mode == "first_candidate" {
			selected = ""
			if item.Kind == "artist_identity" {
				selected = firstArtist
			} else if len(item.Candidates) > 0 {
				selected = item.Candidates[0].ID
			}
		}
		call := metricsToolCall{"shelve_pending_item", map[string]string{"item_id": item.ID, "reason": "当前证据无法确认对应。"}}
		if selected != "" {
			basis := map[string]any{"note": "已核对本批待定项。"}
			args := map[string]any{"item_id": item.ID, "external_id": selected, "basis": basis}
			call = metricsToolCall{"resolve_release_match", args}
			if item.Kind == "artist_identity" {
				call.name = "resolve_artist_identity"
				args["source"] = "musicbrainz"
				basis["citations"] = []string{"https://musicbrainz.org/artist/" + selected}
			}
		}
		rep.calls = append(rep.calls, call)
	}
	return rep, nil
}

type metricsReviewEvidence struct {
	ID      string `json:"item_id"`
	Facts   struct{ Targets []struct{ ID string } }
	Preview struct {
		Data []struct {
			Field string
			After any
		}
	}
}

func (m *metricsModel) reviewReply(ctx context.Context, q metricsChatRequest) (metricsModelReply, error) {
	rep := metricsModelReply{category: "review"}
	var data struct {
		Items []struct {
			ID   string `json:"item_id"`
			Tool string
		}
		Evidence []metricsReviewEvidence
	}
	if len(q.Messages) == 0 {
		return rep, fmt.Errorf("审查没有消息")
	}
	if err := json.Unmarshal([]byte(q.Messages[len(q.Messages)-1].Content), &data); err != nil {
		return rep, err
	}
	evidence := map[string]metricsReviewEvidence{}
	for _, e := range data.Evidence {
		evidence[e.ID] = e
	}
	var decisions []any
	for _, item := range data.Items {
		e, ok := evidence[item.ID]
		if !ok || len(e.Facts.Targets) != 1 {
			return rep, fmt.Errorf("审查项 %s 的目标数为 %d", item.ID, len(e.Facts.Targets))
		}
		target, ok := m.targets[e.Facts.Targets[0].ID]
		if !ok {
			return rep, fmt.Errorf("审查的本地目标 %s 没有样本标注", e.Facts.Targets[0].ID)
		}
		rep.sampleIDs = append(rep.sampleIDs, target.SampleID)
		decision := "approved"
		if m.mode == "oracle" {
			approve, err := m.checkPreview(ctx, target, item.Tool, e)
			if err != nil {
				return rep, err
			}
			if !approve {
				decision = "rejected"
			}
		}
		decisions = append(decisions, map[string]any{"item_id": item.ID, "decision": decision, "risk": "low", "reasons": []string{"已核对模拟审查准则。"}})
	}
	rep.calls = []metricsToolCall{{"submit_review", map[string]any{"items": decisions}}}
	return rep, nil
}

func (m *metricsModel) checkPreview(ctx context.Context, expected metricsModelTarget, tool string, e metricsReviewEvidence) (bool, error) {
	linked := false
	for _, ch := range e.Preview.Data {
		switch ch.Field {
		case "external_link":
			value := fmt.Sprint(ch.After)
			linked = tool == "resolve_release_match" && value != "" && (value == expected.ReleaseID || value == expected.RecordingID)
		case "artist_link":
			entry, _ := ch.After.(map[string]any)
			linked = tool == "resolve_artist_identity" && expected.ArtistID != "" && entry["source"] == "musicbrainz" && entry["external_id"] == expected.ArtistID
		case "track_links":
			// 预览只给出数量；逐轨对应在度量时与标注比对。
		case "group_external_link":
			if expected.GroupID == "" || ch.After != expected.GroupID {
				return false, nil
			}
		case "group":
			var matches bool
			err := m.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT FROM external_links WHERE object_type='release_group'
				AND object_id=$1 AND source='musicbrainz' AND external_id=$2)`, ch.After, expected.GroupID).Scan(&matches)
			if err != nil || !matches {
				return false, err
			}
		default:
			want, ok := expected.Fields[ch.Field]
			if !ok || want != fmt.Sprint(ch.After) {
				return false, nil
			}
		}
	}
	return linked, nil
}
