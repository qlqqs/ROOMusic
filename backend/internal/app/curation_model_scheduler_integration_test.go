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
	Model      string
	ToolChoice json.RawMessage `json:"tool_choice"`
	Tools      []struct {
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
	status     int
	retryAfter string
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
	decision := map[string]string{"item_id": itemID, "decision": "upgrade"}
	if externalID != "" {
		decision["decision"], decision["external_id"] = "select", externalID
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
	clock    *programClock
	requests chan schedulerRequest
	endpoint string
	remote   b3RemoteRelease
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
			http.Error(w, "合成模型服务暂不可用", failure.status)
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
	select {
	case q := <-f.requests:
		want := "test-main"
		if review {
			want = "test-review"
		}
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
		if int(got.Attempts) != i || got.Status != "in_progress" || seen[got.ConversationID] {
			t.Fatalf("第 %d 次领取 %+v", i+1, got)
		}
		seen[got.ConversationID] = true
		q.complete(t)
	}
	schedulerWait(t, "第三次未处理后升级", func() bool { return f.item(t, item.ID).Route == "lookup" })
	got := f.item(t, item.ID)
	if got.Status != "waiting_route" || got.Attempts != 0 || got.ShelvedReason != nil || !strings.Contains(string(got.Evidence), "第一档连续三次未得到可执行的决定") {
		t.Fatalf("三次未处理后 %+v", got)
	}
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
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_work_states WHERE target_id=$1 AND status='conflict' AND next_attempt_at IS NULL`, f.a)

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
			ReleaseID: release, Field: "title", Before: "原题", After: "候选标题",
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

	// 只投影这张发行的巡检输入，让它实际领取后再准备冲突项。
	ctx := t.Context()
	tx, err := f.application.database.BeginTx(ctx, nil)
	requireCurationOK(t, err)
	defer tx.Rollback()
	requireCurationOK(t, curation.ProjectPatrolInputs(ctx, tx, []string{f.a}))
	requireCurationOK(t, tx.Commit())
	f.update(t, "PUT", curationBase+"/settings", map[string]any{"enabled": true, "model_concurrency": 1})
	patrol, patrolRequest := next()
	if patrol.Kind != "name_patrol" || patrol.ReleaseID != f.a {
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
		return f.item(t, selection.ID).Route == "lookup" && f.progress(t).Running == 3
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
