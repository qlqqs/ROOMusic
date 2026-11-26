package app

import (
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

type artistMergeFixture struct {
	*programFixture
	primary, alias, third, mbid, track string
	remote                             sources.Release
}

func newArtistMergeFixture(t *testing.T) *artistMergeFixture {
	t.Helper()
	f := newArtistProgram(t)
	track, r := f.tagged(t)
	// 固定 UUID 使参与数较多的一方排在后面，能区分按参与数和按 UUID 保留。
	seed := artistSamples{t: t, app: f.application}
	seed.identity(2, "Artist A")
	seed.identity(1, "Artist Alias")
	mustExec(t, f.application, `UPDATE releases SET album_artist=artist WHERE id=$1`, f.a)
	mustExec(t, f.application, `UPDATE tracks SET artist='Artist Alias' WHERE id=$1`, track)
	mustExec(t, f.application, `UPDATE tracks SET source_status='present' WHERE medium_id IN (SELECT id FROM media WHERE release_id=$1)`, f.b)
	r.Artists[0].ArtistID = uuid.New()
	r.Media[0].Tracks[0].Artists = []sources.Credit{{Name: "Artist Alias", ArtistID: r.Artists[0].ArtistID}}
	f.source.releases[r.ID] = r
	publishSearch(t, f.application)
	return &artistMergeFixture{f, seed.id(2), seed.id(1), artistIDByName(t, f, "Artist B"), r.Artists[0].ArtistID, track, r}
}

func (f *artistMergeFixture) seedEvidence(t *testing.T) {
	t.Helper()
	req := artistLinkRequest(f.a, f.primary, f.mbid)
	req.Links = append(req.Links, curationCLink("artist", f.alias, f.mbid))
	curationCApplyLinks(t, f.curationFixture, req)
}

func (f *artistMergeFixture) mergeRequest(t *testing.T, a, b string) curation.ArtistAutoMerge {
	t.Helper()
	return curation.ArtistAutoMerge{A: a, B: b, ExternalID: f.mbid, Source: curation.Source{Kind: "source", Provider: "musicbrainz"}, BatchID: f.batch(t, "program"), Key: uuid.New()}
}

func (f *artistMergeFixture) postArtist(t *testing.T, artist, action string, body map[string]any) map[string]any {
	t.Helper()
	r := f.application.request(t, "POST", "/api/v1/artists/"+artist+"/"+action, body, &f.admin, idempotencyHeader(uuid.New()))
	if r.Code != 200 {
		t.Fatalf("%s HTTP %d %s", action, r.Code, r.Body)
	}
	return decodeJSONMap(t, r)
}

func (f *artistMergeFixture) revision(t *testing.T, artist string) int {
	t.Helper()
	var rev int
	requireCurationOK(t, f.application.database.QueryRow(`SELECT edit_revision FROM artists WHERE id=$1`, artist).Scan(&rev))
	return rev
}

func (f *artistMergeFixture) manualMerge(t *testing.T, target, source string) string {
	t.Helper()
	r := f.postArtist(t, target, "merge", map[string]any{"expected_revision": f.revision(t, target), "source_artist_id": source, "source_expected_revision": f.revision(t, source)})
	return r["operation_id"].(string)
}

func TestPostgreSQLCurationAutoMergeSameMusicBrainzArtist(t *testing.T) {
	f := newArtistMergeFixture(t)
	f.assertCount(t, 2, `SELECT count(*) FROM artist_effective_credits WHERE artist_id=$1`, f.primary)
	f.assertCount(t, 1, `SELECT count(*) FROM artist_effective_credits WHERE artist_id=$1`, f.alias)
	f.run(t)
	f.assertRelease(t, f.a, "matched", "tag_id")
	requireProgramArtistLink(t, f.programFixture, f.a, f.primary, f.mbid)
	requireProgramArtistLink(t, f.programFixture, f.a, f.alias, f.mbid)
	f.assertCount(t, 1, `SELECT count(*) FROM artist_overrides a JOIN operations o ON o.id=a.redirect_operation_id
	JOIN curation_batches b ON b.id=o.batch_id WHERE a.artist_id=$1 AND a.redirect_artist_id=$2 AND o.target_id=$2
	AND o.operation_type='artist_merge' AND o.origin->>'source'='curation' AND o.origin->>'provider_kind'='source'
	AND o.origin->>'provider'='musicbrainz' AND b.tier='program'`, f.alias, f.primary)
}

func TestPostgreSQLCurationAutoMergeBlocked(t *testing.T) {
	for _, tc := range []struct{ name, code string }{
		{"闭包标记", "nothing_to_apply"}, {"人工拆分", "manual_structure"}, {"忽略行", "manual_structure"},
		{"人工撤销过合并", "manual_structure"}, {"闭包内多个MBID", "no_strong_evidence"}, {"已重定向", "artist_redirected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newArtistMergeFixture(t)
			f.seedEvidence(t)
			a, b := f.primary, f.alias
			sample := artistSamples{t: t, app: f.application}
			switch tc.name {
			case "闭包标记":
				// 仅写标记，使此用例独立于人工撤销操作的另一条拦截路径。
				tx, err := f.application.database.BeginTx(t.Context(), nil)
				requireCurationOK(t, err)
				defer tx.Rollback()
				linkOp := programString(t, f.application, `SELECT operation_id FROM external_links WHERE object_id=$1`, a)
				requireCurationOK(t, f.writer.SuppressArtistMerge(t.Context(), tx, a, b, linkOp, f.admin.id))
				requireCurationOK(t, tx.Commit())
				f.manualMerge(t, f.third, b)
				b = f.third
			case "人工拆分":
				f.manualMerge(t, a, b)
				scope := map[string]any{"mode": "aliases", "name_key_ids": []string{sample.key(b)}}
				preview := f.postArtist(t, a, "split-preview", scope)
				scope["basis"], scope["expected_revision"] = preview["basis"], f.revision(t, a)
				scope["target"] = map[string]any{"kind": "restore", "artist_id": b, "expected_revision": f.revision(t, b)}
				f.postArtist(t, a, "split", scope)
			case "忽略行":
				left, right := sample.key(a), sample.key(b)
				op := programString(t, f.application, `SELECT operation_id FROM external_links WHERE object_id=$1`, a)
				mustExec(t, f.application, `INSERT INTO artist_candidate_ignores(left_key_id,right_key_id,operation_id) VALUES($1,$2,$3)`, min(left, right), max(left, right), op)
			case "人工撤销过合并":
				f.undo(t, f.manualMerge(t, a, b))
				f.assertCount(t, 0, `SELECT count(*) FROM curation_suppressions WHERE subject='artist_merge'`)
			case "闭包内多个MBID":
				curationCApplyLinks(t, f.curationFixture, artistLinkRequest(f.b, f.third, uuid.New()))
				f.manualMerge(t, b, f.third)
			case "已重定向":
				f.manualMerge(t, a, b)
			}
			_, err := f.writer.MergeArtists(t.Context(), f.mergeRequest(t, a, b))
			curationCRequireProblem(t, err, 409, tc.code)
			f.assertCount(t, 0, `SELECT count(*) FROM operations WHERE operation_type='artist_merge' AND origin->>'source'='curation'`)
		})
	}
}

