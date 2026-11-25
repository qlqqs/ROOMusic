package httpapi

import (
	"net/http"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/personal"
	"github.com/qlqq/roomusic/backend/internal/subsonic"
	"github.com/qlqq/roomusic/backend/internal/subsonic/protocol"
)

// 同一响应的三类实体共用批量本人状态读取。全库艺人索引和多碟详情按 personal 的上限分批。
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
		ratings, favorites, err := h.browse.Personal.LookupBrowseState(r.Context(), memberID(r), trackIDs, groupIDs, artistIDs)
		if err != nil {
			return protocol.SearchResult{}, err
		}
		// 状态读取会去重并统一小写，按 ID 对齐。
		byArtist := make(map[string]subsonic.PersonalState, len(ratings.Artists))
		for _, v := range ratings.Artists {
			byArtist[v.ArtistID] = subsonic.PersonalState{Rating: v.Rating}
		}
		for _, v := range favorites.Artists {
			state := byArtist[v.ArtistID]
			state.StarredAt = v.CreatedAt
			byArtist[v.ArtistID] = state
		}
		byAlbum := make(map[string]subsonic.PersonalState, len(ratings.Albums))
		for _, v := range ratings.Albums {
			byAlbum[v.GroupID] = subsonic.PersonalState{Rating: v.Rating}
		}
		for _, v := range favorites.Albums {
			state := byAlbum[v.GroupID]
			state.StarredAt = v.CreatedAt
			byAlbum[v.GroupID] = state
		}
		byTrack := make(map[string]subsonic.PersonalState, len(ratings.Tracks))
		for _, v := range ratings.Tracks {
			byTrack[v.TrackID] = subsonic.PersonalState{Rating: v.Rating}
		}
		for _, v := range favorites.Tracks {
			state := byTrack[v.TrackID]
			state.StarredAt = v.CreatedAt
			byTrack[v.TrackID] = state
		}
		for _, item := range artistBatch {
			out.Artists = append(out.Artists, subsonic.Artist(item, byArtist[strings.ToLower(item.ID)]))
		}
		for _, item := range albumBatch {
			out.Albums = append(out.Albums, subsonic.Album(item, byAlbum[strings.ToLower(item.ID)]))
		}
		for _, item := range trackBatch {
			out.Songs = append(out.Songs, subsonic.Song(item, byTrack[strings.ToLower(item.TrackID)]))
		}
		artists = artists[len(artistBatch):]
		albums = albums[len(albumBatch):]
		tracks = tracks[len(trackBatch):]
	}
	return out, nil
}

// 已缺失的评分专辑不占客户端的分页位置；沿本人评分顺序取页，分批补齐可见专辑。
func (h *Handlers) highestAlbums(r *http.Request, page catalog.BrowsePage) (protocol.Response, error) {
	var selected []catalog.BrowseAlbum
	size := min(500, page.Limit+min(page.Offset, 500))
	for offset := 0; len(selected) < page.Limit; {
		rated, err := h.browse.Personal.HighestRatedAlbums(r.Context(), memberID(r), offset, size)
		if err != nil {
			return protocol.Response{}, err
		}
		ids := make([]string, len(rated))
		for i, v := range rated {
			ids[i] = v.GroupID
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
			selected = append(selected, item)
			if len(selected) == page.Limit {
				break
			}
		}
		if len(rated) < size {
			break
		}
		offset += len(rated)
		size = max(100, size)
	}
	mapped, err := h.ratedEntities(r, nil, selected, nil)
	if err != nil {
		return protocol.Response{}, err
	}
	return protocol.Response{AlbumList2: &protocol.AlbumList{Albums: mapped.Albums}}, nil
}
