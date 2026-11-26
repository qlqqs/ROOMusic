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
	if m.Status != "matched" || m.Level == "artist" {
		return nil
	}
	if m.Level == "release_group" {
		if v := groupTitleCandidate(e, m.GroupID, m.GroupTitle); v != nil {
			return []curation.VerificationCandidate{*v}
		}
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
		check := m.Verification
		if check == nil {
			check = verifyAlbum(e, *r, c)
		}
		if !c.countEqual || !c.durationsOK || len(c.conflicts) > 0 || check.Contradicted {
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

func groupTitleCandidate(e catalog.CurationEvidence, id, title string) *curation.VerificationCandidate {
	if id == "" || id != e.ReleaseGroupMatchID || looseKey(title) == "" || sameText(e.Title, title) ||
		e.Authenticity["title"] != metadata.Real || e.Title == "" || e.Corrected["title"] || e.Automatic["title"] {
		return nil
	}
	return &curation.VerificationCandidate{ReleaseID: e.ID, Field: "title", Before: e.Title, After: title,
		ExternalType: "release_group", ExternalID: id, ExternalValue: title, Structure: true}
}

func nameSourceOperation(ctx context.Context, q operations.Queryer, v curation.VerificationCandidate) (string, error) {
	target, id := v.NameLinkTarget()
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
	actual, err := m.currentNameCandidate(ctx, e, want)
	if err != nil {
		return curation.VerificationPlan{}, err
	}
	if actual != nil {
		if actual.Before != want.Before || (want.Structure && actual.VerificationCandidate != want) {
			return curation.VerificationPlan{}, staleItem()
		}
		actual.After = want.After
	}
	return m.writer.VerificationPlan(ctx, item, in, actual)
}

func (m *ModelMatcher) currentNameCandidate(ctx context.Context, e catalog.CurationEvidence, want curation.VerificationCandidate) (*curation.VerifiedName, error) {
	if e.ReleaseGroupMatchID != "" && want.TrackID == "" && want.Field == "title" && (e.ExternalID == "" || want.ExternalType == "release_group") {
		return m.currentGroupTitle(ctx, e, want)
	}
	kind, id := "release_match", e.ExternalID
	if id == "" && len(e.Tracks) == 1 && e.Tracks[0].ID == want.TrackID {
		kind, id = "recording_match", e.Tracks[0].ExternalID
	}
	if id != "" {
		match, err := m.readMatch(ctx, kind, e, id)
		if err != nil {
			if slices.Contains([]string{"track_count", "position_conflict", "media_count_mismatch"}, failureCode(err)) || errors.Is(err, sources.ErrNotFound) {
				return nil, nil
			}
			return nil, err
		}
		for _, v := range structuralNameValues(e, match) {
			if v.Field != want.Field || v.TrackID != want.TrackID {
				continue
			}
			v.OperationID, err = nameSourceOperation(ctx, m.db, v)
			if errors.Is(err, sql.ErrNoRows) {
				return nil, nil
			}
			if err != nil {
				return nil, err
			}
			check := match.Verification
			proof := curation.VerifiedName{VerificationCandidate: v, Match: curation.NameMatchEvidence{
				LocalTracks: check.LocalTracks, ExternalTracks: check.ExternalTracks, Comparable: check.Comparable, Within: check.Within,
			}}
			if r := match.Release; r != nil {
				proof.Match.ReleaseID, proof.Match.ReleaseTitle = r.ID, r.Title
			}
			return &proof, nil
		}
	}
	return nil, nil
}

func (m *ModelMatcher) currentGroupTitle(ctx context.Context, e catalog.CurationEvidence, want curation.VerificationCandidate) (*curation.VerifiedName, error) {
	group, err := found(m.sources.ReleaseGroupDetails(ctx, "musicbrainz", e.ReleaseGroupMatchID))
	if err != nil {
		return nil, err
	}
	v := groupTitleCandidate(e, group.ID, group.Title)
	if v == nil || !sameText(want.After, group.Title) || !group.Complete || len(group.Editions) == 0 {
		return nil, nil
	}
	for _, edition := range group.Editions {
		title := edition.Title
		if title == "" {
			f, ok := m.sources.ReleaseFinder("musicbrainz")
			if !ok {
				return nil, sources.ErrUnavailable
			}
			detail, err := found(f.ReleaseByID(ctx, edition.ID))
			if err != nil {
				return nil, err
			}
			if detail.ID != edition.ID || detail.Group.ID != group.ID {
				return nil, nil
			}
			title = detail.Title
		}
		if looseKey(title) == "" || sameText(e.Title, title) {
			return nil, nil
		}
	}
	v.OperationID, err = nameSourceOperation(ctx, m.db, *v)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &curation.VerifiedName{VerificationCandidate: *v, Match: curation.NameMatchEvidence{
		ReleaseGroupID: group.ID, ReleaseGroupTitle: group.Title, EditionCount: len(group.Editions), LocalTracks: len(e.Tracks),
	}}, nil
}

// 在领取事务外读回当前外部资料，下一轮模型及独立审查都使用刷新后的输入。
func (m *ModelMatcher) RetryStaleVerification(ctx context.Context, item curation.PendingItem) (bool, error) {
	if item.Kind == "verify_artist_credit" {
		return m.writer.HandleStaleVerification(ctx, item, "", nil)
	}
	entries, err := m.writer.LoadCurationEvidence(ctx, []string{item.TargetID})
	if err != nil {
		return false, err
	}
	e, ok := entries[item.TargetID]
	if !ok {
		return m.writer.HandleStaleVerification(ctx, item, "", nil)
	}
	var candidate *curation.VerificationCandidate
	if item.Kind == "verify_name" {
		var want curation.VerificationCandidate
		if err := json.Unmarshal(item.Evidence, &want); err != nil {
			return false, err
		}
		candidate = &want
		// 巡检建议保留原文和建议值；结构差异重新从当前发行或录音取两端的值。
		if want.Structure {
			actual, err := m.currentNameCandidate(ctx, e, want)
			if err != nil {
				return false, err
			}
			if actual != nil {
				candidate = &actual.VerificationCandidate
			} else {
				candidate = &curation.VerificationCandidate{ReleaseID: item.TargetID, TrackID: want.TrackID, Field: want.Field, Before: want.Before, After: want.After}
			}
		}
	}
	return m.writer.HandleStaleVerification(ctx, item, IdentityDigest(e), candidate)
}
