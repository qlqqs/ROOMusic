package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/listening"
	"github.com/qlqq/roomusic/backend/internal/personal"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/subsonic/protocol"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

type Browse struct {
	Catalog       *catalog.Store
	Personal      *personal.Service
	Listening     *listening.Service
	Zones         *zones.Service
	ReadArtwork   func(ctx context.Context, id string, head bool) (string, []byte, error)
	DataDirectory string
}

var errParameter = errors.New("invalid parameter")
var errNotFound = errors.New("not found")

func browseFailure(err error) protocol.Response {
	switch {
	case errors.Is(err, errParameter):
		return protocol.Failure(protocol.MissingParameter, "Invalid or missing parameter")
	case errors.Is(err, errNotFound), errors.Is(err, sql.ErrNoRows), errors.Is(err, fs.ErrNotExist),
		errors.Is(err, catalog.ErrReleaseGroupNotFound), errors.Is(err, catalog.ErrArtistNotFound), errors.Is(err, personal.ErrNotFound):
		return protocol.Failure(protocol.NotFound, "Requested data was not found")
	default:
		return protocol.Failure(protocol.General, "Service unavailable")
	}
}

func memberID(r *http.Request) string { return r.Context().Value(userKey{}).(identity.User).ID }

func entityID(values url.Values) (string, error) {
	id := strings.ToLower(values.Get("id"))
	if id == "" {
		return "", errParameter
	}
	if !uuid.Valid(id) {
		return "", errNotFound
	}
	return id, nil
}

func number(values url.Values, key string, fallback int) (int, error) {
	if !values.Has(key) {
		return fallback, nil
	}
	n, err := strconv.Atoi(values.Get(key))
	if err != nil || n < 0 {
		return 0, errParameter
	}
	return n, nil
}

func browsePage(values url.Values, countKey, offsetKey string, fallback int) (catalog.BrowsePage, error) {
	n, err := number(values, countKey, fallback)
	if err != nil {
		return catalog.BrowsePage{}, err
	}
	offset, err := number(values, offsetKey, 0)
	return catalog.BrowsePage{Limit: min(n, 500), Offset: offset}, err
}

func (h *Handlers) getArtists(r *http.Request) protocol.Response {
	artists, err := h.browse.Catalog.BrowseArtists(r.Context())
	if err != nil && !errors.Is(err, catalog.ErrArtistCatalogPreparing) {
		return browseFailure(err)
	}
	mapped, err := h.ratedEntities(r, artists, nil, nil)
	if errors.Is(err, personal.ErrArtistCatalogPreparing) {
		return protocol.Response{Artists: &protocol.Artists{Indexes: []protocol.ArtistIndex{}}}
	}
	if err != nil {
		return browseFailure(err)
	}
	out := protocol.Artists{Indexes: []protocol.ArtistIndex{}}
	for i, a := range artists {
		if len(out.Indexes) == 0 || out.Indexes[len(out.Indexes)-1].Name != a.InitialGroup {
			out.Indexes = append(out.Indexes, protocol.ArtistIndex{Name: a.InitialGroup})
		}
		index := &out.Indexes[len(out.Indexes)-1]
		index.Artists = append(index.Artists, mapped.Artists[i])
	}
	return protocol.Response{Artists: &out}
}

func (h *Handlers) getArtist(r *http.Request) protocol.Response {
	id, err := entityID(r.Form)
	if err != nil {
		return browseFailure(err)
	}
	d, err := h.browse.Catalog.BrowseArtistAlbums(r.Context(), id)
	if err != nil {
		return browseFailure(err)
	}
	mapped, err := h.ratedEntities(r, []catalog.BrowseArtist{d.Artist}, d.Albums, nil)
	if err != nil {
		return browseFailure(err)
	}
	a := mapped.Artists[0]
	a.Albums = mapped.Albums
	a.AlbumCount = int64(len(a.Albums))
	return protocol.Response{Artist: &a}
}

