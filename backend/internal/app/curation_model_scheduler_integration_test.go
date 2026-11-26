package app

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/curation/model"
	"github.com/qlqq/roomusic/backend/internal/curation/program"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/coverartarchive"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
)

const unavailableMusicBrainzID = "9634b40c-a111-493b-abae-015ce393854b"

const stewardBase = "/api/v1/me/steward"

type schedulerRequest struct {
	MaxCompletionTokens int `json:"max_completion_tokens"`
	MaxTokens           int `json:"max_tokens"`
	Model               string
	ToolChoice          json.RawMessage `json:"tool_choice"`
	Tools               []struct {
		Type     string
		Function struct{ Name string }
	}
	Messages []struct {
		Role    string
		Content json.RawMessage
	}
	inputTokens int
	reply       chan string
	fail        chan schedulerHTTPFailure
}

type schedulerHTTPFailure struct {
	status        int
	retryAfter    string
	code, message string
}

func (q schedulerRequest) outputLimit() int {
	return max(q.MaxCompletionTokens, q.MaxTokens)
}

func (q schedulerRequest) tool(t *testing.T, name string, input any) {
	t.Helper()
	args, err := json.Marshal(input)
	requireCurationOK(t, err)
	call := map[string]any{"index": 0, "id": uuid.New(), "type": "function", "function": map[string]any{"name": name, "arguments": string(args)}}
	q.reply <- q.frame(t, map[string]any{"tool_calls": []any{call}}, "tool_calls")
}

func (q schedulerRequest) frame(t *testing.T, delta any, reason string) string {
	t.Helper()
	tokens := q.inputTokens
	if tokens == 0 {
		tokens = 13
	}
	raw, err := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": reason}},
		"usage": map[string]int{"prompt_tokens": tokens, "completion_tokens": 7}})
	requireCurationOK(t, err)
	return "data: " + string(raw) + "\n\ndata: [DONE]\n\n"
}

func (q schedulerRequest) complete(t *testing.T) {
	t.Helper()
	q.reply <- q.frame(t, map[string]string{"content": "完成"}, "stop")
}

func (q schedulerRequest) selectCandidate(t *testing.T, itemID, externalID string) {
	t.Helper()
	if len(q.Tools) != 0 {
		t.Fatalf("第一档附带了工具：%+v", q.Tools)
	}
	decision := map[string]string{"item_id": itemID, "decision": "undetermined", "reason": "没有可核实的候选"}
	if externalID != "" {
		decision["decision"], decision["selected_id"] = "judge", externalID
	}
	raw, err := json.Marshal(map[string]any{"items": []any{decision}})
	requireCurationOK(t, err)
	q.reply <- q.frame(t, map[string]string{"content": string(raw)}, "stop")
}

func (q schedulerRequest) review(t *testing.T, decision string) {
	t.Helper()
	var content string
	requireCurationOK(t, json.Unmarshal(q.Messages[len(q.Messages)-1].Content, &content))
	var evidence struct {
		Items []struct {
			ID string `json:"item_id"`
		}
	}
	requireCurationOK(t, json.Unmarshal([]byte(content), &evidence))
	if len(evidence.Items) != 1 || !uuid.Valid(evidence.Items[0].ID) {
		t.Fatalf("审查收到的方案项 %s", content)
	}
	q.tool(t, "submit_review", map[string]any{"items": []any{map[string]any{"item_id": evidence.Items[0].ID, "decision": decision, "risk": "low", "reasons": []string{"合成审查理由"}}}})
}

type schedulerFixture struct {
	*curationFixture
	clock        *programClock
	requests     chan schedulerRequest
	requestCount atomic.Int32
	endpoint     string
	remote       b3RemoteRelease
}

func newSchedulerFixture(t *testing.T, opts ...Option) *schedulerFixture {
	t.Helper()
	return newSchedulerFixtureWithSources(t, nil, nil, opts...)
}

