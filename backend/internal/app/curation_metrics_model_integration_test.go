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
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type metricsModelTarget struct {
	SampleID, ReleaseID, GroupID, RecordingID, ArtistID, SearchTitle string
	Fields                                                           map[string]string
}

type metricsModelUsage struct {
	Calls        int `json:"calls"`
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type metricsModel struct {
	mode        string
	server      *httptest.Server
	db          *sql.DB
	gate        sync.RWMutex
	mu          sync.Mutex
	targets     map[string]metricsModelTarget
	counts      map[string]map[string]metricsModelUsage
	routes      map[string][]string
	itemUsage   map[metricsItemStage]metricsItemUsage
	canonical   map[string]string
	typos       map[string]string
	lowNames    map[string]bool
	localReply  func(metricsPending) curation.LocalInput
	rejectMerge bool
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
	m := &metricsModel{mode: mode, targets: map[string]metricsModelTarget{}, counts: map[string]map[string]metricsModelUsage{},
		routes: map[string][]string{}, itemUsage: map[metricsItemStage]metricsItemUsage{}, canonical: map[string]string{},
		typos: map[string]string{}, lowNames: map[string]bool{}}
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

func (m *metricsModel) setPatrolSamples(samples []metricSample) {
	for _, sample := range samples {
		for _, artist := range sample.External.Artists {
			for _, alias := range artist.Aliases {
				m.canonical[alias] = artist.Name + " | " + alias
				m.canonical[artist.Name] = artist.Name + " | " + alias
			}
		}
		if sample.Shape == "title_typo" {
			m.typos[sample.Local.Album] = sample.Expect.Fields["title"]
		}
	}
}

type metricsItemStage struct{ item, stage string }
type metricsItemUsage struct{ calls, tokens int }
type metricsStagePeak struct {
	Items     int `json:"items"`
	ToolCalls int `json:"max_tool_calls"`
	Tokens    int `json:"max_tokens"`
}

func (m *metricsModel) checkItemBudgets(t *testing.T) map[string]metricsStagePeak {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	peaks := map[string]metricsStagePeak{}
	for key, use := range m.itemUsage {
		peak := peaks[key.stage]
		peak.Items++
		peak.ToolCalls = max(peak.ToolCalls, use.calls)
		peak.Tokens = max(peak.Tokens, use.tokens)
		peaks[key.stage] = peak
		maxCalls, maxTokens := 20, 150000
		if key.stage == "web" {
			maxCalls, maxTokens = 30, 300000
		}
		if use.calls > maxCalls || use.tokens > maxTokens {
			t.Errorf("档位 %s 项 %s 超过 D16：工具调用 %d/%d，token %d/%d", key.stage, key.item, use.calls, maxCalls, use.tokens, maxTokens)
		}
	}
	if peaks["lookup"].Items == 0 || peaks["web"].Items == 0 {
		t.Errorf("D16 的两档都应有实际用量：%v", peaks)
	}
	return peaks
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
	Model string
	Tools []struct {
		Type     string
		Function struct{ Name string }
	}
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
	text      string
	sampleIDs []string
	calls     []metricsToolCall
	itemIDs   []string
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
	case "metrics-main", "metrics-main-v2":
		rep, err = m.pendingReply(r.Context(), q)
	case "metrics-review":
		rep, err = m.reviewReply(r.Context(), q)
	default:
		return fmt.Errorf("意外的模型名 %q", q.Model)
	}
	if err != nil {
		return err
	}
	wire, used, err := metricsFrame(rep.text, rep.calls, len(body))
	if err != nil {
		return err
	}
	if len(rep.sampleIDs) == 0 {
		return fmt.Errorf("模型调用缺少样本归属：%s", rep.category)
	}
	// D16 的两个档位按项统计，度量驱动每次只领取一项。
	if rep.category == "lookup" || rep.category == "web" {
		if len(rep.itemIDs) != 1 {
			return fmt.Errorf("档位 %s 本次领取了 %d 项", rep.category, len(rep.itemIDs))
		}
		key := metricsItemStage{rep.itemIDs[0], rep.category}
		use := m.itemUsage[key]
		use.calls += len(rep.calls)
		use.tokens += used.InputTokens + used.OutputTokens
		m.itemUsage[key] = use
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
func metricsFrame(content string, calls []metricsToolCall, inputBytes int) ([]byte, metricsModelUsage, error) {
	delta := map[string]any{"content": content}
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
	ReleaseID  string `json:"release_id"`
	ArtistID   string `json:"artist_id"`
	Candidates []struct {
		ID string `json:"external_id"`
	}
	RawEvidence json.RawMessage `json:"-"`
	Evidence    struct {
		Names                               []string
		Title, Artist, Field, Before, After string
		Tracks                              json.RawMessage
	}
}

func (p *metricsPending) UnmarshalJSON(data []byte) error {
	type pending metricsPending
	envelope := struct {
		*pending
		Evidence json.RawMessage
	}{pending: (*pending)(p)}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return err
	}
	p.RawEvidence = envelope.Evidence
	// 巡检与本地整理使用各自的结构化证据。
	if p.Kind == "name_patrol" || p.Kind == "local_cleanup" {
		return nil
	}
	return json.Unmarshal(envelope.Evidence, &p.Evidence)
}

func metricsPendingItems(content string) ([]metricsPending, bool, error) {
	_, body, ok := strings.Cut(content, "<roomusic-source type=\"curation_pending\" trust=\"untrusted\">")
	if !ok {
		return nil, false, nil
	}
	body, _, ok = strings.Cut(body, "</roomusic-source>")
	if !ok {
		return nil, true, fmt.Errorf("待定项资料缺少结束标签")
	}
	var items []metricsPending
	err := json.Unmarshal([]byte(body), &items)
	return items, true, err
}

func metricsToolBody(content, name string) (string, bool) {
	prefix := `<roomusic-source kind="tool" name="` + name + `">`
	body, ok := strings.CutPrefix(content, prefix)
	if !ok {
		return "", false
	}
	return strings.TrimSpace(html.UnescapeString(strings.TrimSuffix(body, "</roomusic-source>"))), true
}

func (m *metricsModel) pendingReply(_ context.Context, q metricsChatRequest) (metricsModelReply, error) {
	rep := metricsModelReply{text: "处理完成"}
	var pending []metricsPending
	var lastTool, lastResult string
	for _, msg := range q.Messages {
		switch msg.Role {
		case "user":
			items, found, err := metricsPendingItems(msg.Content)
			if err != nil {
				return rep, err
			}
			if found {
				pending, lastTool, lastResult = items, "", ""
			}
		case "assistant":
			for _, tc := range msg.ToolCalls {
				lastTool = tc.Function.Name
			}
		case "tool":
			lastResult, _ = metricsToolBody(msg.Content, lastTool)
		}
	}
	if len(pending) == 0 {
		return rep, fmt.Errorf("首条消息缺少待定项")
	}
	hasTool := func(name string) bool {
		return slices.ContainsFunc(q.Tools, func(t struct {
			Type     string
			Function struct{ Name string }
		}) bool {
			return t.Function.Name == name
		})
	}
	switch {
	case pending[0].Kind == "name_patrol" && hasTool("submit_patrol"):
		rep.category = "patrol"
	case pending[0].Kind == "local_cleanup" && hasTool("submit_local_cleanup"):
		rep.category = "local_cleanup"
	case len(q.Tools) == 0:
		rep.category = "select"
	case hasTool("search_music_metadata"):
		rep.category = "lookup"
	case hasTool("web_search"):
		rep.category = "web"
	default:
		rep.category = "verify"
	}
	var decisions []map[string]string
	for _, item := range pending {
		id := item.ReleaseID
		if id == "" {
			id = item.ArtistID
		}
		target, ok := m.targets[id]
		if !ok {
			return rep, fmt.Errorf("待定项 %s 本地目标 %s 没有样本标注", item.ID, id)
		}
		rep.sampleIDs = append(rep.sampleIDs, target.SampleID)
		rep.itemIDs = append(rep.itemIDs, item.ID)
		route := m.routes[item.ID]
		if len(route) == 0 || route[len(route)-1] != rep.category {
			m.routes[item.ID] = append(route, rep.category)
		}
		if rep.category == "select" {
			decision := map[string]string{"item_id": item.ID, "decision": "upgrade"}
			for _, candidate := range item.Candidates {
				if m.mode == "first_candidate" || candidate.ID == target.ReleaseID || candidate.ID == target.RecordingID {
					decision["decision"], decision["external_id"] = "select", candidate.ID
					break
				}
			}
			decisions = append(decisions, decision)
			continue
		}
		if rep.category == "patrol" && lastTool == "" {
			call, err := m.patrolTool(item)
			if err != nil {
				return rep, err
			}
			rep.calls = append(rep.calls, call)
			continue
		}
		if call := m.nextTool(item, target, rep.category, lastTool, lastResult); call.name != "" {
			rep.calls = append(rep.calls, call)
		}
	}
	if rep.category == "select" {
		raw, err := json.Marshal(map[string]any{"items": decisions})
		rep.text = string(raw)
		return rep, err
	}
	return rep, nil
}

type metricsPatrolBatch struct {
	Kind  string
	Names []struct{ ID, Name string }
}

func (m *metricsModel) patrolTool(item metricsPending) (metricsToolCall, error) {
	var batch metricsPatrolBatch
	if err := json.Unmarshal(item.RawEvidence, &batch); err != nil {
		return metricsToolCall{}, err
	}
	reply := curation.PatrolSubmission{ItemID: item.ID, Results: []curation.PatrolResult{}, Issues: []curation.PatrolIssue{}}
	for _, n := range batch.Names {
		if batch.Kind == "artist" {
			canonical, confidence := m.canonical[n.Name], "high"
			if canonical == "" {
				canonical = n.Name + " | "
			}
			if m.lowNames[n.Name] {
				confidence = "low"
			}
			reply.Results = append(reply.Results, curation.PatrolResult{ID: n.ID, Canonical: canonical, Confidence: confidence})
		}
		if batch.Kind == "album" && m.typos[n.Name] != "" {
			reply.Issues = append(reply.Issues, curation.PatrolIssue{ID: n.ID, Kind: "typo", Suggested: m.typos[n.Name]})
		}
	}
	return metricsToolCall{"submit_patrol", reply}, nil
}

func (m *metricsModel) nextTool(item metricsPending, target metricsModelTarget, stage, lastTool, result string) metricsToolCall {
	abandon := metricsToolCall{"shelve_pending_item", map[string]string{"item_id": item.ID, "reason": "当前证据无法确认对应。"}}
	match := func(id string) metricsToolCall {
		return metricsToolCall{"resolve_release_match", map[string]any{"item_id": item.ID, "external_id": id, "basis": map[string]string{"note": "已读取外部候选。"}}}
	}
	switch stage {
	case "patrol":
		return metricsToolCall{}
	case "local_cleanup":
		if lastTool != "" {
			return metricsToolCall{}
		}
		input := curation.LocalInput{ItemID: item.ID}
		if m.localReply != nil {
			input = m.localReply(item)
			input.ItemID = item.ID
		}
		return metricsToolCall{"submit_local_cleanup", input}
	case "lookup":
		entity, field, title, expected := "release", "release", item.Evidence.Title, target.ReleaseID
		if item.Kind == "recording_match" {
			entity, field, expected = "recording", "recording", target.RecordingID
			var tracks []struct{ Title string }
			if json.Unmarshal(item.Evidence.Tracks, &tracks) == nil && len(tracks) > 0 {
				title = tracks[0].Title
			}
		}
		switch lastTool {
		case "":
			if m.mode == "oracle" && target.SearchTitle != "" {
				title = target.SearchTitle
			}
			query := field + ":" + fmt.Sprintf("%q", title)
			if title == "" {
				query = "artist:" + fmt.Sprintf("%q", item.Evidence.Artist)
			}
			if title == "" && item.Evidence.Artist == "" {
				return abandon
			}
			return metricsToolCall{"search_music_metadata", map[string]any{"entity": entity, "query": query, "limit": 10}}
		case "search_music_metadata":
			var found struct{ Results []struct{ MBID string } }
			_ = json.Unmarshal([]byte(result), &found)
			for _, candidate := range found.Results {
				if m.mode == "first_candidate" || candidate.MBID == expected {
					include := []string{"artist-credits"}
					if entity == "release" {
						include = append(include, "recordings")
					}
					return metricsToolCall{"get_music_metadata", map[string]any{"entity": entity, "mbid": candidate.MBID, "include": include}}
				}
			}
		case "get_music_metadata":
			var found struct{ Metadata struct{ MBID string } }
			if json.Unmarshal([]byte(result), &found) == nil && uuid.Valid(found.Metadata.MBID) {
				return match(found.Metadata.MBID)
			}
		case "resolve_release_match":
			return metricsToolCall{}
		}
	case "web":
		switch lastTool {
		case "":
			query := strings.TrimSpace(item.Evidence.Artist + " " + item.Evidence.Title + " " + strings.Join(item.Evidence.Names, " "))
			if query == "" {
				query = "未标记的本地发行"
			}
			return metricsToolCall{"web_search", map[string]string{"q": query}}
		case "web_search":
			var found struct{ Results []struct{ URL string } }
			_ = json.Unmarshal([]byte(result), &found)
			for _, candidate := range found.Results {
				id, ok := strings.CutPrefix(candidate.URL, "https://musicbrainz.org/artist/")
				if item.Kind == "artist_identity" && ok && uuid.Valid(id) && (m.mode == "first_candidate" || id == target.ArtistID) {
					return metricsToolCall{"resolve_artist_identity", map[string]any{"item_id": item.ID, "source": "musicbrainz", "external_id": id,
						"basis": map[string]any{"note": "网页搜索查到艺人。", "citations": []string{candidate.URL}}}}
				}
			}
		case "resolve_artist_identity":
			return metricsToolCall{}
		}
	case "verify":
		if lastTool != "" {
			return metricsToolCall{}
		}
		verdict := "uncertain"
		switch item.Kind {
		case "verify_name":
			if item.Evidence.After != "" && item.Evidence.After == target.Fields[item.Evidence.Field] {
				verdict = "typo"
			}
		case "verify_artist_credit":
			verdict = "different"
		case "verify_artist_merge":
			verdict = "different"
			if canonical := m.canonical[item.Evidence.Before]; !m.rejectMerge && canonical != "" && canonical == m.canonical[item.Evidence.After] {
				verdict = "same"
			}
		default:
			return abandon
		}
		return metricsToolCall{"submit_verification", map[string]string{"item_id": item.ID, "verdict": verdict, "reason": "按照合成样本的标注核对。"}}
	}
	return abandon
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
	linked := tool == "submit_verification" && len(e.Preview.Data) > 0
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