func (h *Handlers) getAlbum(r *http.Request) protocol.Response {
	id, err := entityID(r.Form)
	if err != nil {
		return browseFailure(err)
	}
	a, tracks, err := h.browse.Catalog.BrowseAlbumTracks(r.Context(), id)
	if err != nil {
		return browseFailure(err)
	}
	mapped, err := h.ratedEntities(r, nil, []catalog.BrowseAlbum{a}, tracks)
	if err != nil {
		return browseFailure(err)
	}
	out := mapped.Albums[0]
	out.Songs = mapped.Songs
	lastDisc := -1
	for _, t := range tracks {
		if a.Representative.MediumCount > 1 && t.Disc != lastDisc && strings.TrimSpace(t.DiscTitle) != "" {
			out.DiscTitles = append(out.DiscTitles, protocol.DiscTitle{Disc: t.Disc, Title: t.DiscTitle})
		}
		lastDisc = t.Disc
	}
	return protocol.Response{Album: &out}
}

func (h *Handlers) getSong(r *http.Request) protocol.Response {
	id, err := entityID(r.Form)
	if err != nil {
		return browseFailure(err)
	}
	tracks, err := h.browse.Catalog.LoadBrowseTracks(r.Context(), []string{id})
	if err != nil {
		return browseFailure(err)
	}
	if len(tracks) == 0 {
		return browseFailure(errNotFound)
	}
	mapped, err := h.ratedEntities(r, nil, nil, tracks)
	if err != nil {
		return browseFailure(err)
	}
	return protocol.Response{Song: &mapped.Songs[0]}
}

func (h *Handlers) getArtistInfo2(r *http.Request) protocol.Response {
	if _, err := number(r.Form, "count", 20); err != nil {
		return browseFailure(err)
	}
	id, err := entityID(r.Form)
	if err != nil {
		return browseFailure(err)
	}
	d, err := h.browse.Catalog.LoadArtist(r.Context(), id)
	if err != nil {
		return browseFailure(err)
	}
	info := protocol.ArtistInfo{}
	if d.Bio != nil {
		info.Biography = d.Bio.Text
	}
	if d.Links.MusicBrainz != nil {
		info.MusicBrainzID = *d.Links.MusicBrainz
	}
	return protocol.Response{ArtistInfo2: &info}
}

func (h *Handlers) getAlbumInfo2(r *http.Request) protocol.Response {
	id, err := entityID(r.Form)
	if err != nil {
		return browseFailure(err)
	}
	items, err := h.browse.Catalog.LoadBrowseAlbums(r.Context(), []string{id})
	if err != nil {
		return browseFailure(err)
	}
	if len(items) == 0 {
		return browseFailure(errNotFound)
	}
	return protocol.Response{AlbumInfo: &protocol.AlbumInfo{MusicBrainzID: items[0].MusicBrainzID}}
}

func (h *Handlers) search3(r *http.Request) protocol.Response {
	v := r.Form
	if !v.Has("query") {
		return browseFailure(errParameter)
	}
	p := catalog.BrowseSearchParams{Query: strings.TrimSpace(v.Get("query"))}
	if p.Query == `""` {
		p.Query = ""
	}
	var err error
	for _, k := range []struct {
		name string
		page *catalog.BrowsePage
	}{{"artist", &p.Artists}, {"album", &p.Albums}, {"song", &p.Songs}} {
		*k.page, err = browsePage(v, k.name+"Count", k.name+"Offset", 20)
		if err != nil {
			return browseFailure(err)
		}
	}
	result, err := h.browse.Catalog.BrowseSearch(r.Context(), p)
	if err != nil {
		return browseFailure(err)
	}
	out, err := h.ratedEntities(r, result.Artists, result.Albums, result.Songs)
	if errors.Is(err, personal.ErrArtistCatalogPreparing) {
		// 目录快照结束后艺人投影可能开始重建；歌曲和专辑仍可读取本人的评分。
		out, err = h.ratedEntities(r, nil, result.Albums, result.Songs)
	}
	if err != nil {
		return browseFailure(err)
	}
	return protocol.Response{SearchResult3: &out}
}