func newSchedulerFixtureWithSources(t *testing.T, lookup model.LookupQueue, fakeSource http.Handler, opts ...Option) *schedulerFixture {
	t.Helper()
	f := &schedulerFixture{clock: newProgramClock(), requests: make(chan schedulerRequest), remote: b3NewRemoteRelease()}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mb/release/"+unavailableMusicBrainzID {
			w.Header().Set("Retry-After", "0")
			http.Error(w, "合成资料源故障", http.StatusServiceUnavailable)
			return
		}
		if r.URL.Path == "/mb/release-group/"+f.remote.Group {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"id":%q,"title":"合成正式专辑","releases":[{"id":%q,"title":"合成正式专辑"}]}`, f.remote.Group, f.remote.ID)
			return
		}
		if r.URL.Path == "/mb/release/"+f.remote.ID {
			w.Header().Set("Content-Type", "application/json")
			f.remote.writeMusicBrainz(w)
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			if fakeSource != nil {
				fakeSource.ServeHTTP(w, r)
			} else {
				http.NotFound(w, r)
			}
			return
		}
		q := schedulerRequest{reply: make(chan string, 1), fail: make(chan schedulerHTTPFailure, 1)}
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		f.requestCount.Add(1)
		select {
		case f.requests <- q:
		case <-r.Context().Done():
			return
		case <-t.Context().Done():
			return
		}
		select {
		case wire := <-q.reply:
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, wire)
		case failure := <-q.fail:
			if failure.retryAfter != "" {
				w.Header().Set("Retry-After", failure.retryAfter)
			}
			if failure.code == "" && failure.message == "" {
				http.Error(w, "合成模型服务暂不可用", failure.status)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(failure.status)
			fmt.Fprintf(w, `{"error":{"code":%q,"message":%q}}`, failure.code, failure.message)
		case <-r.Context().Done():
		case <-t.Context().Done():
		}
	}))
	t.Cleanup(server.Close)
	f.endpoint = server.URL
	src := func(path string) sources.Options {
		return sources.Options{BaseURL: server.URL + path, NewClient: b3HTTPClient(server), Limiter: sources.NewLimiter(0, nil, nil)}
	}
	opts = append([]Option{WithCurationModel(true, model.Options{Now: f.clock.options().Now, PollInterval: 20 * time.Millisecond, Lookup: lookup}),
		WithCurationSources(src("/mb"), coverartarchive.Options{BaseURL: server.URL + "/covers", NewClient: b3HTTPClient(server), Limiter: sources.NewLimiter(0, nil, nil), ManifestLimiter: sources.NewLimiter(0, nil, nil)}, src("/lyrics"))}, opts...)
	logger := slog.New(slog.NewTextHandler(t.Output(), &slog.HandlerOptions{Level: slog.LevelError}))
	g := newGroupEnvironmentForApp(t, newIntegrationTestApplicationWithLogger(t, logger, opts...))
	f.curationFixture = &curationFixture{&operationsEnvironment{g}, curation.NewWriter(g.application.database, g.application.dataDirectory, nil)}
	return f
}

func (f *schedulerFixture) configure(t *testing.T, u *integrationTestUser) {
	t.Helper()
	resp := f.application.request(t, "PUT", stewardBase+"/settings", map[string]string{"endpoint": f.endpoint, "protocol": "openai", "model": "test-main", "review_model": "test-review"}, u, nil)
	if resp.Code != 200 {
		t.Fatalf("配置模型 HTTP %d %s", resp.Code, resp.Body)
	}
}

func (f *schedulerFixture) next(t *testing.T, review bool) schedulerRequest {
	t.Helper()
	want := "test-main"
	if review {
		want = "test-review"
	}
	return f.nextModel(t, want, review)
}

func (f *schedulerFixture) nextModel(t *testing.T, want string, review bool) schedulerRequest {
	t.Helper()
	select {
	case q := <-f.requests:
		if q.Model != want || (len(q.ToolChoice) > 0) != review || len(q.Messages) == 0 {
			t.Fatalf("请求模型=%q，tool_choice=%s，期望 %q", q.Model, q.ToolChoice, want)
		}
		return q
	case <-time.After(5 * time.Second):
		t.Fatalf("未收到模型请求（审查=%t）；后台工具 %s", review, f.modelToolResults(t))
		return schedulerRequest{}
	}
}

func (f *schedulerFixture) nextAfterPatrol(t *testing.T) schedulerRequest {
	t.Helper()
	for {
		q := f.next(t, false)
		if !slices.ContainsFunc(q.Tools, func(tool struct {
			Type     string
			Function struct{ Name string }
		}) bool {
			return tool.Function.Name == "submit_patrol"
		}) {
			return q
		}
		var content string
		requireCurationOK(t, json.Unmarshal(q.Messages[len(q.Messages)-1].Content, &content))
		items, _, err := metricsPendingItems(content)
		requireCurationOK(t, err)
		if len(items) != 1 || items[0].Kind != "name_patrol" {
			t.Fatalf("应收到一块巡检资料，得到 %+v", items)
		}
		response, err := (&metricsModel{}).patrolTool(items[0], nil)
		requireCurationOK(t, err)
		q.tool(t, response.name, response.args)
	}
}

func (f *schedulerFixture) modelToolResults(t *testing.T) string {
	t.Helper()
	return programString(t, f.application, `SELECT coalesce(json_agg(r)::text,'[]') FROM (
		SELECT calls.tool_name,calls.status,calls.error_code,calls.output FROM steward_tool_calls calls
		JOIN steward_messages m ON m.id=calls.message_id JOIN steward_conversations c ON c.id=m.conversation_id
		WHERE c.kind='curation' ORDER BY c.created_at DESC,m.sequence DESC,calls.position DESC LIMIT 8
	) r`)
}

func (f *schedulerFixture) progress(t *testing.T) curation.ModelProgress {
	t.Helper()
	var body struct{ Model curation.ModelProgress }
	getJSON(t, f.application, &f.admin, curationBase+"/progress", &body)
	return body.Model
}

func (f *schedulerFixture) pending(t *testing.T, target string) curation.PendingItem {
	t.Helper()

	candidates, err := json.Marshal([]map[string]string{{"external_id": f.remote.ID}})
	requireCurationOK(t, err)
	requireCurationOK(t, curation.UpsertPendingItem(t.Context(), f.application.database, curation.PendingItem{
		TargetID: target, Kind: "release_match", RuleVersion: program.StoredRuleVersion, InputDigest: modelDigestForTarget(t, f, target),
		Evidence: []byte(`{"title":"合成资料"}`), Candidates: candidates, Conflicts: []byte(`["search_only"]`),
	}))
	id := programString(t, f.application, `SELECT id FROM curation_pending_items WHERE target_id=$1 AND kind='release_match' AND status<>'resolved'`, target)
	return f.item(t, id)
}

func modelDigestForTarget(t *testing.T, f *schedulerFixture, target string) string {
	t.Helper()
	rows, err := catalog.LoadCurationEvidence(t.Context(), f.application.database, []string{target}, f.writer.ArtworkExists)
	requireCurationOK(t, err)
	return program.ReleaseDigest(rows[target])
}

func (f *schedulerFixture) item(t *testing.T, id string) curation.PendingItem {
	t.Helper()
	item, err := f.writer.PendingItem(t.Context(), id)
	requireCurationOK(t, err)
	return item
}

func schedulerWait(t *testing.T, description string, condition func() bool) {
	t.Helper()
	end := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(end) {
			t.Fatalf("等待超时：%s", description)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (f *schedulerFixture) pause(t *testing.T) {
	t.Helper()
	_, err := f.writer.SetPaused(t.Context(), f.admin.id, true)
	requireCurationOK(t, err)
}

func TestPostgreSQLCurationModelStartsCurationConversation(t *testing.T) {
	f := newSchedulerFixture(t)
	f.configure(t, &f.admin)
	var ids []string
	for i := 0; i < 6; i++ {
		target, _ := insertGroupedRelease(t, f.application, f.rootID, groupedRelease{title: fmt.Sprintf("合成发行 %d", i), artist: "合成艺人", tracks: []string{"present"}})
		item := f.pending(t, target)
		ids = append(ids, item.ID)
	}
	f.enable(t)
	request := f.next(t, false)
	first := f.item(t, ids[0])
	store := runtime.NewStore(f.application.database)
	detail, err := store.GetConversation(t.Context(), f.admin.id, first.ConversationID)
	requireCurationOK(t, err)
	if c := detail.Conversation; c.Kind != "curation" || c.Mode != "steward" || c.UserID != f.admin.id || len(detail.Messages) != 1 || detail.Messages[0].Role != "user" {
		t.Fatalf("自动整理会话 %+v", detail)
	}
	for i, id := range ids {
		item := f.item(t, id)
		inBatch := i < 5
		if strings.Contains(string(detail.Messages[0].Content), id) != inBatch || (item.Status == "in_progress" && item.ConversationID == first.ConversationID) != inBatch {
			t.Fatalf("第 %d 项 %+v，首条消息 %s", i, item, detail.Messages[0].Content)
		}
	}
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM steward_conversations`)
	f.pause(t)
	request.complete(t)
}

