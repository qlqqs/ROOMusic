package httpapi

import (
	"net/http"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/personal"
	"github.com/qlqq/roomusic/backend/internal/subsonic"
	"github.com/qlqq/roomusic/backend/internal/subsonic/protocol"
)

// 同一响应的三类实体共用一次本人评分读取。全库艺人索引和多碟详情按 personal 的上限分批。
func (h *Handlers) ratedEntities(r *http.Request, artists []catalog.BrowseArtist, albums []catalog.BrowseAlbum, tracks []catalog.BrowseTrack) (protocol.SearchResult, error) {
	out := protocol.SearchResult{
		Artists: make([]protocol.Artist, 0, len(artists)),
		Albums:  make([]protocol.Album, 0, len(albums)),
		Songs:   make([]protocol.Song, 0, len(tracks)),
	}
	for len(artists)+len(albums)+len(tracks) > 0 {
		artistBatch := artists[:min(len(artists), 1000)]
		albumBatch := albums[:min(len(albums), 1000)]
		trackBatch := tracks[:min(len(tracks), personal.TrackLimit)]
		artistIDs := make([]string, len(artistBatch))
		for i, artist := range artistBatch {
			artistIDs[i] = artist.ID
		}
		groupIDs := make([]string, len(albumBatch))
		for i, album := range albumBatch {
			groupIDs[i] = album.ID
		}
		trackIDs := make([]string, len(trackBatch))
		for i, track := range trackBatch {
			trackIDs[i] = track.TrackID
		}
		ratings, err := h.browse.Personal.LookupRatings(r.Context(), memberID(r), trackIDs, groupIDs, artistIDs)
		if err != nil {
			return protocol.SearchResult{}, err
		}
		// LookupRatings 会去重并统一小写，按 ID 取回，不按位置对齐。
		byArtist := make(map[string]*int, len(ratings.Artists))
		for _, v := range ratings.Artists {
			byArtist[v.ArtistID] = v.Rating
		}
		byAlbum := make(map[string]*int, len(ratings.Albums))
		for _, v := range ratings.Albums {
			byAlbum[v.GroupID] = v.Rating
		}
		byTrack := make(map[string]*int, len(ratings.Tracks))
		for _, v := range ratings.Tracks {
			byTrack[v.TrackID] = v.Rating
		}
		for _, item := range artistBatch {
			out.Artists = append(out.Artists, subsonic.Artist(item, subsonic.PersonalState{Rating: byArtist[strings.ToLower(item.ID)]}))
		}
		for _, item := range albumBatch {
			out.Albums = append(out.Albums, subsonic.Album(item, subsonic.PersonalState{Rating: byAlbum[strings.ToLower(item.ID)]}))
		}
		for _, item := range trackBatch {
			out.Songs = append(out.Songs, subsonic.Song(item, subsonic.PersonalState{Rating: byTrack[strings.ToLower(item.TrackID)]}))
		}
		artists = artists[len(artistBatch):]
		albums = albums[len(albumBatch):]
		tracks = tracks[len(trackBatch):]
	}
	return out, nil
}

// 已缺失的评分专辑不占客户端的分页位置；沿本人评分顺序取页，分批补齐可见专辑。
func (h *Handlers) highestAlbums(r *http.Request, page catalog.BrowsePage) (protocol.Response, error) {
	out := protocol.AlbumList{Albums: []protocol.Album{}}
	size := min(500, page.Limit+min(page.Offset, 500))
	for offset := 0; len(out.Albums) < page.Limit; {
		rated, err := h.browse.Personal.HighestRatedAlbums(r.Context(), memberID(r), offset, size)
		if err != nil {
			return protocol.Response{}, err
		}
		ids := make([]string, len(rated))
		ratings := make(map[string]int, len(rated))
		for i, v := range rated {
			ids[i] = v.GroupID
			ratings[v.GroupID] = v.Rating
		}
		if len(ids) == 0 {
			break
		}
		albums, err := h.browse.Catalog.LoadBrowseAlbums(r.Context(), ids)
		if err != nil {
			return protocol.Response{}, err
		}
		for _, item := range albums {
			if page.Offset > 0 {
				page.Offset--
				continue
			}
			stars := ratings[item.ID]
			out.Albums = append(out.Albums, subsonic.Album(item, subsonic.PersonalState{Rating: &stars}))
			if len(out.Albums) == page.Limit {
				break
			}
		}
		if len(rated) < size {
			break
		}
		offset += len(rated)
		size = max(100, size)
	}
	return protocol.Response{AlbumList2: &out}, nil
}
