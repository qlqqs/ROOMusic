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

// 同一录音在发行中出现多次时，不能用录音编号唯一定位曲目署名。
func releaseTrackCredits(r *sources.Release) map[string]*curation.ReleaseArtistCredit {
	out := map[string]*curation.ReleaseArtistCredit{}
	if r != nil {
		for _, medium := range r.Media {
			for _, t := range medium.Tracks {
				if _, repeated := out[t.RecordingID]; repeated {
					out[t.RecordingID] = nil
				} else {
					out[t.RecordingID] = &curation.ReleaseArtistCredit{ReleaseID: r.ID, RecordingID: t.RecordingID, Artists: t.Artists}
				}
			}
		}
	}
	return out
}

func planTrackFields(e catalog.CurationEvidence, m MatchResult) []curation.TrackRequest {
	type names struct{ title, artist string }
	recordings := map[string]names{}
	ambiguous := map[string]bool{}
	artistCredits := releaseTrackCredits(m.Release)
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
		if title := m.TrackTitles[t.ID]; m.Level == "release_group" && title != "" && t.TitleInferred {
			value = names{title: metadata.NormalizeValue(title)}
		} else if id == "" || !ok || ambiguous[id] {
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
			requests = append(requests, curation.TrackRequest{ArtistCredit: artistCredits[id], ReleaseID: e.ID, TrackID: t.ID, OnlyEmpty: true, Fields: fields, ExpectedScan: map[string]json.RawMessage{"title": jsonValue(t.Title), "artist": jsonValue(t.Artist)}})
		}
	}
	return requests
}