func TestPostgreSQLCurationModelShelveAndReopen(t *testing.T) {
	f := newSchedulerFixture(t)
	item := f.pending(t, f.a)
	conversation := modelConversation(t, f.curationFixture)
	modelClaim(t, f.curationFixture, conversation, item)
	requireCurationOK(t, f.writer.ShelvePendingItem(t.Context(), item.ID, conversation, "历史搁置项"))
	requireCurationOK(t, curation.UpsertPendingItem(t.Context(), f.application.database, item))
	if got := f.item(t, item.ID); got.Status != "shelved" {
		t.Fatalf("证据相同后 %+v", got)
	}
	mustExec(t, f.application, `UPDATE releases SET barcode='123456789012' WHERE id=$1`, f.a)
	again := f.pending(t, f.a)
	if again.ID != item.ID || again.Status != "waiting_model" || again.ShelvedReason != nil || again.Attempts != 0 {
		t.Fatalf("证据变化后 %+v", again)
	}
}

func TestPostgreSQLCurationModelUnhandledThreeTimes(t *testing.T) {
	gate := &testLookupGate{}
	f := newSchedulerFixtureWithSources(t, gate, nil)
	f.configure(t, &f.admin)
	configureModelWeb(t, f)
	item := modelRouteFixture(t, f, f.pending(t, f.a), "")
	f.enable(t)
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		q := f.next(t, false)
		got := f.item(t, item.ID)
		if got.Attempts != 0 || got.EmptyCount != i || got.Status != "in_progress" || seen[got.ConversationID] {
			t.Fatalf("第 %d 次领取 %+v", i+1, got)
		}
		seen[got.ConversationID] = true
		q.complete(t)
		if i < 2 {
			schedulerWait(t, "空答后等待重试", func() bool { return f.item(t, item.ID).NextAttemptAt != nil })
			f.clock.now.Store(f.item(t, item.ID).NextAttemptAt.UnixNano())
		}
	}
	schedulerWait(t, "连续三次空答后搁置", func() bool { return f.item(t, item.ID).Status == "shelved" })
	got := f.item(t, item.ID)
	if got.Attempts != 0 || got.EmptyCount != 3 || got.ShelvedReason == nil || *got.ShelvedReason != "模型没给出结果" {
		t.Fatalf("空答计次或结案原因错误：%+v", got)
	}
	// 单独准备过渡期的旧档位，继续验证其原有放弃规则。
	item = modelRouteFixture(t, f, f.pending(t, f.b), "lookup")
	gate.ready.Store(true)
	for _, route := range []string{"lookup", "web"} {
		for attempt := 0; attempt < 3; attempt++ {
			q := f.next(t, false)
			got := f.item(t, item.ID)
			if got.Route != route || int(got.Attempts) != attempt || got.ShelvedReason != nil {
				t.Fatalf("期望 %s 第 %d 次领取，实际 %+v", route, attempt+1, got)
			}
			q.complete(t)
		}
	}
	modelWaitResolution(t, f, item.ID, "unlisted")
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_work_states WHERE target_id=$1 AND status='conflict' AND next_attempt_at IS NULL`, f.b)

	t.Run("第二档无联网来源", func(t *testing.T) {
		gate := &testLookupGate{}
		gate.ready.Store(true)
		f := newSchedulerFixtureWithSources(t, gate, nil)
		f.configure(t, &f.admin)
		item := modelRouteFixture(t, f, f.pending(t, f.a), "lookup")
		f.enable(t)
		for range 3 {
			f.next(t, false).complete(t)
		}
		modelWaitResolution(t, f, item.ID, "unlisted")
		b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_work_states WHERE target_id=$1 AND status='conflict' AND next_attempt_at IS NULL`, f.a)
	})
}

