package program

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"regexp"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

func artworkGap(e catalog.CurationEvidence) bool {
	return e.CandidateKind != "loose_unknown" && (e.ExternalID != "" || e.ReleaseGroupMatchID != "") && !e.LocalArtwork && (!e.ExternalArtwork || !e.ArtworkFile)
}

func mediaMiss(err error) bool {
	return errors.Is(err, sources.ErrNotFound) || errors.Is(err, sources.ErrInvalidQuery)
}

func (w *Worker) applyArtwork(ctx context.Context, u *unit, e catalog.CurationEvidence) (string, error) {
	return w.mediaFallback(ctx, u, sources.CoverArt, func(provider string) (string, error) { return w.artworkFrom(ctx, u, e, provider) })
}
func (w *Worker) artworkFrom(ctx context.Context, u *unit, e catalog.CurationEvidence, provider string) (string, error) {
	finder, ok := w.sources.CoverFinder(provider)
	if !ok {
		return "", operations.Conflict("source_disabled", "封面源不可用")
	}
	var targets []sources.CoverTarget
	if e.ExternalID != "" {
		targets = append(targets, sources.CoverTarget{Kind: "release", ID: e.ExternalID})
	}
	if e.ExternalGroupID != "" {
		targets = append(targets, sources.CoverTarget{Kind: "release_group", ID: e.ExternalGroupID})
	}
	for _, target := range targets {
		if provider != "coverartarchive" {
			target.Title = e.Authenticity["title"].SearchValue(e.Title)
			target.Artist = e.SelectedArtistAuth().SearchValue(albumArtist(e))
		}
		images, err := finder.Covers(ctx, target)
		if mediaMiss(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		image := sources.FrontImage(images)
		if image.ID == "" {
			continue
		}
		suppressed, err := curation.ExternalIdentitySuppressed(ctx, w.db, "release", e.ID, "artwork", provider, image.ID)
		if err != nil {
			return "", err
		}
		if suppressed {
			return "done", nil
		}
		file, err := finder.FrontCover(ctx, target)
		if mediaMiss(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		externalID := file.Image.ID
		if externalID == "" {
			externalID = image.ID
		}
		basis := "Cover Art Archive 主封面 " + externalID
		if provider != "coverartarchive" {
			basis = provider + " 主封面 " + externalID
		}
		request := curation.ArtworkRequest{ReleaseID: e.ID, Data: file.Data, ExternalID: externalID,
			Source: curation.Source{Kind: "source", Provider: provider, Basis: basis}}
		write := func(batch, key string) error {
			request.BatchID, request.Key = batch, key
			_, err := w.writer.ApplyArtwork(ctx, request)
			return err
		}
		for attempt := range 2 {
			if attempt == 1 {
				u.generation++
			}
			if err := u.write(ctx, "artwork", write); err != nil {
				return "", err
			}
			// 同键回放只返回原操作，仍需确认当前封面行指向的文件存在。
			entries, err := w.writer.LoadCurationEvidence(ctx, []string{e.ID})
			if err != nil {
				return "", err
			}
			fresh, visible := entries[e.ID]
			if !visible {
				return "", errReleaseChanged
			}
			if !fresh.ExternalArtwork || !artworkGap(fresh) {
				return "done", nil
			}
			fresh = subjectEvidence(u.subject, fresh, u.releaseState)
			if ArtworkDigest(afterMedia(e, fresh, u.subject, e.ID)) != ArtworkDigest(fresh) {
				return "", errReleaseChanged
			}
		}
		return "try_later", nil
	}
	return "no_match", nil
}

func lyricsGap(t catalog.CurationTrack) bool {
	return !t.LocalLyrics && !t.ExternalLyrics && looseKey(t.EffectiveTitleAuth.SearchValue(t.EffectiveTitle)) != "" && !lyricsArtistPlaceholder(t) && looseKey(t.EffectiveArtist) != "" && t.DurationSeconds > 0
}

func lyricsArtistPlaceholder(t catalog.CurationTrack) bool {
	return t.EffectiveArtistAuth == metadata.Placeholder
}

var artistSeparator = regexp.MustCompile(`\b(?:feat|ft)\.|[&/、,;×]`)

func primaryArtistKey(name string) string {
	key := textnorm.Text(name)
	if sep := artistSeparator.FindStringIndex(key); sep != nil {
		key = key[:sep[0]]
	}
	return textnorm.Loose(key)
}

func lyricsArtistMatches(local, external string) bool {
	if sameText(local, external) {
		return true
	}
	first := primaryArtistKey(local)
	return first != "" && first == primaryArtistKey(external)
}

type lyricsChoice struct {
	id, text string
	hash     [sha256.Size]byte
	synced   bool
}

func usableLyrics(body string) (string, bool) {
	body, ok := metadata.NormalizeLyrics(body)
	if !ok {
		return "", false
	}
	_, ok = catalog.ParseLyrics(body)
	return body, ok
}

func candidateLyrics(t catalog.CurationTrack, item sources.Lyrics) (lyricsChoice, bool) {
	if item.Instrumental || item.ID == "" || !(item.DurationSeconds > 0 && math.Abs(float64(t.DurationSeconds)-item.DurationSeconds) <= 2) ||
		!sameText(t.EffectiveTitle, item.Title) || !lyricsArtistMatches(t.EffectiveArtist, item.Artist) {
		return lyricsChoice{}, false
	}
	plain, hasPlain := usableLyrics(item.Plain)
	synced, hasSynced := usableLyrics(item.Synced)
	if !hasPlain && !hasSynced {
		return lyricsChoice{}, false
	}
	// 同一份原词可能同时有纯文本记录和附带时间轴的记录。
	content := plain
	if !hasPlain {
		content = synced
	}
	out := lyricsChoice{id: item.ID, text: plain, hash: sha256.Sum256([]byte(content)), synced: hasSynced}
	if hasSynced {
		out.text = synced
	}
	return out, true
}

func chooseLyrics(t catalog.CurationTrack, items []sources.Lyrics) (lyricsChoice, string) {
	var chosen lyricsChoice
	for _, item := range items {
		candidate, ok := candidateLyrics(t, item)
		if !ok {
			continue
		}
		if chosen.id != "" && chosen.hash != candidate.hash {
			return lyricsChoice{}, "uncertain"
		}
		if chosen.id == "" || (candidate.synced && !chosen.synced) {
			chosen = candidate
		}
	}
	if chosen.id == "" {
		return lyricsChoice{}, "no_match"
	}
	return chosen, "done"
}

func (w *Worker) applyLyrics(ctx context.Context, u *unit, e catalog.CurationEvidence, t catalog.CurationTrack) (string, error) {
	return w.mediaFallback(ctx, u, sources.LyricsLookup, func(provider string) (string, error) { return w.lyricsFrom(ctx, u, e, t, provider) })
}
func (w *Worker) lyricsFrom(ctx context.Context, u *unit, e catalog.CurationEvidence, t catalog.CurationTrack, provider string) (string, error) {
	finder, ok := w.sources.LyricsFinder(provider)
	if !ok {
		return "", operations.Conflict("source_disabled", "歌词源不可用")
	}
	query := sources.LyricsQuery{Title: t.EffectiveTitle, Artist: t.EffectiveArtist, DurationSeconds: t.DurationSeconds}
	if !e.TitlePlaceholder {
		query.Album = e.Title
	}
	exact, err := finder.LyricsFor(ctx, query)
	if err != nil && !mediaMiss(err) {
		return "", err
	}
	choice, accepted := candidateLyrics(t, exact)
	if err != nil || !accepted {
		items, err := finder.SearchLyrics(ctx, sources.LyricsQuery{Title: t.EffectiveTitle, Artist: t.EffectiveArtist})
		if mediaMiss(err) {
			return "no_match", nil
		}
		if err != nil {
			return "", err
		}
		var status string
		choice, status = chooseLyrics(t, items)
		if status != "done" {
			return status, nil
		}
	}
	if hasMarker(e.Suppressions, "track", t.ID, "lyrics", choice.text) ||
		hasMarker(e.Suppressions, "track", t.ID, "lyrics_source", provider+"\x00"+choice.id) {
		return "done", nil
	}
	basis := fmt.Sprintf("LRCLIB 记录 %s，标题、艺人和时长核对相合", choice.id)
	if provider != "lrclib" {
		basis = provider + " 歌词 " + choice.id
	}
	request := curation.LyricsRequest{TrackID: t.ID, Text: choice.text, ExternalID: choice.id,
		Source: curation.Source{Kind: "source", Provider: provider, Basis: basis}}
	err = u.write(ctx, "lyrics", func(batch, key string) error {
		request.BatchID, request.Key = batch, key
		_, err := w.writer.ApplyLyrics(ctx, request)
		return err
	})
	return "done", err
}

func (w *Worker) mediaFallback(ctx context.Context, u *unit, cap sources.Capability, run func(string) (string, error)) (string, error) {
	builtin := subjectSource(u.subject)
	ids := w.sources.PluginIDs(cap)
	if len(ids) == 0 {
		return run(builtin)
	}
	var failures mediaFailures
	last := ""
	for _, id := range append([]string{builtin}, ids...) {
		u.provider = id
		status, err := run(id)
		if err == nil {
			last = id
			if status != "no_match" {
				return status, nil
			}
			continue
		}
		if abort := failures.record(ctx, id, 1, err); abort != nil {
			return "", abort
		}
	}
	if last != "" {
		u.provider = last
	}
	if retry := failures.retryAbove(0); retry != nil {
		return "", retry
	}
	if last == "" {
		return "", operations.Conflict("source_disabled", "没有启用的资料源")
	}
	return "no_match", nil
}
