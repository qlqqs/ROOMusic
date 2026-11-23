package program

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"unicode"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

func planTrackFields(e catalog.CurationEvidence, m MatchResult) []curation.TrackRequest {
	type names struct{ title, artist string }
	recordings := map[string]names{}
	ambiguous := map[string]bool{}
	if r := m.Release; r != nil {
		for _, medium := range r.Media {
			for _, t := range medium.Tracks {
				value := names{metadata.NormalizeValue(t.Title), metadata.NormalizeValue(credits(t.Artists))}
				if old, ok := recordings[t.RecordingID]; ok && old != value {
					ambiguous[t.RecordingID] = true
				}
				recordings[t.RecordingID] = value
			}
		}
	} else if r := m.Recording; r != nil {
		recordings[r.ID] = names{metadata.NormalizeValue(r.Title), metadata.NormalizeValue(credits(r.Artists))}
	}
	pairs := map[string]string{}
	for _, pair := range m.Pairs {
		pairs[pair.TrackID] = pair.RecordingID
	}
	var requests []curation.TrackRequest
	for _, t := range e.Tracks {
		id := pairs[t.ID]
		value, ok := recordings[id]
		if id == "" || !ok || ambiguous[id] {
			continue
		}
		fields := map[string]catalog.CorrectionIntent{}
		add := func(name, text string, gap bool) {
			if !gap || text == "" || len(text) > metadata.MaxTextBytes || strings.IndexFunc(text, unicode.IsControl) >= 0 {
				return
			}
			intent := catalog.CorrectionIntent{Mode: catalog.CorrectionSet, Value: text}
			if !hasMarker(e.Suppressions, "track", t.ID, name, string(jsonValue(intent))) {
				fields[name] = intent
			}
		}
		add("title", value.title, t.TitlePlaceholder || t.TitleInferred)
		add("artist", value.artist, t.ArtistPlaceholder)
		if len(fields) > 0 {
			requests = append(requests, curation.TrackRequest{ReleaseID: e.ID, TrackID: t.ID, OnlyEmpty: true, Fields: fields})
		}
	}
	return requests
}

