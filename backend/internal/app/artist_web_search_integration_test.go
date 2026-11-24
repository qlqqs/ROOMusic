package app

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/curation/model"
	"github.com/qlqq/roomusic/backend/internal/curation/program"
	"github.com/qlqq/roomusic/backend/internal/platform/config"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
	"github.com/qlqq/roomusic/backend/internal/upstreammock"
)

const webSearchArtistName = "纸屿"
const webSearchOtherMBID = "10000000-0000-4000-8000-000000000098"
const webSearchSoloMBID = "10000000-0000-4000-8000-000000000003"

type artistWebSearchFixture struct {
	*schedulerFixture
}

func newArtistWebSearchFixture(t *testing.T, scenario string, runProgram, runModel bool, wrap func(http.Handler) http.Handler) *artistWebSearchFixture {
	t.Helper()
	opts := upstreammock.Options{}
	if scenario != "" {
		opts.ScriptFile = filepath.Join("..", "upstreammock", "testdata", "artist-"+scenario+".json")
	}
	mock, err := upstreammock.New(opts)
	requireCurationOK(t, err)
	var handler http.Handler = mock
	if wrap != nil {
		handler = wrap(handler)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	clock := newProgramClock()
	app := newConfiguredIntegrationTestApplication(t, slog.New(slog.NewTextHandler(io.Discard, nil)), config.Server{UpstreamMockURL: server.URL},
		WithCurationProgram(runProgram, clock.options()),
		WithCurationModel(runModel, model.Options{Now: clock.options().Now, PollInterval: 20 * time.Millisecond}),
	)
	if runProgram {
		clock.wait(t)
	}
	groups := newGroupEnvironmentForApp(t, app)
	mustExec(t, app, `UPDATE tracks SET source_status='missing'`)
	f := &artistWebSearchFixture{
		schedulerFixture: &schedulerFixture{
			curationFixture: &curationFixture{&operationsEnvironment{groups}, curation.NewWriter(app.database, app.dataDirectory, nil)},
			clock:           clock, endpoint: server.URL,
		},
	}
	return f
}

// 模型请求仍经正常会话与 SSE 协议，只把测试需要控制的回复留给用例。
func newControlledArtistWebSearch(t *testing.T, extra func(http.ResponseWriter, *http.Request) bool) *artistWebSearchFixture {
	t.Helper()
	requests := make(chan schedulerRequest)
	f := newArtistWebSearchFixture(t, "", false, true, func(mock http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if extra != nil && extra(w, r) {
				return
			}
			if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
				mock.ServeHTTP(w, r)
				return
			}
			q := schedulerRequest{reply: make(chan string, 1)}
			if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
				http.Error(w, "模型请求无效", http.StatusBadRequest)
				return
			}
			select {
			case requests <- q:
			case <-r.Context().Done():
				return
			case <-t.Context().Done():
				return
			}
			select {
			case wire := <-q.reply:
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, wire)
			case <-r.Context().Done():
			case <-t.Context().Done():
			}
		})
	})
	f.requests = requests
	f.stewardSettings(t, &f.admin, map[string]any{
		"endpoint": f.endpoint + "/openai/chat/v1", "protocol": "openai", "model": "test-main", "review_model": "test-review",
		"search_source": "searxng", "searxng_url": f.endpoint + "/searxng",
	})
	return f
}

func (f *artistWebSearchFixture) stewardSettings(t *testing.T, actor *integrationTestUser, patch map[string]any) {
	t.Helper()
	r := f.application.request(t, http.MethodPut, stewardBase+"/settings", patch, actor, nil)
	if r.Code != http.StatusOK {
		t.Fatalf("管家设置 HTTP %d %s", r.Code, r.Body)
	}
}

func (f *artistWebSearchFixture) configureMock(t *testing.T) {
	t.Helper()
	f.stewardSettings(t, &f.admin, map[string]any{
		"endpoint": f.endpoint + "/openai/v1", "protocol": "responses", "model": "mock-model", "review_model": "mock-reviewer",
		"search_source": "searxng", "searxng_url": f.endpoint + "/searxng",
	})
}