func TestPostgreSQLCurationAutoMergeRevertSuppresses(t *testing.T) {
	f := newArtistMergeFixture(t)
	// 对应与合并分批，撤销合并批次后两条强证据仍在。
	f.seedEvidence(t)
	req := f.mergeRequest(t, f.primary, f.alias)
	merged, err := f.writer.MergeArtists(t.Context(), req)
	requireCurationOK(t, err)
	requireCurationOK(t, f.writer.CloseBatch(t.Context(), req.BatchID))
	result := f.scope(t, curation.Scope{Kind: "batch", ID: req.BatchID}, "", uuid.New(), uuid.New())
	if result.Reverted != 1 || len(result.Skipped) != 0 {
		t.Fatalf("按批撤销 %+v", result)
	}
	first, second := min(f.primary, f.alias), max(f.primary, f.alias)
	f.assertCount(t, 1, `SELECT count(*) FROM curation_suppressions s JOIN operations r ON r.id=s.operation_id
	WHERE r.reverts_operation_id=$1 AND s.subject='artist_merge' AND s.target_type='artist' AND s.target_id=$2
	AND s.value->>'artist_id'=$3 AND s.value->>'name'='Artist A' AND s.value_hash=$4`, merged.OperationID, first, second, fmt.Sprintf("%x", sha256.Sum256([]byte(second))))
	f.run(t)
	f.assertRelease(t, f.a, "matched", "tag_id")
	f.assertCount(t, 0, `SELECT count(*) FROM artist_overrides WHERE redirect_artist_id IS NOT NULL`)
	f.manualMerge(t, f.third, f.alias)
	// 让下一轮确实再尝试对位。
	before := f.source.calls["ReleaseByID"]
	mustExec(t, f.application, `UPDATE curation_work_states SET result=result-'artist_links' WHERE subject='release' AND target_id=$1`, f.a)
	f.clock.advance(t, 7*time.Hour)
	if f.source.calls["ReleaseByID"] <= before {
		t.Fatal("没有重新读取发行")
	}
	f.assertCount(t, 1, `SELECT count(*) FROM curation_work_states WHERE target_id=$1 AND result->>'artist_links'='done'`, f.a)
	f.assertCount(t, 1, `SELECT count(*) FROM operations WHERE operation_type='artist_merge' AND origin->>'source'='curation'`)
	f.assertCount(t, 1, `SELECT count(*) FROM artist_overrides WHERE artist_id=$1 AND redirect_artist_id=$2`, f.alias, f.third)
}

func TestPostgreSQLCurationAutoMergeReplay(t *testing.T) {
	f := newArtistMergeFixture(t)
	before := seedTrackFieldBackfill(t, f.programFixture, f.track, f.remote)
	// 在对应与合并已提交、回填标记尚未提交的边界中断工作者。
	mustExec(t, f.application, `CREATE FUNCTION interrupt_artist_backfill() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN RAISE EXCEPTION '模拟回填中断'; END $$`)
	mustExec(t, f.application, `CREATE TRIGGER interrupt_artist_backfill BEFORE UPDATE ON curation_work_states
	FOR EACH ROW WHEN (NEW.subject='release' AND NEW.result ? 'artist_links' AND NOT (OLD.result ? 'artist_links'))
	EXECUTE FUNCTION interrupt_artist_backfill()`)
	f.run(t)
	f.stop()
	<-f.done
	f.assertCount(t, 1, `SELECT count(*) FROM operations WHERE operation_type='artist_merge'`)
	f.assertCount(t, 1, `SELECT count(*) FROM curation_work_states WHERE subject='release' AND target_id=$1 AND NOT (result ? 'artist_links')`, f.a)
	mustExec(t, f.application, `DROP TRIGGER interrupt_artist_backfill ON curation_work_states`)
	f.run(t)
	f.assertCount(t, 1, `SELECT count(*) FROM curation_work_states WHERE target_id=$1 AND result->>'artist_links'='done'
	AND generation=$2 AND attempts=$3`, f.a, before.Generation, before.Attempts)
	f.assertCount(t, 1, `SELECT count(*) FROM operations WHERE operation_type='artist_merge'`)
	f.assertCount(t, 1, `SELECT count(*) FROM operations WHERE after_state->>'method'='artist_credit'`)
}
