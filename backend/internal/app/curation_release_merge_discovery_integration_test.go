package app

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/curation/program"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func seedReleaseMergeLink(t *testing.T, f *curationFixture, release, external, recording string, discs, tracks int) string {
	t.Helper()
	track, _ := externalTrack(t, f.application, release)
	req := curationCLinkRequest(release, "",
		curationCLink("release", release, external), curationCLink("track", track, recording))
	req.External.DiscCount, req.External.TrackCount = discs, tracks
	return curationCApplyLinks(t, f, req).OperationID
}

func discoverReleaseMerge(t *testing.T, f *curationFixture, external string) {
	t.Helper()
	requireCurationOK(t, f.writer.DiscoverReleaseMerges(t.Context(), external, program.StoredRuleVersion))
}

func requireReleaseMergeItem(t *testing.T, f *curationFixture, target, source, external string, queued bool) curation.PendingItem {
	t.Helper()
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_pending_items WHERE kind='release_merge' AND status<>'resolved'`)
	id := programString(t, f.application, `SELECT id FROM curation_pending_items WHERE kind='release_merge' AND status<>'resolved'`)
	item, err := f.writer.PendingItem(t.Context(), id)
	requireCurationOK(t, err)
	evidence, err := catalog.LoadCurationEvidence(t.Context(), f.application.database, []string{target, source}, f.writer.ArtworkExists)
	requireCurationOK(t, err)
	owner := func(release string) string {
		t.Helper()
		return programString(t, f.application, `SELECT operation_id FROM external_links WHERE source='musicbrainz' AND object_type='release' AND object_id=$1`, release)
	}
	wantDigest := operations.Fingerprint([]string{curation.ReleaseDigest(evidence[target]), curation.ReleaseDigest(evidence[source]), owner(target), owner(source)})
	wantCandidate := curation.MergeCandidate{ReleaseID: source, Title: evidence[source].Title, DiscCount: len(evidence[source].Media), TrackCount: len(evidence[source].Tracks)}
	var match struct {
		ExternalID string `json:"external_id"`
		Queued     bool   `json:"queued"`
	}
	requireCurationOK(t, json.Unmarshal(item.Evidence, &match))
	candidate, err := curation.ReleaseMergeCandidate(item)
	requireCurationOK(t, err)
	if item.Status != "waiting_model" || item.TargetID != target || candidate != wantCandidate ||
		item.InputDigest != wantDigest || string(item.Conflicts) != `["same_external_release"]` ||
		match.ExternalID != external || match.Queued != queued {
		t.Fatalf("待定项 %+v，候选 %+v，外部发行 %+v，期望目标 %s、候选 %+v、摘要 %s、排队 %t", item, candidate, match, target, wantCandidate, wantDigest, queued)
	}
	return item
}

func requireReleaseMergeResolution(t *testing.T, f *curationFixture, item, resolution string) {
	t.Helper()
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_pending_items WHERE id=$1 AND status='resolved' AND resolution=$2 AND resolved_at IS NOT NULL`, item, resolution)
}

func manualReleaseMerge(t *testing.T, f *curationFixture, target, source string) {
	t.Helper()
	path := "/api/v1/releases/" + target
	var preview struct{ Token string }
	getJSON(t, f.application, &f.admin, path+"/merge-preview?source="+source, &preview)
	r := f.application.request(t, "POST", path+"/merge", map[string]string{"source_release_id": source, "token": preview.Token}, &f.admin, idempotencyHeader(uuid.New()))
	if r.Code != 200 {
		t.Fatalf("人工合并 HTTP %d %s", r.Code, r.Body)
	}
}