func (f *artistWebSearchFixture) addRelease(t *testing.T, name string) string {
	t.Helper()
	release, _ := insertGroupedRelease(t, f.application, f.rootID, groupedRelease{
		title: "沿岸合成专辑", artist: name, year: 2024, tracks: []string{"present"},
	})
	track, _ := externalTrack(t, f.application, release)
	mustExec(t, f.application, `UPDATE tracks SET artist=$2,title='潮声合成曲目' WHERE id=$1`, track, name)
	return track
}

func (f *artistWebSearchFixture) artistID(t *testing.T, name string) string {
	t.Helper()
	return programString(t, f.application, `SELECT id FROM artists WHERE initial_name=$1`, name)
}

func (f *artistWebSearchFixture) addArtist(t *testing.T, name string) string {
	t.Helper()
	f.addRelease(t, name)
	publishSearch(t, f.application)
	return f.artistID(t, name)
}

func (f *artistWebSearchFixture) pendingArtist(t *testing.T, artist, name string) curation.PendingItem {
	t.Helper()
	evidence, err := json.Marshal(map[string]any{
		"names": []string{name}, "albums": []any{}, "tracks": []string{}, "gaps": map[string]bool{"image": true, "bio": true},
	})
	requireCurationOK(t, err)
	requireCurationOK(t, curation.UpsertPendingItem(t.Context(), f.application.database, curation.PendingItem{
		ArtistID: artist, TargetType: "artist", Kind: "artist_identity", RuleVersion: program.StoredRuleVersion, InputDigest: "合成艺人证据", Evidence: evidence,
	}))
	return f.artistItem(t, artist)
}

func (f *artistWebSearchFixture) artistItem(t *testing.T, artist string) curation.PendingItem {
	t.Helper()
	id := programString(t, f.application, `SELECT id FROM curation_pending_items WHERE artist_id=$1 ORDER BY created_at DESC,id DESC LIMIT 1`, artist)
	return f.item(t, id)
}

func (f *artistWebSearchFixture) status(t *testing.T, artist, state string, candidates int) curation.ArtistWebSearchStatus {
	t.Helper()
	var result curation.ArtistWebSearchStatus
	getJSON(t, f.application, &f.admin, "/api/v1/artists/"+artist+"/web-search", &result)
	if result.State != state || result.Candidates == nil || len(result.Candidates) != candidates {
		t.Fatalf("艺人网络搜索状态 %+v，期望 %s 与 %d 个候选", result, state, candidates)
	}
	return result
}

type artistMockCall struct {
	Path   string
	Method string
	Query  map[string][]string
	Status int
}

func (f *artistWebSearchFixture) mockCalls(t *testing.T, service string) []artistMockCall {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, f.endpoint+"/_mock/requests?service="+service, nil)
	requireCurationOK(t, err)
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	requireCurationOK(t, err)
	defer resp.Body.Close()
	var calls []artistMockCall
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("读取 mock 请求 HTTP %d", resp.StatusCode)
	}
	requireCurationOK(t, json.NewDecoder(resp.Body).Decode(&calls))
	return calls
}

func TestPostgreSQLArtistWebSearchSourceRequirements(t *testing.T) {
	f := newArtistWebSearchFixture(t, "", false, false, nil)
	other := f.application.createUser(t, "admin")
	read := func(actor *integrationTestUser, enabled, active, modelReady, searchReady bool, reason string) {
		t.Helper()
		var body struct{ Items []curation.SourceSettings }
		getJSON(t, f.application, actor, curationBase+"/sources", &body)
		var found bool
		for _, item := range body.Items {
			if item.ID != "web_search" {
				if item.Requirements != nil {
					t.Errorf("来源 %s 有意外的依赖状态 %+v", item.ID, item.Requirements)
				}
				continue
			}
			found = true
			want := []curation.SourceRequirement{{Kind: "model", Ready: modelReady}, {Kind: "search", Ready: searchReady}}
			if !slices.Equal(item.Requirements, want) || item.Enabled != enabled || item.Active != active ||
				!slices.Equal(item.Capabilities, []sources.Capability{sources.ArtistImageLookup, sources.ArtistBioLookup}) ||
				(reason == "" && item.InactiveReason != nil) || (reason != "" && (item.InactiveReason == nil || *item.InactiveReason != reason)) {
				t.Fatalf("网络搜索来源 = %+v，期望 enabled=%t active=%t requirements=%+v reason=%q", item, enabled, active, want, reason)
			}
		}
		if !found {
			t.Fatal("来源列表没有 web_search")
		}
	}
	read(&f.admin, true, false, false, false, "curation_disabled")
	f.enable(t)
	read(&f.admin, true, false, false, false, "search_unconfigured")
	f.configureMock(t)
	read(&other, true, true, true, true, "")
	f.stewardSettings(t, &f.admin, map[string]any{"search_source": "native", "overrides": map[string]string{"native_search": "supported"}})
	f.stewardSettings(t, &f.admin, map[string]any{"overrides": map[string]string{"native_search": "unsupported"}})
	read(&f.admin, true, false, true, false, "search_unconfigured")
	f.configureMock(t)
	f.update(t, http.MethodPut, curationBase+"/sources/web_search", map[string]bool{"enabled": false})
	read(&other, false, false, true, true, "source_disabled")
	f.update(t, http.MethodPut, curationBase+"/sources/web_search", map[string]bool{"enabled": true})

	_, err := f.writer.SetSettings(t.Context(), other.id, true)
	requireCurationOK(t, err)
	read(&f.admin, true, false, false, false, "search_unconfigured")
}

