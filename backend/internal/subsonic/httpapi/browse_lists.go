package httpapi

import (
	"math/rand/v2"
	"net/http"
	"net/url"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/personal"
	"github.com/qlqq/roomusic/backend/internal/subsonic/protocol"
)

func years(v url.Values, required bool) (from, to *int, err error) {
	for _, p := range []struct {
		key  string
		dest **int
	}{{"fromYear", &from}, {"toYear", &to}} {
		if !v.Has(p.key) && !required {
			continue
		}
		y, e := number(v, p.key, -1)
		if e != nil || y < 0 || y > 9999 {
			return nil, nil, errParameter
		}
		*p.dest = &y
	}
	return from, to, nil
}

func (h *Handlers) getAlbumList2(r *http.Request) protocol.Response {
	p, err := browsePage(r.Form, "size", "offset", 10)
	if err != nil {
		return browseFailure(err)
	}
	kind := r.Form.Get("type")
	params := catalog.BrowseAlbumParams{BrowsePage: p}
	var items []catalog.BrowseAlbum
	switch kind {
	case "alphabeticalByName":
		params.Sort = catalog.BrowseAlbumSortTitle
	case "alphabeticalByArtist":
		params.Sort = catalog.BrowseAlbumSortArtist
	case "newest":
		params.Sort = catalog.BrowseAlbumSortNewest
	case "random":
		params.Sort = catalog.BrowseAlbumSortRandom
		params.Seed = rand.Int64()
	case "byYear":
		params.Sort = catalog.BrowseAlbumSortYear
		params.FromYear, params.ToYear, err = years(r.Form, true)
	case "byGenre":
		params.Sort = catalog.BrowseAlbumSortGenre
		params.Genre = r.Form.Get("genre")
		if params.Genre == "" {
			err = errParameter
		}
	case "highest":
		out, err := h.highestAlbums(r, p)
		if err != nil {
			return browseFailure(err)
		}
		return out
	case "recent", "frequent", "starred":
		var ids []string
		if kind == "starred" {
			ids, err = h.favoriteAlbumIDs(r, p)
		} else {
			ids, err = h.browse.Listening.AlbumIDs(r.Context(), memberID(r), kind == "frequent", p.Limit, p.Offset)
		}
		if err == nil {
			items, err = h.browse.Catalog.LoadBrowseAlbums(r.Context(), ids)
		}
	default:
		err = errParameter
	}
	if err == nil && params.Sort != "" && p.Limit > 0 {
		items, err = h.browse.Catalog.ListBrowseAlbums(r.Context(), params)
	}
	if err != nil {
		return browseFailure(err)
	}
	mapped, err := h.ratedEntities(r, nil, items, nil)
	if err != nil {
		return browseFailure(err)
	}
	return protocol.Response{AlbumList2: &protocol.AlbumList{Albums: mapped.Albums}}
}

// 收藏可以保留整张专辑都已缺失的条目；客户端的 offset 只计算可见专辑。
func (h *Handlers) favoriteAlbumIDs(r *http.Request, p catalog.BrowsePage) ([]string, error) {
	ids := []string{}
	page := personal.Page{Number: 1, Size: 100}
	for len(ids) < p.Limit {
		items, err := h.browse.Personal.FavoriteAlbums(r.Context(), memberID(r), page)
		if err != nil {
			return nil, err
		}
		for _, item := range items.Items {
			if !item.Album.Available {
				continue
			}
			if p.Offset > 0 {
				p.Offset--
				continue
			}
			ids = append(ids, item.Album.GroupID)
			if len(ids) == p.Limit {
				break
			}
		}
		if int64(page.Number)*int64(page.Size) >= items.Pagination.Total {
			break
		}
		page.Number++
		page.Revision = &items.Revision
	}
	return ids, nil
}

func (h *Handlers) getRandomSongs(r *http.Request) protocol.Response {
	size, err := number(r.Form, "size", 10)
	if err != nil {
		return browseFailure(err)
	}
	from, to, err := years(r.Form, false)
	if err != nil {
		return browseFailure(err)
	}
	items, err := h.browse.Catalog.RandomTracks(r.Context(), catalog.RandomTrackParams{Limit: min(size, 500), Seed: rand.Int64(), Genre: r.Form.Get("genre"), FromYear: from, ToYear: to})
	if err != nil {
		return browseFailure(err)
	}
	mapped, err := h.ratedEntities(r, nil, nil, items)
	if err != nil {
		return browseFailure(err)
	}
	return protocol.Response{RandomSongs: &protocol.RandomSongs{Songs: mapped.Songs}}
}

func (h *Handlers) getGenres(r *http.Request) protocol.Response {
	counts, err := h.browse.Catalog.ListGenres(r.Context())
	if err != nil {
		return browseFailure(err)
	}
	out := protocol.Genres{Genres: make([]protocol.Genre, len(counts))}
	for i, c := range counts {
		out.Genres[i] = protocol.Genre{Name: c.Name, AlbumCount: c.AlbumCount, SongCount: c.SongCount}
	}
	return protocol.Response{Genres: &out}
}