func writeTrackFields(ctx context.Context, writer *curation.Writer, requests []curation.TrackRequest, source curation.Source, write matchStep) error {
	for _, r := range requests {
		err := write(ctx, "track_fields/"+r.TrackID, func(batch, key string) error {
			r.BatchID, r.Key, r.Source = batch, key, source
			_, err := writer.ApplyTrack(ctx, r)
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// 调用方已用 needsWork 核对规则版本和输入摘要。
func matchBackfills(s curation.WorkState, e catalog.CurationEvidence) (tracks, artists bool) {
	if s.Status != "matched" {
		return false, false
	}
	if e.CandidateKind == "loose_unknown" {
		if len(e.Tracks) != 1 || e.Tracks[0].ExternalID == "" {
			return false, false
		}
	} else if e.ExternalID == "" {
		return false, false
	}
	var result map[string]json.RawMessage
	if json.Unmarshal(s.Result, &result) != nil {
		return false, false
	}
	if result["track_fields"] == nil {
		for _, t := range e.Tracks {
			if t.ExternalID != "" && (t.TitlePlaceholder || t.TitleInferred || t.ArtistPlaceholder) {
				tracks = true
				break
			}
		}
	}
	return tracks, result["artist_links"] == nil
}

func (w *Worker) linkedTracks(ctx context.Context, e catalog.CurationEvidence) (MatchResult, error) {
	var match MatchResult
	for _, t := range e.Tracks {
		if t.ExternalID != "" {
			match.Pairs = append(match.Pairs, TrackPair{t.ID, t.ExternalID})
		}
	}
	if e.CandidateKind != "loose_unknown" {
		finder, ok := w.sources.ReleaseFinder("musicbrainz")
		if !ok {
			return match, sources.ErrUnavailable
		}
		release, err := finder.ReleaseByID(ctx, e.ExternalID)
		if err == nil && release.ID == e.ExternalID {
			match.Release = &release
		}
		return match, err
	}
	finder, ok := w.sources.RecordingFinder("musicbrainz")
	if !ok {
		return match, sources.ErrUnavailable
	}
	recording, err := finder.RecordingByID(ctx, e.Tracks[0].ExternalID)
	if err == nil && recording.ID == e.Tracks[0].ExternalID {
		match.Recording = &recording
	}
	return match, err
}

func (w *Worker) backfillMatch(ctx context.Context, e *catalog.CurationEvidence, s curation.WorkState, states pageStates, tracks, artists bool) error {
	if err := w.ready(ctx); err != nil {
		return err
	}
	if err := w.sources.Check(ctx, "musicbrainz"); err != nil {
		if failureCode(err) == "source_disabled" {
			return nil
		}
		return err
	}
	var local catalog.CurationArtistCredits
	if artists {
		var err error
		local, err = catalog.LoadCurationArtistCredits(ctx, w.db, e.ID)
		if errors.Is(err, catalog.ErrArtistCatalogPreparing) {
			artists = false
			if !tracks {
				return nil
			}
		} else if err != nil {
			return err
		}
	}
	match, err := w.linkedTracks(ctx, *e)
	markers := map[string]string{}
	switch {
	case errors.Is(err, sources.ErrNotFound):
		if tracks {
			markers["track_fields"] = "unavailable"
		}
		if artists {
			markers["artist_links"] = "unavailable"
		}
	case failureCode(err) == "source_disabled" || curation.IsSourceUnavailable(err):
		return nil
	case err != nil:
		return err
	default:
		u := unit{w: w, subject: s.Subject, id: e.ID, generation: s.Generation, fixedGeneration: true, releaseID: e.ID, inputDigest: s.InputDigest, releaseState: s}
		source := curation.Source{Kind: "source", Provider: "musicbrainz", Basis: "按已有 MusicBrainz 对应补全"}
		if artists {
			links := planArtistLinks(local, *e, match)
			request := curation.LinkRequest{ReleaseID: e.ID, ExternalSource: "musicbrainz", Source: source}
			if err := writeArtistLinks(ctx, w.db, w.writer, request, links, u.write); err != nil {
				if failureCode(err) == "source_disabled" {
					return nil
				}
				return err
			}
			markers["artist_links"] = "none"
			if len(links) > 0 {
				markers["artist_links"] = "done"
			}
		}
		if tracks {
			requests := planTrackFields(*e, match)
			source.Basis = "按已有 MusicBrainz 对应补全曲目字段"
			if err := writeTrackFields(ctx, w.writer, requests, source, u.write); err != nil {
				if failureCode(err) == "source_disabled" {
					return nil
				}
				return err
			}
			markers["track_fields"] = "none"
			if len(requests) > 0 {
				markers["track_fields"] = "done"
			}
		}
	}
	if err := w.sources.Check(ctx, "musicbrainz"); err != nil {
		if failureCode(err) == "source_disabled" {
			return nil
		}
		return err
	}
	tx, err := w.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	fresh, err := catalog.LoadCurationEvidence(ctx, tx, []string{e.ID}, w.writer.ArtworkExists)
	if err != nil {
		return err
	}
	entry, ok := fresh[e.ID]
	if !ok || ReleaseDigest(entry) != s.InputDigest {
		return errReleaseChanged
	}
	err = tx.QueryRowContext(ctx, `UPDATE curation_work_states SET result=result || $5::jsonb,updated_at=now()
 WHERE subject='release' AND target_id=$1 AND status='matched' AND generation=$2 AND rule_version=$3 AND input_digest=$4 AND updated_at=$6
 RETURNING result,updated_at`, e.ID, s.Generation, s.RuleVersion, s.InputDigest, jsonValue(markers), s.UpdatedAt).Scan(&s.Result, &s.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return errReleaseChanged
	}
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	states.releases[e.ID] = s
	*e = entry
	return nil
}