func TestPostgreSQLArtistWebSearchBackgroundSearchGate(t *testing.T) {
	for _, tc := range []struct {
		name, search, nativeSupport            string
		enabled, searchTool, fetchTool, native bool
	}{
		{"searxng_ready", "searxng", "", true, true, true, false},
		{"searxng_disabled", "searxng", "", false, false, false, false},
		{"search_missing", "none", "", true, false, false, false},
		{"native_unsupported", "native", "unsupported", true, false, false, false},
		{"native_disabled", "native", "supported", false, false, false, false},
		{"native_ready", "native", "supported", true, false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			type wireTool struct{ Type, Name string }
			requests := make(chan []wireTool, 1)
			f := newArtistWebSearchFixture(t, "", false, true, func(mock http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/openai/v1/responses" {
						raw, err := io.ReadAll(r.Body)
						var input struct{ Tools []wireTool }
						if err != nil || json.Unmarshal(raw, &input) != nil {
							http.Error(w, "模型请求无效", 400)
							return
						}
						r.Body = io.NopCloser(bytes.NewReader(raw))
						select {
						case requests <- input.Tools:
						default:
						}
					}
					mock.ServeHTTP(w, r)
				})
			})
			f.configureMock(t)
			patch := map[string]any{"search_source": tc.search}
			if tc.search == "native" {
				patch["overrides"] = map[string]string{"native_search": "supported"}
			}
			f.stewardSettings(t, &f.admin, patch)
			if tc.nativeSupport == "unsupported" {
				f.stewardSettings(t, &f.admin, map[string]any{"overrides": map[string]string{"native_search": "unsupported"}})
			}
			f.update(t, http.MethodPut, curationBase+"/sources/web_search", map[string]bool{"enabled": tc.enabled})
			release, _ := insertGroupedRelease(t, f.application, f.rootID, groupedRelease{title: "合成待定发行", artist: "合成组合", tracks: []string{"present"}})
			modelPending(t, f.curationFixture, release, "release_match", "合成发行证据")
			f.enable(t)

			select {
			case tools := <-requests:
				has := func(name string) bool {
					return slices.ContainsFunc(tools, func(tool wireTool) bool { return tool.Type == "function" && tool.Name == name })
				}
				native := slices.ContainsFunc(tools, func(tool wireTool) bool {
					return tool.Type == "web_search" || strings.HasPrefix(tool.Type, "web_search_")
				})
				if !has("read_pending_items") || has("web_search") != tc.searchTool || has("fetch_page") != tc.fetchTool || native != tc.native {
					t.Fatalf("后台模型请求工具 %+v，期望 web_search=%t，fetch_page=%t，原生搜索=%t", tools, tc.searchTool, tc.fetchTool, tc.native)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("未收到后台模型请求")
			}
		})
	}
}

func webSearchIdentityProposal(item, source, id, page string) map[string]any {
	return map[string]any{"item_id": item, "source": source, "external_id": id,
		"basis": map[string]any{"note": "合成名录核对署名", "citations": []string{page}}}
}

