package httpapi

import (
	"net/http"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/personal"
	"github.com/qlqq/roomusic/backend/internal/subsonic/protocol"
)

func (h *Handlers) star(r *http.Request) protocol.Response   { return h.setStar(r, true) }
func (h *Handlers) unstar(r *http.Request) protocol.Response { return h.setStar(r, false) }

func (h *Handlers) setStar(r *http.Request, favorite bool) protocol.Response {
	tracks, albums, artists := r.Form["id"], r.Form["albumId"], r.Form["artistId"]
	if len(tracks)+len(albums)+len(artists) == 0 {
		return browseFailure(errParameter)
	}
	if err := h.browse.Personal.SetFavorites(r.Context(), memberID(r), tracks, albums, artists, favorite); err != nil {
		return browseFailure(err)
	}
	return protocol.Response{}
}

func (h *Handlers) getStarred2(r *http.Request) protocol.Response {
	ctx, user := r.Context(), memberID(r)
	var artists []catalog.BrowseArtist
	var albums []catalog.BrowseAlbum
	var tracks []catalog.BrowseTrack
	page := personal.Page{Number: 1, Size: 100}
	for {
		p, err := h.browse.Personal.FavoriteArtists(ctx, user, page)
		if err != nil {
			return browseFailure(err)
		}
		page.Revision = &p.Revision
		for _, item := range p.Items {
			artists = append(artists, catalog.BrowseArtist{ArtistSummary: item.Artist})
		}
		if int64(len(artists)) >= p.Pagination.Total {
			break
		}
		page.Number++
	}
	for page.Number = 1; ; page.Number++ {
		p, err := h.browse.Personal.FavoriteAlbums(ctx, user, page)
		if err != nil {
			return browseFailure(err)
		}
		ids := make([]string, 0, len(p.Items))
		for _, item := range p.Items {
			if item.Album.Available {
				ids = append(ids, item.Album.GroupID)
			}
		}
		loaded, err := h.browse.Catalog.LoadBrowseAlbums(ctx, ids)
		if err != nil {
			return browseFailure(err)
		}
		byID := make(map[string]catalog.BrowseAlbum, len(loaded))
		for _, item := range loaded {
			byID[item.ID] = item
		}
		for _, item := range p.Items {
			album := item.Album
			value, ok := byID[album.GroupID]
			if !ok {
				value = catalog.BrowseAlbum{ID: album.GroupID, AddedAt: item.CreatedAt,
					Representative: catalog.ReleaseSummary{Title: album.Title, Artist: album.Artist}}
			}
			albums = append(albums, value)
		}
		if int64(len(albums)) >= p.Pagination.Total {
			break
		}
	}
	for page.Number = 1; ; page.Number++ {
		p, err := h.browse.Personal.FavoriteTracks(ctx, user, page)
		if err != nil {
			return browseFailure(err)
		}
		ids := make([]string, 0, len(p.Items))
		for _, item := range p.Items {
			ids = append(ids, item.Track.TrackID)
		}
		items, err := h.orderedPersonalTracks(r, ids)
		if err != nil {
			return browseFailure(err)
		}
		tracks = append(tracks, items...)
		if int64(len(tracks)) >= p.Pagination.Total {
			break
		}
	}
	mapped, err := h.ratedEntities(r, artists, albums, tracks)
	if err != nil {
		return browseFailure(err)
	}
	return protocol.Response{Starred2: &mapped}
}

// 收藏与歌单保留已缺失曲目的安全摘要和顺序。
func (h *Handlers) orderedPersonalTracks(r *http.Request, ids []string) ([]catalog.BrowseTrack, error) {
	ctx := r.Context()
	out := make([]catalog.BrowseTrack, 0, len(ids))
	for start := 0; start < len(ids); start += 1000 {
		batch := ids[start:min(start+1000, len(ids))]
		loaded, err := h.browse.Catalog.LoadBrowseTracks(ctx, batch)
		if err != nil {
			return nil, err
		}
		byID := make(map[string]catalog.BrowseTrack, len(loaded))
		for _, track := range loaded {
			byID[track.TrackID] = track
		}
		var missing []string
		for _, id := range batch {
			if _, ok := byID[id]; !ok {
				missing = append(missing, id)
			}
		}
		if len(missing) > 0 {
			summaries, err := h.browse.Catalog.LoadTrackSummaries(ctx, missing)
			if err != nil {
				return nil, err
			}
			for _, id := range missing {
				summary, ok := summaries[id]
				if !ok {
					return nil, catalog.ErrTrackNotFound
				}
				byID[id] = catalog.BrowseTrack{TrackSummary: summary}
			}
		}
		for _, id := range batch {
			out = append(out, byID[id])
		}
	}
	return out, nil
}