func TestPostgreSQLCurationModelConcurrency(t *testing.T) {
	f := newSchedulerFixture(t, WithCurationModel(true, model.Options{PollInterval: time.Hour}))
	f.configure(t, &f.admin)
	newRelease := func(title, artist string) string {
		id, _ := insertGroupedRelease(t, f.application, f.rootID, groupedRelease{title: title, artist: artist, tracks: []string{"present"}})
		return id
	}
	verify := func(release string) string {
		return queueModelVerification(t, f, "verify_name", curation.VerificationCandidate{
			ReleaseID: release, Field: "title", Before: "原题", After: "原提", Structure: true,
		})
	}
	next := func() (metricsPending, schedulerRequest) {
		t.Helper()
		q := f.next(t, false)
		var prompt string
		requireCurationOK(t, json.Unmarshal(q.Messages[len(q.Messages)-1].Content, &prompt))
		items, _, err := metricsPendingItems(prompt)
		requireCurationOK(t, err)
		if len(items) != 1 {
			t.Fatalf("期望单项模型请求，收到 %s", prompt)
		}
		return items[0], q
	}
	checkProgress := func(running, concurrency int) {
		t.Helper()
		p := f.progress(t)
		if p.Running != running || p.Concurrency != concurrency || p.State != "running" {
			t.Fatalf("期望运行 %d 项、并发数 %d，进度 %+v", running, concurrency, p)
		}
	}

	f.pending(t, f.a)
	f.update(t, "PUT", curationBase+"/settings", map[string]any{"enabled": true, "model_concurrency": 1})
	patrol, patrolRequest := next()
	if patrol.Kind != "release_match" || patrol.ReleaseID != f.a {
		t.Fatalf("期望先领取发行巡检，收到 %+v", patrol)
	}
	checkProgress(1, 1)

	sameRelease := verify(f.a)
	sameArtist := verify(newRelease("同艺人的另一张专辑", "Artist A"))
	verifyID := verify(newRelease("待核对专辑", "核对艺人"))
	cleanup := modelPending(t, f.curationFixture, f.b, "local_cleanup", "合成本地整理")
	mustExec(t, f.application, `UPDATE curation_pending_items SET evidence='{"directory_key":"合成目录"}' WHERE id=$1`, cleanup.ID)
	selection := modelRouteFixture(t, f, f.pending(t, newRelease("待选专辑", "候选艺人")), "")
	// 第一档候选已保存完整明细，本次无需向 MusicBrainz 查询。
	candidates, err := json.Marshal([]program.Candidate{{ExternalID: f.remote.ID, Release: &sources.Release{
		ID: f.remote.ID, Title: "待选专辑", Complete: true, Media: []sources.Medium{{Position: 1, Tracks: []sources.Track{{Position: 1, Title: "合成曲目"}}}},
	}}})
	requireCurationOK(t, err)
	mustExec(t, f.application, `UPDATE curation_pending_items SET candidates=$2 WHERE id=$1`, selection.ID, candidates)

	f.update(t, "PUT", curationBase+"/settings", map[string]int{"model_concurrency": 4})
	active := map[string]schedulerRequest{patrol.ID: patrolRequest}
	for range 3 {
		item, request := next()
		active[item.ID] = request
	}
	for _, id := range []string{patrol.ID, cleanup.ID, selection.ID, verifyID} {
		if _, ok := active[id]; !ok {
			t.Fatalf("并发请求缺少项 %s，实际领取巡检、本地整理、选择、核对共 %d 项", id, len(active))
		}
	}
	for _, id := range []string{sameRelease, sameArtist} {
		if p := f.item(t, id); p.Status != "waiting_model" || p.ConversationID != "" {
			t.Fatalf("巡检运行时已领取冲突项 %+v", p)
		}
	}
	checkProgress(4, 4)
	f.update(t, "PUT", curationBase+"/settings", map[string]int{"model_concurrency": 1})
	checkProgress(4, 1)
	spare := verify(newRelease("可补位专辑", "补位艺人"))
	active[selection.ID].selectCandidate(t, selection.ID, "")
	delete(active, selection.ID)
	schedulerWait(t, "第一档结束，保留其余三个在途请求", func() bool {
		return f.item(t, selection.ID).Status == "resolved" && f.progress(t).Running == 3
	})
	checkProgress(3, 1)
	if p := f.item(t, spare); p.Status != "waiting_model" {
		t.Fatalf("调低并发后提前补位 %+v", p)
	}
	f.update(t, "PUT", curationBase+"/settings", map[string]int{"model_concurrency": 4})
	item, request := next()
	if item.ID != spare {
		t.Fatalf("期望调高后立即领取补位项 %s，实际 %+v", spare, item)
	}
	active[item.ID] = request
	checkProgress(4, 4)
	f.pause(t)
	for _, q := range active {
		q.complete(t)
	}
	schedulerWait(t, "暂停后全部在途项退出", func() bool { return f.progress(t).Running == 0 })
}