func TestPostgreSQLArtistWebSearchIdentityRejectionsAndStates(t *testing.T) {
	f := newControlledArtistWebSearch(t, func(w http.ResponseWriter, r *http.Request) bool {
		id := strings.TrimPrefix(r.URL.Path, "/musicbrainz/ws/2/artist/")
		kind := map[string]string{noNameMBID: "Character", webSearchOtherMBID: "Other"}[id]
		if kind == "" {
			return false
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": id, "name": webSearchArtistName, "type": kind})
		return true
	})
	artist := f.addArtist(t, webSearchArtistName)
	item := f.pendingArtist(t, artist, webSearchArtistName)
	f.status(t, artist, "queued", 0)
	requireAPIError(t, f.application.request(t, http.MethodGet, "/api/v1/artists/"+artist+"/web-search", nil, &f.ordinary, nil), 403, "forbidden")
	modelSuppressLink(t, &programFixture{curationFixture: f.curationFixture}, "artist", artist, artistMediaMBID)
	f.enable(t)

	next := f.next(t, false)
	f.status(t, artist, "running", 0)
	conversationID := f.item(t, item.ID).ConversationID
	page := f.endpoint + "/searxng/artists/coast"
	input := webSearchIdentityProposal(item.ID, "musicbrainz", webSearchSoloMBID, page)
	citations := []string{page, page + "?2", page + "?3", page + "?4", page + "?5", page + "?6"}
	input["basis"].(map[string]any)["citations"] = citations
	next.tool(t, "resolve_artist_identity", input)
	next = f.next(t, false)
	if got := next.lastToolResult(t); !strings.Contains(got, "invalid_input") && !strings.Contains(got, "validation_failed") {
		t.Errorf("身份候选超过五条引用却未返回输入校验错误：%s", got)
	}
	for _, tc := range []struct{ id, code string }{
		{noNameMBID, "not_an_artist"},
		{webSearchOtherMBID, "not_an_artist"},
		{meridianMBID, "identity_mismatch"},
		{artistMediaMBID, "suppressed"},
	} {
		next.tool(t, "resolve_artist_identity", webSearchIdentityProposal(item.ID, "musicbrainz", tc.id, f.endpoint+"/searxng/artists/coast"))
		next = f.next(t, false)
		if got := next.lastToolResult(t); !strings.Contains(got, tc.code) {
			t.Errorf("提交 %s 返回 %s，期望 %q", tc.id, got, tc.code)
		}
		b3RequireCount(t, f.application, 0, `SELECT count(*) FROM external_links WHERE object_type='artist' AND object_id=$1`, artist)
		b3RequireCount(t, f.application, 0, `SELECT count(*) FROM operations WHERE target_type='artist' AND target_id=$1`, artist)
		b3RequireCount(t, f.application, 0, `SELECT count(*) FROM curation_model_exceptions WHERE pending_item_id=$1`, item.ID)
	}
	next.tool(t, "shelve_pending_item", map[string]string{"item_id": item.ID, "reason": "三个合成候选均无法采纳"})
	f.next(t, false).complete(t)
	f.status(t, artist, "shelved", 0)
	conversation, err := runtime.NewStore(f.application.database).GetConversation(t.Context(), f.admin.id, conversationID)
	requireCurationOK(t, err)
	if len(conversation.Plans) != 0 {
		t.Fatalf("无效候选创建了审查方案 %+v", conversation.Plans)
	}
}

func TestPostgreSQLArtistWebSearchMusicBrainzWithoutType(t *testing.T) {
	f := newControlledArtistWebSearch(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/musicbrainz/ws/2/artist/"+webSearchSoloMBID {
			return false
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": webSearchSoloMBID, "name": webSearchArtistName})
		return true
	})
	artist := f.addArtist(t, webSearchArtistName)
	item := f.pendingArtist(t, artist, webSearchArtistName)
	f.enable(t)
	f.next(t, false).tool(t, "resolve_artist_identity", webSearchIdentityProposal(item.ID, "musicbrainz", webSearchSoloMBID, f.endpoint+"/searxng/artists/coast"))
	f.next(t, true).review(t, "approved")
	f.next(t, false).complete(t)
	f.waitArtistResolution(t, item.ID, "model_linked")
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM external_links l JOIN operations o ON o.id=l.operation_id
		WHERE l.object_type='artist' AND l.object_id=$1 AND l.source='musicbrainz' AND l.external_id=$2
		AND o.operation_type='artist_link' AND o.origin->>'provider_kind'='model'`, artist, webSearchSoloMBID)
}
