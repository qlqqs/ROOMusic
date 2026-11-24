package app

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
)

func TestPostgreSQLArtistWebSearchMockIdentity(t *testing.T) {
	f := newArtistWebSearchFixture(t, "identity", true, true, nil)
	f.configureMock(t)
	artist := f.addArtist(t, webSearchArtistName)
	for _, source := range []string{"fanarttv", "theaudiodb"} {
		f.update(t, http.MethodPut, curationBase+"/sources/"+source, map[string]bool{"enabled": false})
	}
	f.status(t, artist, "none", 0)
	f.enable(t)
	f.clock.advance(t, time.Minute)
	item := f.artistItem(t, artist)
	f.waitArtistResolution(t, item.ID, "model_linked")
	f.requireModelArtistLink(t, artist, "wikidata", "Q900001", false)
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM steward_reviews r JOIN steward_plans p ON p.id=r.plan_id WHERE p.conversation_id=$1 AND r.status='completed'`, f.item(t, item.ID).ConversationID)
	if got := readArtistMedia(t, f.application, &f.admin, artist); got.Image != nil || got.Bio != nil {
		t.Fatalf("认人当轮写入了资料：%+v", got)
	}
	f.requireMockArtistSearch(t)
	f.clock.advance(t, time.Minute)
	got := readArtistMedia(t, f.application, &f.admin, artist)
	if got.Image == nil || got.Image.Source != "commons" || got.Image.License == nil ||
		got.Bio == nil || got.Bio.Source != "wikipedia" || got.Bio.License == nil {
		t.Fatalf("下一轮缺少有许可的艺人资料：图片=%+v 简介=%+v", got.Image, got.Bio)
	}
	f.status(t, artist, "none", 0)
}

func TestPostgreSQLArtistWebSearchMockMediaAndUsage(t *testing.T) {
	f := newArtistWebSearchFixture(t, "media", true, true, nil)
	f.configureMock(t)
	artist := f.addArtist(t, "海岸来信")
	f.enable(t)
	f.clock.advance(t, time.Minute)
	item := f.artistItem(t, artist)
	var finished curation.PendingItem
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		finished = f.item(t, item.ID)
		if finished.Status == "resolved" && f.progress(t).State == "idle" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if finished.Status != "resolved" || finished.Resolution == nil || *finished.Resolution != "model_media" {
		view, _ := runtime.NewStore(f.application.database).GetConversation(t.Context(), f.admin.id, finished.ConversationID)
		t.Fatalf("mock 资料提交未完成，待定项 %+v，用量 %+v，模型请求 %+v，会话消息 %+v",
			finished, f.progress(t).Today, f.mockCalls(t, "openai"), view.Messages)
	}
	// 搜索、读取待定项与提交资料走同一会话，mock 每个模型请求记 12 个输入与 8 个输出 token。
	calls := len(f.mockCalls(t, "openai"))
	usage := f.progress(t).Today
	if calls < 3 || usage.InputTokens != int64(calls*12) || usage.OutputTokens != int64(calls*8) {
		t.Fatalf("mock 模型请求数=%d，当日用量 %+v", calls, usage)
	}
	limit := usage.InputTokens + usage.OutputTokens
	f.update(t, http.MethodPut, curationBase+"/settings", map[string]any{"daily_token_limit": limit})
	f.pendingArtist(t, artist, "海岸来信")
	schedulerWait(t, "达到每日模型额度", func() bool { return f.progress(t).State == "limit_reached" })
	f.status(t, artist, "queued", 0)
	f.requireMockArtistSearch(t)
	f.requireWebSearchMedia(t, artist, "沿岸音乐名录")
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM external_links WHERE object_type='artist' AND object_id=$1`, artist)
	b3RequireCount(t, f.application, 2, `SELECT count(*) FROM operations WHERE target_type='artist' AND target_id=$1
		AND origin->>'provider'='web_search' AND origin->>'provider_kind'='model' AND after_state->>'model'='mock-model'`, artist)
	conversation, err := runtime.NewStore(f.application.database).GetConversation(t.Context(), f.admin.id, f.item(t, item.ID).ConversationID)
	requireCurationOK(t, err)
	if len(conversation.Plans) != 0 {
		t.Fatalf("资料提交创建了审查方案 %+v", conversation.Plans)
	}
}