func curationRulePending(t *testing.T, f *schedulerFixture, label string) curation.PendingItem {
	t.Helper()
	target, _ := insertGroupedRelease(t, f.application, f.rootID, groupedRelease{
		title: "合成标题 " + label, artist: "合成艺人 " + label, tracks: []string{"present"},
	})
	return f.pending(t, target)
}

func curationRulePrompt(t *testing.T, request schedulerRequest) string {
	t.Helper()
	var prompt string
	requireCurationOK(t, json.Unmarshal(request.Messages[len(request.Messages)-1].Content, &prompt))
	return prompt
}

func requireCurationRuleItems(t *testing.T, request schedulerRequest, want ...string) {
	t.Helper()
	prompt := curationRulePrompt(t, request)
	items, found, err := metricsPendingItems(prompt)
	requireCurationOK(t, err)
	var ids []string
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	if !found || !slices.Equal(ids, want) {
		t.Fatalf("请求项=%v，期望 %v", ids, want)
	}
}

func (q schedulerRequest) undetermined(t *testing.T, id, reason string) {
	t.Helper()
	decision, err := json.Marshal(map[string]any{"items": []map[string]string{{
		"item_id": id, "decision": "undetermined", "reason": reason,
	}}})
	requireCurationOK(t, err)
	q.reply <- q.frame(t, map[string]string{"content": string(decision)}, "stop")
}

