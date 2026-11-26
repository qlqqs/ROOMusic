package app

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
)

func TestPostgreSQLArtistWebSearchMockIdentity(t *testing.T) {
	f := newArtistWebSearchFixture(t, "identity", true, true, nil)
	f.configureMock(t)
	release, _ := insertGroupedRelease(t, f.application, f.rootID, groupedRelease{title: "潮线以北", artist: webSearchArtistName, year: 2021, tracks: []string{"present", "present"}})
	mustExec(t, f.application, `UPDATE tracks t SET duration_seconds=CASE t.position WHEN 1 THEN 180 ELSE 192 END, title=CASE t.position WHEN 1 THEN '潮汐信箱' ELSE '细雨码头' END FROM media m WHERE m.id=t.medium_id AND m.release_id=$1`, release)
	publishSearch(t, f.application)
	artist := f.artistID(t, webSearchArtistName)
	for _, source := range []string{"fanarttv", "theaudiodb"} {
		f.update(t, http.MethodPut, curationBase+"/sources/"+source, map[string]bool{"enabled": false})
	}
	f.status(t, artist, "none", 0)
	item := f.startArtist(t, artist)
	f.waitArtistResolution(t, item.ID, "model_linked")
	f.requireModelArtistLink(t, artist, "musicbrainz", artistMediaMBID, false)
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM external_links WHERE object_type='release' AND object_id=$1 AND source='musicbrainz'`, release)
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM steward_reviews r JOIN steward_plans p ON p.id=r.plan_id WHERE p.conversation_id=$1`, f.item(t, item.ID).ConversationID)
	if got := readArtistMedia(t, f.application, &f.admin, artist); got.Image != nil || got.Bio != nil {
		t.Fatalf("认人当轮写入了资料：%+v", got)
	}
	f.requireMockArtistSearch(t)
	f.enable(t)
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
	item := f.startArtist(t, artist)
	f.waitArtistResolution(t, item.ID, "no_result")
	f.requireMockArtistSearch(t)
	calls := len(f.mockCalls(t, "openai"))
	usage := f.progress(t).Today
	if calls == 0 || usage.InputTokens != int64(calls*12) || usage.OutputTokens != int64(calls*8) {
		t.Fatalf("共享日账 %+v，实际请求 %d", usage, calls)
	}
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM operations WHERE target_type='artist' AND target_id=$1`, artist)
}

func TestPostgreSQLArtistWebSearchMockCandidates(t *testing.T) {
	for _, decision := range []string{"accept", "accept_with_other_accepting", "decline", "occupied"} {
		t.Run(decision, func(t *testing.T) {
			f := newArtistWebSearchFixture(t, "candidates", true, true, nil)
			f.configureMock(t)
			artist := f.addArtist(t, webSearchArtistName)
			item := f.startArtist(t, artist)
			app := f.application
			schedulerWait(t, "两个候选均保留为建议", func() bool {
				return countRows(t, app, `SELECT count(*) FROM curation_model_exceptions WHERE pending_item_id=$1 AND status='open'`, item.ID) == 2
			})
			f.requireMockArtistSearch(t)
			f.waitArtistResolution(t, item.ID, "suggestion_recorded")
			if p := f.progress(t); p.Items.WaitingHuman != 0 {
				t.Fatalf("进度仍有人工待办 %+v", p.Items)
			}
			var listed struct {
				Items []curation.ModelException
				Total int
			}
			getJSON(t, app, &f.admin, curationBase+"/exceptions?status=open", &listed)
			if listed.Total != 2 || len(listed.Items) != 2 {
				t.Fatalf("例外列表 %+v", listed)
			}
			bySource := make(map[string]curation.ModelException)
			for _, ex := range listed.Items {
				if ex.Artist == nil || ex.Artist.ID != artist || ex.Artist.Name != webSearchArtistName || ex.Release != nil ||
					ex.PendingItemID != item.ID || ex.Review.ID != "" || !ex.CanAccept || ex.ReasonCode != "no_evidence" {
					t.Fatalf("艺人建议不符 %+v", ex)
				}
				var in struct {
					Source     string
					ExternalID string `json:"external_id"`
					Basis      struct{ Citations []string }
				}
				requireCurationOK(t, json.Unmarshal(ex.Input, &in))
				if len(in.Basis.Citations) != 1 || in.Basis.Citations[0] != f.endpoint+"/searxng/artists/coast" ||
					(in.Source != "musicbrainz" || in.ExternalID != artistMediaMBID && in.ExternalID != webSearchSoloMBID) {
					t.Fatalf("候选建议字段 %s", ex.Input)
				}
				bySource[in.ExternalID] = ex
			}
			duo, solo := bySource[artistMediaMBID], bySource[webSearchSoloMBID]
			if duo.ID == "" || solo.ID == "" {
				t.Fatalf("双候选对应不符 %+v", bySource)
			}
			b3RequireCount(t, app, 0, `SELECT count(*) FROM external_links WHERE object_type='artist' AND object_id=$1`, artist)

			switch decision {
			case "accept", "accept_with_other_accepting":
				if decision == "accept_with_other_accepting" {
					other := app.createUser(t, "admin")
					requireCurationOK(t, f.writer.ClaimException(t.Context(), solo.ID, other.id))
					requireAPIError(t, f.decide(t, &f.admin, duo.ID, "accept"), 409, "exception_decided")
					b3RequireCount(t, app, 1, `SELECT count(*) FROM curation_model_exceptions WHERE id=$1 AND status='accepting' AND decided_at IS NULL AND decided_by=$2`, solo.ID, other.id)
					b3RequireCount(t, app, 0, `SELECT count(*) FROM external_links WHERE object_type='artist' AND object_id=$1`, artist)
					requireCurationOK(t, f.writer.ReopenException(t.Context(), solo.ID, other.id))
				}
				readExceptionDecision(t, f.decide(t, &f.admin, duo.ID, "accept"), "accepted", f.admin.id)
				f.requireModelArtistLink(t, artist, "musicbrainz", artistMediaMBID, true)
				f.waitArtistResolution(t, item.ID, "suggestion_recorded")
				b3RequireCount(t, app, 1, `SELECT count(*) FROM curation_model_exceptions WHERE id=$1 AND status='superseded' AND decided_at IS NOT NULL`, solo.ID)
				f.status(t, artist, "none", 0)
			case "decline":
				readExceptionDecision(t, f.decide(t, &f.admin, duo.ID, "decline"), "declined", f.admin.id)
				b3RequireCount(t, app, 1, `SELECT count(*) FROM curation_model_exceptions WHERE pending_item_id=$1 AND id=$2 AND status='open'`, item.ID, solo.ID)
				b3RequireCount(t, app, 1, `SELECT count(*) FROM curation_suppressions WHERE target_type='artist' AND target_id=$1
					AND subject='external_link' AND value->>'source'='musicbrainz' AND value->>'external_id'=$2`, artist, artistMediaMBID)
				f.pause(t)
				readExceptionDecision(t, f.decide(t, &f.admin, solo.ID, "decline"), "declined", f.admin.id)
				f.waitArtistResolution(t, item.ID, "no_result")
				b3RequireCount(t, app, 0, `SELECT count(*) FROM external_links WHERE object_type='artist' AND object_id=$1`, artist)
			case "occupied":
				linkArtistMedia(t, app, artist, artistMediaMBID)
				requireAPIError(t, f.decide(t, &f.admin, duo.ID, "accept"), 409, "occupied")
				b3RequireCount(t, app, 1, `SELECT count(*) FROM external_links WHERE object_type='artist' AND object_id=$1 AND source='musicbrainz' AND external_id=$2`, artist, artistMediaMBID)
				b3RequireCount(t, app, 0, `SELECT count(*) FROM operations WHERE operation_type='artist_link' AND target_id=$1`, artist)
				f.waitArtistResolution(t, item.ID, "suggestion_recorded")
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