func TestPostgreSQLArtistWebSearchMockCandidates(t *testing.T) {
	for _, decision := range []string{"accept", "accept_with_other_accepting", "decline", "occupied"} {
		t.Run(decision, func(t *testing.T) {
			f := newArtistWebSearchFixture(t, "candidates", true, true, nil)
			f.configureMock(t)
			artist := f.addArtist(t, webSearchArtistName)
			f.enable(t)
			f.clock.advance(t, time.Minute)
			item := f.artistItem(t, artist)
			app := f.application
			schedulerWait(t, "两个候选均转人工", func() bool {
				return countRows(t, app, `SELECT count(*) FROM curation_model_exceptions WHERE pending_item_id=$1 AND status='open'`, item.ID) == 2
			})
			f.requireMockArtistSearch(t)
			state := f.status(t, artist, "waiting_human", 2)
			bySource := make(map[string]curation.ArtistWebSearchCandidate)
			for _, c := range state.Candidates {
				if c.Name != webSearchArtistName || c.Description == nil || *c.Description == "" || c.Note == "" || !uuid.Valid(c.ExceptionID) ||
					len(c.Citations) != 1 || c.Citations[0].URL != f.endpoint+"/searxng/artists/coast" || c.Citations[0].Title != "" {
					t.Errorf("候选字段不完整 %+v", c)
				}
				bySource[c.Source] = c
			}
			duo, solo := bySource["wikidata"], bySource["musicbrainz"]
			if duo.ExternalID != "Q900001" || solo.ExternalID != webSearchSoloMBID {
				t.Fatalf("双候选对应不符 %+v", state)
			}
			var listed struct {
				Items []curation.ModelException
				Total int
			}
			getJSON(t, app, &f.admin, curationBase+"/exceptions?status=open", &listed)
			if listed.Total != 2 || len(listed.Items) != 2 {
				t.Fatalf("例外列表 %+v", listed)
			}
			for _, ex := range listed.Items {
				if ex.Artist == nil || ex.Artist.ID != artist || ex.Artist.Name != webSearchArtistName || ex.Release != nil ||
					ex.PendingItemID != item.ID || ex.Review.Decision != "needs_human_confirmation" {
					t.Errorf("艺人例外不符 %+v", ex)
				}
			}
			b3RequireCount(t, app, 0, `SELECT count(*) FROM external_links WHERE object_type='artist' AND object_id=$1`, artist)

			switch decision {
			case "accept", "accept_with_other_accepting":
				if decision == "accept_with_other_accepting" {
					other := app.createUser(t, "admin")
					requireCurationOK(t, f.writer.ClaimException(t.Context(), solo.ExceptionID, other.id))
					readExceptionDecision(t, f.decide(t, &f.admin, duo.ExceptionID, "accept"), "accepted", f.admin.id)
					b3RequireCount(t, app, 1, `SELECT count(*) FROM curation_model_exceptions WHERE id=$1 AND status='accepting' AND decided_at IS NULL AND decided_by=$2`, solo.ExceptionID, other.id)
					f.requireModelArtistLink(t, artist, "wikidata", duo.ExternalID, true)
					return
				}
				readExceptionDecision(t, f.decide(t, &f.admin, duo.ExceptionID, "accept"), "accepted", f.admin.id)
				f.requireModelArtistLink(t, artist, "wikidata", duo.ExternalID, true)
				f.waitArtistResolution(t, item.ID, "human_accepted")
				b3RequireCount(t, app, 1, `SELECT count(*) FROM curation_model_exceptions WHERE id=$1 AND status='superseded' AND decided_at IS NOT NULL`, solo.ExceptionID)
				f.status(t, artist, "none", 0)
			case "decline":
				readExceptionDecision(t, f.decide(t, &f.admin, duo.ExceptionID, "decline"), "declined", f.admin.id)
				state = f.status(t, artist, "waiting_human", 1)
				if state.Candidates[0].ExceptionID != solo.ExceptionID {
					t.Fatalf("不采纳后候选为 %+v", state.Candidates)
				}
				b3RequireCount(t, app, 1, `SELECT count(*) FROM curation_suppressions WHERE target_type='artist' AND target_id=$1
					AND subject='external_link' AND value->>'source'='wikidata' AND value->>'external_id'=$2`, artist, duo.ExternalID)
				f.pause(t)
				readExceptionDecision(t, f.decide(t, &f.admin, solo.ExceptionID, "decline"), "declined", f.admin.id)
				f.status(t, artist, "queued", 0)
				b3RequireCount(t, app, 0, `SELECT count(*) FROM external_links WHERE object_type='artist' AND object_id=$1`, artist)
			case "occupied":
				linkArtistMedia(t, app, artist, artistMediaMBID)
				requireAPIError(t, f.decide(t, &f.admin, duo.ExceptionID, "accept"), 409, "occupied")
				b3RequireCount(t, app, 1, `SELECT count(*) FROM external_links WHERE object_type='artist' AND object_id=$1 AND source='musicbrainz' AND external_id=$2`, artist, artistMediaMBID)
				b3RequireCount(t, app, 0, `SELECT count(*) FROM operations WHERE operation_type='artist_link' AND target_id=$1`, artist)
				f.status(t, artist, "waiting_human", 2)
			}
		})
	}
}