func TestPostgreSQLCurationModelRulesPartialReply(t *testing.T) {
	f := newSchedulerFixture(t)
	f.configure(t, &f.admin)
	a, b := f.pending(t, f.a), f.pending(t, f.b)
	f.update(t, "PUT", curationBase+"/settings", map[string]any{"enabled": true, "model_concurrency": 1})
	const reason = "合成曲序证据无法区分两个候选"
	first := f.next(t, false)
	requireCurationRuleItems(t, first, a.ID, b.ID)
	first.undetermined(t, a.ID, reason)
	modelWaitResolution(t, f, a.ID, "undetermined")
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_pending_items WHERE id=$1 AND evidence->>'decision_reason'=$2 AND attempts=1 AND empty_count=0`, a.ID, reason)

	for i, seconds := range []int{5, 10, 0} {
		count := i + 1
		schedulerWait(t, "只有缺答项累加空答", func() bool { return f.item(t, b.ID).EmptyCount == count })
		p := f.item(t, b.ID)
		b3RequireCount(t, f.application, 1, `SELECT cardinality(streak_items) FROM curation_model_state`)
		if p.Attempts != 0 || p.SingleRequest {
			t.Fatalf("空答未经模型回答且不触发拆单：%+v", p)
		}
		if seconds == 0 {
			if p.Status != "shelved" || p.NextAttemptAt != nil || p.ShelvedReason == nil || *p.ShelvedReason != "模型没给出结果" {
				t.Fatalf("第三次空答应该结案：%+v", p)
			}
			break
		}
		deadline := f.clock.options().Now().Add(time.Duration(seconds) * time.Second)
		if p.Status != "waiting_model" || p.NextAttemptAt == nil || !p.NextAttemptAt.Equal(deadline) {
			t.Fatalf("第 %d 次空答后应在 %v 再试：%+v", count, deadline, p)
		}
		f.clock.now.Store(deadline.UnixNano())
		next := f.next(t, false)
		requireCurationRuleItems(t, next, b.ID)
		next.complete(t)
	}
	requireCurationRuleRequestCount(t, f, 3)
}

func TestPostgreSQLCurationModelRulesFailureStreak(t *testing.T) {
	for _, tc := range []struct {
		name, pause string
		failure     curation.ModelFailure
	}{
		{"相同拒绝", "configuration", curation.ModelFailure{Class: "request_rejected", Status: 400, Code: "invalid_request_error", Message: "输入  类型错误 req-123"}},
		{"不同项首次空答", "empty_replies", curation.ModelFailure{Class: "empty_reply"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clock := newProgramClock()
			f := newSchedulerFixture(t, WithCurationModel(false, model.Options{Now: clock.options().Now}))
			f.configure(t, &f.admin)
			f.enable(t)
			if p := f.progress(t); !p.Configured || p.Model != "test-main" {
				t.Fatalf("应从已保存设置同步配置代次：%+v", p)
			}
			state, err := f.writer.ModelState(t.Context())
			requireCurationOK(t, err)
			config := state.ConfigKey
			requireState := func(wantSize int, wantPause string) {
				t.Helper()
				b3RequireCount(t, f.application, wantSize, `SELECT cardinality(streak_items) FROM curation_model_state`)
				state, err := f.writer.ModelState(t.Context())
				requireCurationOK(t, err)
				if wantPause == "" && state.Pause != nil || wantPause != "" && (state.Pause == nil || *state.Pause != wantPause) {
					t.Fatalf("连续 %d 项，期望暂停 %q，实际 %+v", wantSize, wantPause, state)
				}
			}
			newClaim := func() (string, curation.PendingItem) {
				t.Helper()
				label := uuid.New()
				target, _ := insertGroupedRelease(t, f.application, f.rootID, groupedRelease{title: "合成发行 " + label, artist: "合成艺人 " + label, tracks: []string{"present"}})
				item := modelPending(t, f.curationFixture, target, "release_match", label)
				conversation := modelConversation(t, f.curationFixture)
				items, err := f.writer.ClaimPreparedSelection(t.Context(), conversation, clock.options().Now(), []curation.PendingItem{item})
				requireCurationOK(t, err)
				if len(items) != 1 || items[0].ID != item.ID {
					t.Fatalf("期望领取 %s，实际 %+v", item.ID, items)
				}
				return conversation, items[0]
			}
			finish := func(failure curation.ModelFailure) {
				t.Helper()
				conversation, item := newClaim()
				requireCurationOK(t, f.writer.FailModelItems(t.Context(), config, conversation, []curation.PendingItem{item}, failure, clock.options().Now()))
			}
			if tc.failure.Class == "request_rejected" {
				finish(tc.failure)
				finish(tc.failure)
				other := tc.failure
				other.Message = "不同原因"
				finish(other)
				requireState(1, "")
			}
			finish(tc.failure)
			conversation, answer := newClaim()
			answerCtx := curation.WithModelConfig(t.Context(), config)
			requireCurationOK(t, f.writer.FinishUndetermined(answerCtx, answer, conversation, "合成结案理由"))
			requireState(0, "")

			for range 8 {
				finish(tc.failure)
			}
			conversation, item := newClaim()
			failure := tc.failure
			if failure.Class == "request_rejected" {
				failure.Message = "输入 类型错误 req-456"
			}
			results := make(chan error, 2)
			for range 2 {
				go func() {
					results <- f.writer.FailModelItems(t.Context(), config, conversation, []curation.PendingItem{item}, failure, clock.options().Now())
				}()
			}
			for range 2 {
				requireCurationOK(t, <-results)
			}
			requireState(9, "")
			finish(tc.failure)
			requireState(10, tc.pause)
			progress := requireCurationRulePause(t, f, tc.pause, nil)
			if tc.pause == "empty_replies" && (progress.ReasonDetail == nil || !strings.Contains(*progress.ReasonDetail, "建议换一个") || !progress.CanRetry) {
				t.Fatalf("十项首次空答应显示更换模型提示和重试入口：%+v", progress)
			}
			b3RequireCount(t, f.application, 0, `SELECT count(*) FROM curation_pending_items WHERE status <> 'resolved' AND attempts<>0`)
			_, err = f.writer.SyncModelConfig(t.Context(), "new-config", "new-model")
			requireCurationOK(t, err)
			requireState(0, "")
			requireCurationOK(t, f.writer.SetModelFailure(t.Context(), config,
				curation.ModelFailure{Class: "configuration", Status: 401, Code: "invalid_api_key"}, clock.options().Now()))
			requireState(0, "")
			b3RequireCount(t, f.application, 0, `SELECT count(*) FROM curation_pending_items WHERE status<>'resolved' AND (status<>'waiting_model' OR empty_count<>0 OR model_stop_code IS NOT NULL)`)
			b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_pending_items WHERE id=$1 AND attempts=1 AND resolution='undetermined'`, answer.ID)
		})
	}
}

func TestPostgreSQLCurationModelRulesBatch400(t *testing.T) {
	for _, tc := range []struct {
		name    string
		failure schedulerHTTPFailure
	}{
		{"普通400拆单", schedulerHTTPFailure{status: 400, code: "invalid_request_error", message: "合成批请求被拒"}},
		{"欠费400不拆", schedulerHTTPFailure{status: 400, code: "insufficient_balance", message: "余额不足"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSchedulerFixture(t)
			f.configure(t, &f.admin)
			a, b := f.pending(t, f.a), f.pending(t, f.b)
			// 批拒绝时仍可判断当前名额是否已经释放。
			f.update(t, "PUT", curationBase+"/settings", map[string]any{"enabled": true, "model_concurrency": 1})
			first := f.next(t, false)
			requireCurationRuleItems(t, first, a.ID, b.ID)
			first.fail <- tc.failure
			if tc.failure.code == "insufficient_balance" {
				next := f.clock.options().Now().Add(time.Hour)
				requireCurationRulePause(t, f, "provider_quota", &next)
				requireCurationRuleRequestCount(t, f, 1)
				b3RequireCount(t, f.application, 2, `SELECT count(*) FROM curation_pending_items WHERE id IN ($1,$2) AND NOT single_request AND attempts=0 AND status='waiting_model'`, a.ID, b.ID)
				return
			}

			single := f.next(t, false)
			requireCurationRuleItems(t, single, a.ID)
			if !f.item(t, a.ID).SingleRequest || !f.item(t, b.ID).SingleRequest {
				t.Fatal("批拒绝后的两个原项都应记录持久化的单发标记")
			}
			single.fail <- tc.failure
			peer := f.next(t, false)
			requireCurationRuleItems(t, peer, b.ID)
			rejected := f.item(t, a.ID)
			if rejected.Status != "shelved" || rejected.Attempts != 0 || rejected.ShelvedReason == nil || !strings.Contains(*rejected.ShelvedReason, "模型拒绝了请求") {
				t.Fatalf("单项拒绝应终止该项且允许同批另一项继续：%+v", rejected)
			}
			peer.undetermined(t, b.ID, "合成候选没有足够证据")
			modelWaitResolution(t, f, b.ID, "undetermined")
			requireCurationRuleRequestCount(t, f, 3)
		})
	}
}

