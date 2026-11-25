package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/subsonic/protocol"
)

func (h *Handlers) getLyricsBySongID(r *http.Request) protocol.Response {
	id, err := entityID(r.Form)
	if err != nil {
		return browseFailure(err)
	}
	result, err := h.browse.Catalog.LoadTrackLyrics(r.Context(), id)
	if err != nil {
		return browseFailure(err)
	}
	out := protocol.LyricsList{Items: []protocol.StructuredLyrics{}}
	if item := result.Lyrics; item != nil {
		lang := item.Language
		if lang == "" {
			lang = "und"
		}
		lyrics := protocol.StructuredLyrics{Language: lang, Synced: item.Kind == catalog.LyricsKindSynced,
			Lines: make([]protocol.LyricsLine, len(item.Lines))}
		for i, line := range item.Lines {
			lyrics.Lines[i] = protocol.LyricsLine{Start: line.StartMS, Value: line.Text}
		}
		out.Items = append(out.Items, lyrics)
	}
	return protocol.Response{LyricsList: &out}
}

func (h *Handlers) getLyrics(r *http.Request) protocol.Response {
	artist, title := strings.TrimSpace(r.Form.Get("artist")), strings.TrimSpace(r.Form.Get("title"))
	out := protocol.Response{Lyrics: &protocol.Lyrics{}}
	if artist == "" && title == "" {
		return out
	}
	ctx := r.Context()
	track, err := h.browse.Catalog.MatchTrackForLyrics(ctx, artist, title)
	if errors.Is(err, catalog.ErrTrackNotFound) {
		return out
	}
	if err != nil {
		return browseFailure(err)
	}
	result, err := h.browse.Catalog.LoadTrackLyrics(ctx, track.TrackID)
	if errors.Is(err, catalog.ErrTrackNotFound) {
		return out
	}
	if err != nil {
		return browseFailure(err)
	}
	if item := result.Lyrics; item != nil {
		lines := make([]string, len(item.Lines))
		for i, line := range item.Lines {
			lines[i] = line.Text
		}
		out.Lyrics = &protocol.Lyrics{Artist: track.Artist, Title: track.Title, Value: strings.Join(lines, "\n")}
	}
	return out
}
