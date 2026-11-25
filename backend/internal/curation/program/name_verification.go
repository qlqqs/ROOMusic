package program

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

// 同一套比较从实际曲目和发行资料得出可纠正的字段。提案不能提供身份、结构或外部值。
func structuralNameValues(e catalog.CurationEvidence, m MatchResult) []curation.VerificationCandidate {
	if m.Status != "matched" || m.Level == "release_group" || m.Level == "artist" {
		return nil
	}
	var values []curation.VerificationCandidate
	add := func(track, field, old, value, external string, auth metadata.Authenticity, protected bool) {
		if !protected && auth == metadata.Real && old != "" {
			values = append(values, curation.VerificationCandidate{ReleaseID: e.ID, TrackID: track, Field: field, Before: old, After: value, Structure: true, ExternalID: external, ExternalValue: value})
		}
	}
	recordings := map[string]sources.Track{}
	pairs := m.Pairs
	if r := m.Release; r != nil {
		var c comparison
		c.compareRelease(e, "model", *r)
		c.compareTracks(e, "model", *r)
		if !c.countEqual || !c.durationsOK || len(c.conflicts) > 0 || verifyAlbum(e, *r, c).Contradicted {
			return nil
		}
		pairs = c.pairs
		if e.ExternalID != r.ID {
			return nil
		}
		add("", "title", e.Title, r.Title, r.ID, e.Authenticity["title"], e.Corrected["title"] || e.Automatic["title"])
		add("", "album_artist", e.AlbumArtist, credits(r.Artists), r.ID, e.Authenticity["album_artist"], e.Corrected["album_artist"] || e.Automatic["album_artist"])
		for _, disc := range r.Media {
			for _, t := range disc.Tracks {
				recordings[t.RecordingID] = t
			}
		}
	} else if r := m.Recording; r != nil && len(e.Tracks) == 1 {
		if known, within := compareLength(e.Tracks[0].DurationSeconds, r.LengthMS); !known || !within || verifyRecording(e, *r).Contradicted {
			return nil
		}
		pairs = []TrackPair{{e.Tracks[0].ID, r.ID}}
		recordings[r.ID] = sources.Track{RecordingID: r.ID, Title: r.Title, Artists: r.Artists}
	} else {
		return nil
	}
	byTrack := map[string]string{}
	for _, p := range pairs {
		byTrack[p.TrackID] = p.RecordingID
	}
	for _, t := range e.Tracks {
		ext, ok := recordings[byTrack[t.ID]]
		if !ok || t.ExternalID != ext.RecordingID {
			continue
		}
		add(t.ID, "title", t.Title, cmp.Or(ext.Title, ext.RecordingTitle), ext.RecordingID, t.Authenticity["title"], t.TitleCorrected || t.TitleAutomatic)
		add(t.ID, "artist", t.Artist, credits(ext.Artists), ext.RecordingID, t.Authenticity["artist"], t.ArtistCorrected || t.ArtistAutomatic)
	}
	return values
}

func nameSourceOperation(ctx context.Context, q operations.Queryer, v curation.VerificationCandidate) (string, error) {
	target, id := "release", v.ReleaseID
	if v.TrackID != "" {
		target, id = "track", v.TrackID
	}
	var op string
	err := q.QueryRowContext(ctx, `SELECT operation_id::text FROM external_links WHERE source='musicbrainz' AND object_type=$1 AND object_id=$2 AND external_id=$3`, target, id, v.ExternalID).Scan(&op)
	return op, err
}

func saveNameDifferences(ctx context.Context, tx *sql.Tx, e catalog.CurationEvidence, m MatchResult) error {
	for _, v := range structuralNameValues(e, m) {
		if v.After == "" || sameText(v.Before, v.After) {
			continue
		}
		op, err := nameSourceOperation(ctx, tx, v)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		v.OperationID = op
		if err = curation.QueueVerification(ctx, tx, "verify_name", IdentityDigest(e), v); err != nil {
			return err
		}
	}
	return nil
}

func (m *ModelMatcher) VerificationPlan(ctx context.Context, item curation.PendingItem, in curation.VerificationInput) (curation.VerificationPlan, error) {
	if item.Kind != "verify_name" {
		return m.writer.VerificationPlan(ctx, item, in, nil)
	}
	var want curation.VerificationCandidate
	if err := json.Unmarshal(item.Evidence, &want); err != nil {
		return curation.VerificationPlan{}, err
	}
	e, err := m.evidence(ctx, m.db, item.TargetID)
	if err != nil {
		return curation.VerificationPlan{}, err
	}
	if IdentityDigest(e) != item.IdentityDigest {
		return curation.VerificationPlan{}, staleItem()
	}
	kind, id := "release_match", e.ExternalID
	if id == "" && len(e.Tracks) == 1 && e.Tracks[0].ID == want.TrackID {
		kind, id = "recording_match", e.Tracks[0].ExternalID
	}
	var actual *curation.VerificationCandidate
	if id != "" {
		match, err := m.readMatch(ctx, kind, e, id)
		if err != nil {
			if slices.Contains([]string{"track_count", "position_conflict", "media_count_mismatch"}, failureCode(err)) || errors.Is(err, sources.ErrNotFound) {
				return m.writer.VerificationPlan(ctx, item, in, nil)
			}
			return curation.VerificationPlan{}, err
		}
		for _, v := range structuralNameValues(e, match) {
			if v.Field != want.Field || v.TrackID != want.TrackID || v.Before != want.Before {
				continue
			}
			v.After = want.After
			v.OperationID, err = nameSourceOperation(ctx, m.db, v)
			if err != nil {
				return curation.VerificationPlan{}, err
			}
			actual = &v
			break
		}
	}
	return m.writer.VerificationPlan(ctx, item, in, actual)
}