func curationRuleCandidates(t *testing.T, f *schedulerFixture, item curation.PendingItem, scores []int, titleSize int) []program.Candidate {
	t.Helper()
	candidates := make([]program.Candidate, len(scores))
	for i, score := range scores {
		id := uuid.New()
		candidates[i] = program.Candidate{ExternalID: id, Score: float64(score), Release: &sources.Release{
			ID: id, Complete: true, Title: strings.Repeat("中日한", titleSize),
			Media: []sources.Medium{{Position: 1, Tracks: []sources.Track{{Position: 1, Title: "合成首曲"}, {Position: 2, Title: "合成次曲"}}}},
		}}
	}
	raw, err := json.Marshal(candidates)
	requireCurationOK(t, err)
	mustExec(t, f.application, `UPDATE curation_pending_items SET candidates=$2 WHERE id=$1`, item.ID, raw)
	return candidates
}

func TestPostgreSQLCurationModelRulesBatchPacking(t *testing.T) {
	f := newSchedulerFixture(t)
	f.configure(t, &f.admin)
	a, long := f.pending(t, f.a), f.pending(t, f.b)
	short := curationRulePending(t, f, "补足首批")
	recording := curationRulePending(t, f, "录音合同")
	mustExec(t, f.application, `UPDATE curation_pending_items SET kind='recording_match' WHERE id=$1`, recording.ID)
	curationRuleCandidates(t, f, a, []int{1}, 900)
	curationRuleCandidates(t, f, long, []int{1}, 3150)
	curationRuleCandidates(t, f, short, []int{1}, 30)
	recID := uuid.New()
	recJSON, err := json.Marshal([]program.Candidate{{ExternalID: recID, Recording: &sources.Recording{ID: recID, Title: "合成录音"}}})
	requireCurationOK(t, err)
	mustExec(t, f.application, `UPDATE curation_pending_items SET candidates=$2 WHERE id=$1`, recording.ID, recJSON)
	const budget = 27500
	f.update(t, "PUT", curationBase+"/settings", map[string]any{"enabled": true, "model_concurrency": 1, "max_request_tokens": budget})
	first := f.next(t, false)
	requireCurationRuleItems(t, first, a.ID, short.ID)
	if first.outputLimit() != budget/8 || len(first.Tools) != 0 {
		t.Fatalf("判断请求的实际输出上限应为 %d，工具应为空，实际 %d/%v", budget/8, first.outputLimit(), first.Tools)
	}
	first.undetermined(t, a.ID, "合成证据不足")
	second := f.next(t, false)
	requireCurationRuleItems(t, second, long.ID)
	second.undetermined(t, long.ID, "合成长候选证据不足")
	third := f.next(t, false)
	requireCurationRuleItems(t, third, recording.ID)
	third.undetermined(t, recording.ID, "合成录音证据不足")
	modelWaitResolution(t, f, recording.ID, "undetermined")
}

func TestPostgreSQLCurationModelRulesTrimCandidates(t *testing.T) {
	f := newSchedulerFixture(t)
	f.configure(t, &f.admin)
	item := f.pending(t, f.a)
	candidates := curationRuleCandidates(t, f, item, []int{10, 90, 90, 70}, 900)
	const localNote = "本地合成备注与标点：中日한"
	mustExec(t, f.application, `UPDATE curation_pending_items SET evidence=evidence||jsonb_build_object('test_note',$2::text) WHERE id=$1`, item.ID, localNote)
	f.update(t, "PUT", curationBase+"/settings", map[string]any{"enabled": true, "model_concurrency": 1, "max_request_tokens": 27000})
	q := f.next(t, false)
	items, found, err := metricsPendingItems(curationRulePrompt(t, q))
	requireCurationOK(t, err)
	if !found || len(items) != 1 || items[0].ID != item.ID {
		t.Fatalf("裁剪应只发送原项：%+v", items)
	}
	var got []string
	for _, candidate := range items[0].Candidates {
		got = append(got, candidate.ID)
	}
	want := []string{candidates[1].ExternalID, candidates[2].ExternalID, candidates[3].ExternalID}
	if !slices.Equal(got, want) || !strings.Contains(string(items[0].RawEvidence), localNote) || !strings.Contains(string(items[0].RawItem), "search_only") {
		t.Errorf("应保留最高分的三个候选、同分原次序、本地备注和冲突，实际候选 %v，期望 %v，证据 %s", got, want, items[0].RawEvidence)
	}
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_pending_items WHERE id=$1 AND jsonb_array_length(candidates)=4`, item.ID)
	q.selectCandidate(t, item.ID, candidates[0].ExternalID)
	schedulerWait(t, "未发送候选的决定计空答", func() bool { return f.item(t, item.ID).EmptyCount == 1 })
	if got := f.item(t, item.ID); got.Attempts != 0 || got.Resolution != nil {
		t.Fatalf("未发送候选不属于有效决定：%+v", got)
	}
	requireCurationRuleRequestCount(t, f, 1)
}

func TestPostgreSQLCurationModelRulesLongInputReopens(t *testing.T) {
	f := newSchedulerFixture(t)
	f.configure(t, &f.admin)
	item := f.pending(t, f.a)
	curationRuleCandidates(t, f, item, []int{90}, 1800)
	f.update(t, "PUT", curationBase+"/settings", map[string]any{"enabled": true, "model_concurrency": 1, "max_request_tokens": 1500})
	schedulerWait(t, "单项仍超长，应在建会话前搁置", func() bool { return f.item(t, item.ID).Status == "shelved" })
	got := f.item(t, item.ID)
	if got.ShelvedReason == nil || *got.ShelvedReason != "内容太长" || got.Attempts != 0 || got.EmptyCount != 0 {
		t.Fatalf("单项超长状态错误：%+v", got)
	}
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM steward_conversations WHERE kind='curation'`)
	requireCurationRuleRequestCount(t, f, 0)
	f.update(t, "PUT", curationBase+"/settings", map[string]int{"max_request_tokens": 128000})
	reopened := f.next(t, false)
	requireCurationRuleItems(t, reopened, item.ID)
	reopened.undetermined(t, item.ID, "合成候选仍需人工判断")
	modelWaitResolution(t, f, item.ID, "undetermined")
}