func writeTrackFields(ctx context.Context, writer *curation.Writer, requests []curation.TrackRequest, origin curation.LinkRequest, write matchStep, record func(string)) error {
	for _, r := range requests {
		err := write(ctx, "track_fields/"+r.TrackID, func(batch, key string) error {
			r.BatchID, r.Key, r.Source = batch, key, origin.Source
			r.ConfirmedBy, r.ConversationID = origin.ConfirmedBy, origin.ConversationID
			applied, err := writer.ApplyTrack(ctx, r)
			if err == nil && record != nil {
				record(applied.OperationID)
			}
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// 已有对应的维护按结果标记独立执行。
func matchBackfills(s curation.WorkState, e catalog.CurationEvidence) (tracks, artists, merges bool) {
	if s.Status != "matched" {
		return false, false, false
	}
	var result map[string]json.RawMessage
	if json.Unmarshal(s.Result, &result) != nil {
		return false, false, false
	}
	if e.ReleaseGroupMatchID != "" && string(result["level"]) == `"release_group"` {
		return false, result["artist_links"] == nil, false
	}
	if e.CandidateKind == "loose_unknown" {
		if len(e.Tracks) != 1 || e.Tracks[0].ExternalID == "" {
			return false, false, false
		}
	} else if e.ExternalID == "" {
		return false, false, false
	}
	if result["track_fields"] == nil {
		for _, t := range e.Tracks {
			if t.ExternalID != "" && (t.TitlePlaceholder || t.TitleInferred || t.ArtistPlaceholder) {
				tracks = true
				break
			}
		}
	}
	return tracks, result["artist_links"] == nil, e.ExternalID != "" && result["release_merge"] == nil
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

func (w *Worker) backfillMatch(ctx context.Context, e *catalog.CurationEvidence, s curation.WorkState, states pageStates, tracks, artists, merges bool) error {
	if err := w.ready(ctx); err != nil {
		return err
	}
	if err := w.sources.Check(ctx, "musicbrainz"); err != nil {
		return err
	}
	var local catalog.CurationArtistCredits
	if artists {
		var err error
		local, err = catalog.LoadCurationArtistCredits(ctx, w.db, e.ID)
		if errors.Is(err, catalog.ErrArtistCatalogPreparing) {
			artists = false
			// 旧行先登记基线，艺人回填仍由缺失的 artist_links 标记在后续轮次触发。
			if !tracks && !merges && s.IdentityDigest != "" {
				return nil
			}
		} else if err != nil {
			return err
		}
	}
	markers := map[string]string{}
	if merges {
		peers, err := curation.ObjectsLinkedTo(ctx, w.db, "musicbrainz", "release", e.ExternalID)
		if err != nil {
			return err
		}
		if len(peers) < 2 {
			merges = false
			markers["release_merge"] = "done"
		}
	}
	var match MatchResult
	var proposals []curation.ArtistCreditProposal
	var err error
	var saved struct {
		releaseReviewState
		Level   string
		Rule    string
		GroupID string `json:"external_group_id"`
		Credits []sources.Credit
	}
	if err = json.Unmarshal(s.Result, &saved); err != nil {
		return err
	}
	if saved.Level == "release_group" && saved.GroupID == e.ReleaseGroupMatchID {
		match = MatchResult{Level: saved.Level, Rule: saved.Rule, GroupID: saved.GroupID, Credits: saved.Credits}
	} else if tracks || artists || merges {
		match, err = w.linkedTracks(ctx, *e)
		match.Rule = saved.Rule
	}
	switch {
	case errors.Is(err, sources.ErrNotFound):
		if merges {
			if err := w.writer.DiscoverReleaseMerges(ctx, e.ExternalID, resourceRuleVersion); err != nil {
				return err
			}
			markers["release_merge"] = "done"
		}
		if tracks {
			markers["track_fields"] = "unavailable"
		}
		if artists {
			markers["artist_links"] = "unavailable"
		}
	case failureCode(err) == "source_disabled" || curation.IsSourceUnavailable(err):
		return sourceFailure(ctx, "musicbrainz", err)
	case err != nil:
		return err
	default:
		u := unit{w: w, ruleVersion: s.RuleVersion, subject: s.Subject, id: e.ID, generation: s.Generation, fixedGeneration: true, releaseID: e.ID, inputDigest: s.InputDigest, releaseState: s}
		source := curation.Source{WriterLevel: catalog.LevelProgramExternal, Kind: "source", Provider: "musicbrainz", Basis: "按已有 MusicBrainz 对应补全"}
		if merges && match.Release == nil {
			if err := w.writer.DiscoverReleaseMerges(ctx, e.ExternalID, resourceRuleVersion); err != nil {
				return err
			}
			markers["release_merge"] = "done"
		}
		if merges && match.Release != nil {
			if err := w.finishEditionMerge(ctx, &u, *match.Release); err != nil {
				return err
			}
			markers["release_merge"] = "done"
		}
		if artists {
			var links []curation.LinkIntent
			links, proposals, err = w.identifyArtists(ctx, local, *e, match, false)
			if err != nil {
				if failureCode(err) == "source_disabled" || curation.IsSourceUnavailable(err) {
					return sourceFailure(ctx, "musicbrainz", err)
				}
				return err
			}
			if saved.RecheckedAt != nil {
				proposals = nil
			}
			request := curation.LinkRequest{ReleaseID: e.ID, ExternalSource: "musicbrainz", Source: source}
			splits := planCreditSplits(local, *e, match)
			if err := writeCreditSplits(ctx, w.writer, request, splits, u.write); err != nil {
				return err
			}
			if err := writeArtistLinks(ctx, w.db, w.writer, request, links, splits, u.write); err != nil {
				return err
			}
			markers["artist_links"] = "none"
			if len(links) > 0 || len(splits) > 0 {
				markers["artist_links"] = "done"
			}
		}
		if tracks {
			requests := planTrackFields(*e, match)
			source.Basis = "按已有 MusicBrainz 对应补全曲目字段"
			if err := writeTrackFields(ctx, w.writer, requests, curation.LinkRequest{Source: source}, u.write, nil); err != nil {
				return err
			}
			markers["track_fields"] = "none"
			if len(requests) > 0 {
				markers["track_fields"] = "done"
			}
		}
	}
	if err := w.sources.Check(ctx, "musicbrainz"); err != nil {
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
	d := releaseInputs(entry)
	if err := curation.SaveArtistCreditProposals(ctx, tx, e.ID, d.identity, proposals); err != nil {
		return err
	}
	if err := refreshMatchItem(ctx, tx, entry, s, d, MatchRuleVersion); err != nil {
		return err
	}
	err = tx.QueryRowContext(ctx, `UPDATE curation_work_states SET result=result || $5::jsonb,updated_at=now(),
 local_rule_version=$7,match_rule_version=$8,identity_digest=$9,gap_digest=$10,file_digest=$12,
 needs_work=manual_requested_at IS DISTINCT FROM $11::timestamptz,
 manual_requested_at=CASE WHEN manual_requested_at IS DISTINCT FROM $11 THEN manual_requested_at END
 WHERE subject='release' AND target_id=$1 AND status='matched' AND generation=$2 AND rule_version=$3 AND input_digest=$4 AND updated_at=$6
 RETURNING result,updated_at`, e.ID, s.Generation, s.RuleVersion, s.InputDigest, jsonValue(markers), s.UpdatedAt, LocalRuleVersion, MatchRuleVersion, d.identity, d.gap, s.ManualRequestedAt, d.file).Scan(&s.Result, &s.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return errReleaseChanged
	}
	if err != nil {
		return err
	}
	d.record(&s)
	s.MatchRuleVersion = MatchRuleVersion
	if err = tx.Commit(); err != nil {
		return err
	}
	states.releases[e.ID] = s
	*e = entry
	return nil
}
