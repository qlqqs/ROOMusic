package app

import (
	"reflect"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

func newArtistProgram(t *testing.T) *programFixture {
	t.Helper()
	f := newProgramFixture(t)
	f.writer.WithArtists(governance.NewArtistGovernance(artistResolutionAdapter{}))
	f.enable(t)
	f.sourceEnabled(t, "lrclib", false)
	f.sourceEnabled(t, "coverartarchive", false)
	return f
}

func artistIDByName(t *testing.T, f *programFixture, name string) string {
	t.Helper()
	return programString(t, f.application, `SELECT id FROM artists WHERE initial_name=$1`, name)
}

func artistLinkRequest(release, artist, external string) curation.LinkRequest {
	req := curationCLinkRequest(release, "", curationCLink("artist", artist, external))
	req.Method, req.Confidence, req.ExternalSource = "artist_credit", "certain", "musicbrainz"
	return req
}

func requireProgramArtistLink(t *testing.T, f *programFixture, release, artist, external string) {
	t.Helper()
	f.assertCount(t, 1, `SELECT count(*) FROM external_links l JOIN operations o ON o.id=l.operation_id
	JOIN curation_batches b ON b.id=o.batch_id WHERE l.object_type='artist' AND l.object_id=$1 AND l.external_id=$2
	AND l.method='artist_credit' AND l.confidence='certain' AND l.source='musicbrainz'
	AND o.target_id=$3 AND o.operation_type='external_link' AND o.origin->>'source'='curation'
	AND o.origin->>'provider_kind'='source' AND o.origin->>'provider'='musicbrainz' AND b.tier='program'`, artist, external, release)
}

func TestPostgreSQLCurationArtistLinks(t *testing.T) {
	t.Run("专辑曲目与单曲", func(t *testing.T) {
		f := newArtistProgram(t)
		track, r := f.tagged(t)
		r.Artists[0].ArtistID = uuid.New()
		guest := sources.Credit{Name: "曲目客座", ArtistID: uuid.New()}
		r.Media[0].Tracks[0].Artists = []sources.Credit{guest}
		f.source.releases[r.ID] = r
		mustExec(t, f.application, `UPDATE tracks SET artist=$2 WHERE id=$1`, track, guest.Name)
		mustExec(t, f.application, `UPDATE releases SET album_artist=artist WHERE id=$1`, f.a)
		single, _ := insertGroupedRelease(t, f.application, f.rootID, groupedRelease{title: "单曲", artist: "单曲歌手", tracks: []string{"present"}})
		st, _ := externalTrack(t, f.application, single)
		mustExec(t, f.application, `UPDATE releases SET candidate_kind='loose_unknown' WHERE id=$1`, single)
		mustExec(t, f.application, `UPDATE tracks SET duration_seconds=100 WHERE id=$1`, st)
		recording := sources.Recording{ID: uuid.New(), Title: "单曲", Artists: []sources.Credit{{Name: "单曲歌手", ArtistID: uuid.New()}}, LengthMS: r.Media[0].Tracks[0].LengthMS}
		f.source.recordings[recording.ID] = recording
		programTag(t, f.application, st, "mb_recording", recording.ID)
		publishSearch(t, f.application)
		id := artistIDByName(t, f, "Artist A")
		model := artistLinkRequest(f.a, id, r.Artists[0].ArtistID)
		model.Source.Kind, model.Source.Provider = "model", "synthetic"
		model.ExpectedRevision, model.ConfirmedBy = 1, f.admin.id
		_, err := f.writer.ApplyLinks(t.Context(), model)
		curationCRequireProblem(t, err, 400, "invalid_input")
		f.run(t)
		f.assertRelease(t, f.a, "matched", "tag_id")
		f.assertRelease(t, single, "matched", "tag_id")
		requireProgramArtistLink(t, f, f.a, id, r.Artists[0].ArtistID)
		requireProgramArtistLink(t, f, f.a, artistIDByName(t, f, guest.Name), guest.ArtistID)
		requireProgramArtistLink(t, f, single, artistIDByName(t, f, "单曲歌手"), recording.Artists[0].ArtistID)
		f.assertCount(t, 3, `SELECT count(*) FROM external_links WHERE object_type='artist'`)
	})
	t.Run("同位异名成为署名候选修改", func(t *testing.T) {
		f := newArtistProgram(t)
		track, r := f.tagged(t)
		mustExec(t, f.application, `UPDATE tracks SET artist='甲 / 乙' WHERE id=$1`, track)
		r.Media[0].Tracks[0].Artists = []sources.Credit{{Name: "乙", ArtistID: uuid.New()}, {Name: "甲", ArtistID: uuid.New()}}
		r.Artists = []sources.Credit{{Name: "外部艺人", ArtistID: uuid.New()}}
		f.source.releases[r.ID] = r
		publishSearch(t, f.application)
		f.assertCount(t, 2, `SELECT count(*) FROM artist_credits WHERE track_id=$1 AND role='track_artist' AND layer='scan'`, track)
		f.run(t)
		f.assertRelease(t, f.a, "matched", "tag_id")
		f.assertCount(t, 0, `SELECT count(*) FROM external_links WHERE object_type='artist'`)
		state := f.stateFor(t, "release", f.a)
		proposals, err := f.writer.OpenArtistCreditProposals(t.Context(), f.a, state.IdentityDigest)
		requireCurationOK(t, err)
		if len(proposals) != 1 {
			t.Fatalf("发行异名应保存一条候选修改，得到 %+v", proposals)
		}
		p := proposals[0]
		if p.Local.Name != "Artist A" || p.Local.Position != 1 || p.External.CreditName != "外部艺人" ||
			p.External.ArtistID != r.Artists[0].ArtistID || p.Basis.Level != "release" || p.Basis.ExternalID != r.ID ||
			p.Basis.OperationID != f.linkOwner(t, f.a) {
			t.Errorf("候选修改未保存同位署名和来源操作：%+v", p)
		}
	})
	t.Run("署名人数不同不写候选修改", func(t *testing.T) {
		f := newArtistProgram(t)
		track, r := f.tagged(t)
		mustExec(t, f.application, `UPDATE tracks SET artist='甲 / 乙' WHERE id=$1`, track)
		r.Media[0].Tracks[0].Artists = []sources.Credit{{Name: "其他艺人", ArtistID: uuid.New()}}
		r.Artists[0].ArtistID = uuid.New()
		f.source.releases[r.ID] = r
		publishSearch(t, f.application)
		f.assertCount(t, 2, `SELECT count(*) FROM artist_credits WHERE track_id=$1 AND role='track_artist' AND layer='scan'`, track)
		f.run(t)
		f.assertRelease(t, f.a, "matched", "tag_id")
		f.assertCount(t, 0, `SELECT count(*) FROM curation_artist_credit_proposals WHERE release_id=$1`, f.a)
		requireProgramArtistLink(t, f, f.a, artistIDByName(t, f, "Artist A"), r.Artists[0].ArtistID)
	})
	t.Run("排除自动层且人工修正优先", func(t *testing.T) {
		f := newArtistProgram(t)
		track, r := f.tagged(t)
		publishSearch(t, f.application)
		f.apply(t, f.a, "fixture", "", map[string]any{"album_artist": "自动专辑艺人"})
		applyTrackAuto(t, f.curationFixture, trackAutoRequest(track, f.a, "fixture", "", map[string]any{"artist": "自动曲目艺人"}))
		check := func(album, trackArtist string) {
			t.Helper()
			c, err := catalog.LoadCurationArtistCredits(t.Context(), f.application.database, f.a)
			requireCurationOK(t, err)
			if len(c.ReleaseArtists) != 1 || c.ReleaseArtists[0].Name != album || len(c.Tracks[track]) != 1 || c.Tracks[track][0].Name != trackArtist {
				t.Fatalf("发行和曲目应分别取 %q 与 %q，得到 %+v", album, trackArtist, c)
			}
		}
		check("Artist A", "Artist A")
		f.correct(t, f.a, 1, map[string]any{"album_artist": setChange("人工专辑艺人")})
		correctTrackForCuration(t, f.curationFixture, track, 1, map[string]any{"artist": setChange("人工曲目艺人")})
		check("人工专辑艺人", "人工曲目艺人")
		r.Artists = []sources.Credit{{Name: "人工专辑艺人", ArtistID: uuid.New()}}
		r.Media[0].Tracks[0].Artists = []sources.Credit{{Name: "人工曲目艺人", ArtistID: uuid.New()}}
		f.source.releases[r.ID] = r
		f.run(t)
		requireProgramArtistLink(t, f, f.a, artistIDByName(t, f, "人工专辑艺人"), r.Artists[0].ArtistID)
		requireProgramArtistLink(t, f, f.a, artistIDByName(t, f, "人工曲目艺人"), r.Media[0].Tracks[0].Artists[0].ArtistID)
	})
}

func TestPostgreSQLCurationArtistLinksBackfill(t *testing.T) {
	f := newArtistProgram(t)
	track, _ := externalTrack(t, f.application, f.a)
	publishSearch(t, f.application)
	r := programAlbum("Album A", "Artist A")
	r.Artists[0].ArtistID = uuid.New()
	f.source.releases[r.ID] = r
	before := seedTrackFieldBackfill(t, f, track, r)
	f.run(t)
	requireProgramArtistLink(t, f, f.a, artistIDByName(t, f, "Artist A"), r.Artists[0].ArtistID)
	after := f.assertRelease(t, f.a, "matched", "barcode")
	f.assertCount(t, 1, `SELECT count(*) FROM curation_work_states WHERE target_id=$1 AND result->>'artist_links'='done'`, f.a)
	if after.RuleVersion != before.RuleVersion || after.Attempts != before.Attempts || after.Generation != before.Generation || after.InputDigest != before.InputDigest {
		t.Fatalf("回填改变了原匹配状态 %+v -> %+v", before, after)
	}
	f.clock.advance(t, 7*time.Hour)
	if again := f.stateFor(t, "release", f.a); !reflect.DeepEqual(after, again) || f.source.calls["ReleaseByID"] != 1 || f.source.calls["SearchReleases"] != 0 {
		t.Fatalf("补做后再次处理，状态 %+v，外部调用 %v", again, f.source.calls)
	}
	f.assertCount(t, 1, `SELECT count(*) FROM operations WHERE after_state->>'method'='artist_credit'`)
}

func TestPostgreSQLCurationExternalLinkArtistRevertSuppresses(t *testing.T) {
	f := newArtistProgram(t)
	publishSearch(t, f.application)
	artist := artistIDByName(t, f, "Artist A")
	req := artistLinkRequest(f.a, artist, uuid.New())
	op := curationCApplyLinks(t, f.curationFixture, req)
	f.undo(t, op.OperationID)
	req.Key = uuid.New()
	got, err := f.writer.ApplyLinks(t.Context(), req)
	curationCRequireProblem(t, err, 409, "nothing_to_apply")
	if got.Skipped["artist:"+artist] != "suppressed" {
		t.Fatalf("同一艺人对应的跳过原因 %+v", got)
	}
	f.assertCount(t, 0, `SELECT count(*) FROM external_links WHERE object_type='artist' AND object_id=$1`, artist)
	req.Key, req.Links[0].ExternalID = uuid.New(), uuid.New()
	curationCApplyLinks(t, f.curationFixture, req)
}