func TestPostgreSQLCurationModelRulesDirectoryAndPriority(t *testing.T) {
	f := newSchedulerFixture(t)
	f.configure(t, &f.admin)
	f.update(t, "PUT", curationBase+"/settings", map[string]string{"model": "curation-override"})
	match := f.pending(t, f.a)
	verify := queueModelVerification(t, f, "verify_name", curation.VerificationCandidate{
		ReleaseID: f.b, Field: "title", Before: "原题", After: "原提", Structure: true,
	})
	var directory []string
	for i := range 6 {
		label := fmt.Sprintf("同目录%d", i)
		target, _ := insertGroupedRelease(t, f.application, f.rootID, groupedRelease{title: label, artist: "艺人" + label, tracks: []string{"present"}})
		p := modelPending(t, f.curationFixture, target, "local_cleanup", label)
		mustExec(t, f.application, `UPDATE curation_pending_items SET evidence=jsonb_build_object('directory_key','合成目录') WHERE id=$1`, p.ID)
		directory = append(directory, p.ID)
	}
	f.update(t, "PUT", curationBase+"/settings", map[string]any{"enabled": true, "model_concurrency": 1})
	wholeDirectory := f.nextModel(t, "curation-override", false)
	requireCurationRuleItems(t, wholeDirectory, directory...)
	wholeDirectory.complete(t)
	selection := f.nextModel(t, "curation-override", false)
	requireCurationRuleItems(t, selection, match.ID)
	selection.undetermined(t, match.ID, "合成候选证据不足")
	verification := f.nextModel(t, "curation-override", false)
	requireCurationRuleItems(t, verification, verify)
	verification.complete(t)
}

func TestPostgreSQLCurationModelRulesTargetMutualExclusion(t *testing.T) {
	clock := newProgramClock()
	f := newSchedulerFixture(t, WithCurationModel(true, model.Options{Now: clock.options().Now, PollInterval: 20 * time.Millisecond, BatchSize: 1}))
	f.clock = clock
	f.configure(t, &f.admin)
	first := f.pending(t, f.a)
	conflicting := queueModelVerification(t, f, "verify_name", curation.VerificationCandidate{
		ReleaseID: f.a, Field: "title", Before: "原题", After: "原提", Structure: true,
	})
	peer := f.pending(t, f.b)
	waiting := curationRulePending(t, f, "并发上限后的发行")
	f.update(t, "PUT", curationBase+"/settings", map[string]any{"enabled": true, "model_concurrency": 2})

	a, b := f.next(t, false), f.next(t, false)
	if strings.Contains(curationRulePrompt(t, a), peer.ID) {
		a, b = b, a
	}
	requireCurationRuleItems(t, a, first.ID)
	requireCurationRuleItems(t, b, peer.ID)
	p := f.progress(t)
	if p.Running != 2 || p.Concurrency != 2 || f.item(t, conflicting).Status != "waiting_model" || f.item(t, waiting.ID).Status != "waiting_model" {
		t.Fatalf("同发行核对应等待其在途判断，并且并发最多两个：%+v", p)
	}
	requireCurationRuleRequestCount(t, f, 2)
	a.undetermined(t, first.ID, "合成候选证据不足")
	modelWaitResolution(t, f, first.ID, "undetermined")
	third := f.next(t, false)
	requireCurationRuleItems(t, third, waiting.ID)
	third.undetermined(t, waiting.ID, "合成证据不足")
	verification := f.next(t, false)
	requireCurationRuleItems(t, verification, conflicting)
	verification.complete(t)
	b.undetermined(t, peer.ID, "合成证据不足")
	modelWaitResolution(t, f, peer.ID, "undetermined")
}