func TestPostgreSQLCurationReleaseMergeProposed(t *testing.T) {
	t.Run("排序", func(t *testing.T) {
		for _, tc := range []struct {
			name                     string
			discA, discB, posA, posB int
			timeA, timeB             string
			targetA                  bool
		}{
			{"先比碟号", 1, 2, 9, 1, "2026-01-02", "2026-01-01", true},
			{"再比曲序", 1, 1, 1, 2, "2026-01-02", "2026-01-01", true},
			{"再比创建时间", 1, 1, 1, 1, "2026-01-01", "2026-01-02", true},
			{"最后比 UUID", 1, 1, 1, 1, "2026-01-01", "2026-01-01", false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				f := newCurationFixture(t)
				f.enable(t)
				a := max(f.a, f.b)
				b := min(f.a, f.b)
				for _, r := range []struct {
					id, created string
					disc, pos   int
				}{{a, tc.timeA, tc.discA, tc.posA}, {b, tc.timeB, tc.discB, tc.posB}} {
					mustExec(t, f.application, `UPDATE releases SET created_at=$2::timestamptz WHERE id=$1`, r.id, r.created)
					mustExec(t, f.application, `UPDATE tracks SET disc_number=$2,position=$3 WHERE medium_id IN (SELECT id FROM media WHERE release_id=$1)`, r.id, r.disc, r.pos)
				}
				external := uuid.New()
				for _, release := range []string{a, b} {
					seedReleaseMergeLink(t, f, release, external, uuid.New(), 2, 2)
				}
				if !tc.targetA {
					a, b = b, a
				}
				discoverReleaseMerge(t, f, external)
				first := requireReleaseMergeItem(t, f, a, b, external, false)
				discoverReleaseMerge(t, f, external)
				again := requireReleaseMergeItem(t, f, a, b, external, false)
				if again.ID != first.ID {
					t.Fatalf("重复发现换了待定项 %s → %s", first.ID, again.ID)
				}
				b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_pending_items WHERE kind='release_merge'`)
			})
		}
	})

	for _, method := range []string{"program", "model"} {
		t.Run(method, func(t *testing.T) {
			f := newProgramFixture(t)
			f.enable(t)
			mustExec(t, f.application, `UPDATE tracks SET source_status='present',disc_number=2 WHERE medium_id IN (SELECT id FROM media WHERE release_id=$1)`, f.b)
			remote := programAlbum("Album A", "Artist A")
			f.source.releases[remote.ID] = remote
			seedReleaseMergeLink(t, f.curationFixture, f.b, remote.ID, uuid.New(), 2, 2)
			if method == "program" {
				track, _ := externalTrack(t, f.application, f.a)
				programTag(t, f.application, track, "mb_release", remote.ID)
				f.run(t)
				f.assertRelease(t, f.a, "matched", "tag_id")
			} else {
				conversation := modelConversation(t, f.curationFixture)
				item := modelClaim(t, f.curationFixture, conversation, modelPending(t, f.curationFixture, f.a, "release_match", modelDigest(t, f)))
				matcher := program.NewModelMatcher(f.application.database, f.writer, f.gate)
				plan, err := matcher.Plan(t.Context(), item, remote.ID)
				requireCurationOK(t, err)
				_, err = matcher.Apply(t.Context(), plan, program.ApplyOptions{Key: uuid.New(), Batch: f.batch(t, "model"), PendingItemID: item.ID, ApprovedReviewID: uuid.New(),
					Source: curation.Source{Kind: "model", Provider: "test-model", Basis: "合成匹配依据"}})
				requireCurationOK(t, err)
				f.assertRelease(t, f.a, "matched", "model")
			}
			f.assertCount(t, 1, `SELECT count(*) FROM curation_work_states WHERE subject='release' AND target_id=$1 AND result->>'release_merge'='done'`, f.a)
			requireReleaseMergeItem(t, f.curationFixture, f.a, f.b, remote.ID, false)
		})
	}

	t.Run("存量回填", func(t *testing.T) {
		f := newProgramFixture(t)
		f.enable(t)
		f.sourceEnabled(t, "lrclib", false)
		mustExec(t, f.application, `UPDATE tracks SET source_status='present',disc_number=2 WHERE medium_id IN (SELECT id FROM media WHERE release_id=$1)`, f.b)
		external := uuid.New()
		for _, release := range []string{f.a, f.b} {
			seedReleaseMergeLink(t, f.curationFixture, release, external, uuid.New(), 2, 2)
		}
		evidence, err := catalog.LoadCurationEvidence(t.Context(), f.application.database, []string{f.a, f.b}, f.writer.ArtworkExists)
		requireCurationOK(t, err)
		for _, release := range []string{f.a, f.b} {
			result := json.RawMessage(`{"mode":"album","method":"barcode","track_fields":"done","artist_links":"done"}`)
			requireCurationOK(t, curation.SaveWorkState(t.Context(), f.application.database, curation.WorkState{
				Subject: "release", TargetID: release, Status: "matched", RuleVersion: program.StoredRuleVersion,
				InputDigest: curation.ReleaseDigest(evidence[release]), Generation: 7, Attempts: 3, Result: result,
			}))
		}
		f.run(t)
		first := requireReleaseMergeItem(t, f.curationFixture, f.a, f.b, external, false)
		after := f.assertRelease(t, f.a, "matched", "barcode")
		var result map[string]string
		requireCurationOK(t, json.Unmarshal(after.Result, &result))
		want := map[string]string{"mode": "album", "method": "barcode", "track_fields": "done", "artist_links": "done", "release_merge": "done"}
		if !reflect.DeepEqual(result, want) || after.InputDigest != curation.ReleaseDigest(evidence[f.a]) || after.Generation != 7 || after.Attempts != 3 {
			t.Fatalf("回填结果 %v，工作状态 %+v", result, after)
		}
		f.clock.advance(t, 7*time.Hour)
		again := requireReleaseMergeItem(t, f.curationFixture, f.a, f.b, external, false)
		if !reflect.DeepEqual(after, f.stateFor(t, "release", f.a)) || first.ID != again.ID || f.source.calls["ReleaseByID"] != 0 || f.source.calls["SearchReleases"] != 0 {
			t.Fatalf("重复回填或外部请求，待定项 %s → %s，调用 %v", first.ID, again.ID, f.source.calls)
		}
	})

	t.Run("三张发行在同一目标排队", func(t *testing.T) {
		f := newCurationFixture(t)
		f.enable(t)
		third, _ := insertGroupedRelease(t, f.application, f.rootID, groupedRelease{title: "第三碟", artist: "Artist A", tracks: []string{"present"}})
		external := uuid.New()
		for i, release := range []string{f.a, f.b, third} {
			mustExec(t, f.application, `UPDATE tracks SET disc_number=$2 WHERE medium_id IN (SELECT id FROM media WHERE release_id=$1)`, release, i+1)
			seedReleaseMergeLink(t, f, release, external, uuid.New(), 3, 3)
			discoverReleaseMerge(t, f, external)
			if i == 1 {
				requireReleaseMergeItem(t, f, f.a, f.b, external, false)
			}
		}
		first := requireReleaseMergeItem(t, f, f.a, f.b, external, true)
		manualReleaseMerge(t, f, f.a, f.b)
		requireReleaseMergeResolution(t, f, first.ID, "merged")
		requireCurationOK(t, f.writer.AdvanceReleaseMergeQueues(t.Context(), program.StoredRuleVersion))
		second := requireReleaseMergeItem(t, f, f.a, third, external, false)
		manualReleaseMerge(t, f, f.a, third)
		requireReleaseMergeResolution(t, f, second.ID, "merged")
		requireCurationOK(t, f.writer.AdvanceReleaseMergeQueues(t.Context(), program.StoredRuleVersion))
		b3RequireCount(t, f.application, 2, `SELECT count(*) FROM curation_pending_items WHERE kind='release_merge' AND status='resolved'`)
		b3RequireCount(t, f.application, 0, `SELECT count(*) FROM curation_pending_items WHERE kind='release_merge' AND status<>'resolved'`)
	})
}