func (f *artistWebSearchFixture) waitArtistResolution(t *testing.T, item, resolution string) {
	t.Helper()
	schedulerWait(t, "艺人项完成："+resolution, func() bool {
		got := f.item(t, item)
		return got.Status == "resolved"
	})
	got := f.item(t, item)
	if got.Resolution == nil || *got.Resolution != resolution {
		t.Fatalf("艺人项 %+v，期望 resolution=%s", got, resolution)
	}
}

func (f *artistWebSearchFixture) requireModelArtistLink(t *testing.T, artist, source, externalID string, confirmed bool) {
	t.Helper()
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM external_links l JOIN operations o ON o.id=l.operation_id
		WHERE l.object_type='artist' AND l.object_id=$1 AND l.source=$2 AND l.external_id=$3
		AND o.target_type='artist' AND o.target_id=$1 AND o.operation_type='artist_link'
		AND o.origin->>'provider_kind'='model' AND o.origin->>'provider'='mock-model' AND o.origin ? 'conversation_id'`, artist, source, externalID)
	if !confirmed {
		b3RequireCount(t, f.application, 1, `SELECT count(*) FROM operations o JOIN curation_batches b ON b.id=o.batch_id
			WHERE o.target_id=$1 AND o.operation_type='artist_link' AND b.tier='model'`, artist)
	}
}

func (f *artistWebSearchFixture) requireMockArtistSearch(t *testing.T) {
	t.Helper()
	calls := f.mockCalls(t, "searxng")
	if !slices.ContainsFunc(calls, func(call artistMockCall) bool {
		return call.Path == "/searxng/search" && call.Method == http.MethodGet && call.Status == http.StatusOK && len(call.Query["q"]) > 0
	}) {
		t.Fatalf("后台无成功的 mock 搜索请求 %+v", calls)
	}
}

func (f *artistWebSearchFixture) requireWebSearchMedia(t *testing.T, artist, title string) {
	t.Helper()
	detail := readArtistMedia(t, f.application, &f.admin, artist)
	if detail.Image == nil || detail.Image.Source != "web_search" || detail.Bio == nil || detail.Bio.Source != "web_search" {
		t.Fatalf("模型写入的资料不符：图片=%+v 简介=%+v", detail.Image, detail.Bio)
	}
	for _, citations := range [][]catalog.Citation{detail.Image.Citations, detail.Bio.Citations} {
		if len(citations) != 1 || citations[0].URL != f.endpoint+"/searxng/artists/coast" || citations[0].Title != title {
			t.Fatalf("艺人资料引用 %+v", citations)
		}
	}
}

func decodeArtistWorkEvidence(t *testing.T, item curation.PendingItem) (evidence struct {
	Names  []string
	Albums []struct {
		Name string
		Year int
	}
	Tracks []string
	Gaps   struct{ Image, Bio bool }
}) {
	t.Helper()
	requireCurationOK(t, json.Unmarshal(item.Evidence, &evidence))
	return evidence
}
