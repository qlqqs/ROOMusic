package httpapi

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/personal"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/subsonic/protocol"
)

func playlistEntity(p personal.PlaylistState, owner string, duration float64) protocol.Playlist {
	return protocol.Playlist{
		ID: p.ID, Name: p.Name, Owner: owner, SongCount: p.TrackCount, Duration: int(duration),
		Created: p.CreatedAt.Format(time.RFC3339Nano), Changed: p.UpdatedAt.Format(time.RFC3339Nano),
		CoverArt: "pl-" + p.ID,
	}
}

func (h *Handlers) getPlaylists(r *http.Request) protocol.Response {
	user := r.Context().Value(userKey{}).(identity.User)
	if name := r.Form.Get("username"); name != "" && name != user.Username {
		return protocol.Failure(protocol.Forbidden, "Permission denied")
	}
	out := protocol.Playlists{Items: []protocol.Playlist{}}
	for page := (personal.Page{Number: 1, Size: 20}); ; page.Number++ {
		items, err := h.browse.Personal.Playlists(r.Context(), user.ID, page)
		if err != nil {
			return browseFailure(err)
		}
		for _, p := range items.Items {
			out.Items = append(out.Items, playlistEntity(p.PlaylistState, user.Username, p.DurationSeconds))
		}
		if int64(len(out.Items)) >= items.Pagination.Total || len(items.Items) < page.Size {
			break
		}
	}
	return protocol.Response{Playlists: &out}
}

func (h *Handlers) getPlaylist(r *http.Request) protocol.Response {
	id, err := entityID(r.Form)
	if err != nil {
		return browseFailure(err)
	}
	return h.playlistDetail(r, id)
}

func (h *Handlers) playlistDetail(r *http.Request, id string) protocol.Response {
	user := r.Context().Value(userKey{}).(identity.User)
	state, ids, err := h.browse.Personal.PlaylistTracks(r.Context(), user.ID, id)
	if err != nil {
		return browseFailure(err)
	}
	tracks, err := h.orderedPersonalTracks(r, ids)
	if err != nil {
		return browseFailure(err)
	}
	mapped, err := h.ratedEntities(r, nil, nil, tracks)
	if err != nil {
		return browseFailure(err)
	}
	var duration float64
	for _, track := range tracks {
		if track.DurationSeconds != nil {
			duration += *track.DurationSeconds
		}
	}
	p := playlistEntity(state, user.Username, duration)
	p.Entries = mapped.Songs
	return protocol.Response{Playlist: &p}
}

func (h *Handlers) createPlaylist(r *http.Request) protocol.Response {
	id, name := r.Form.Get("playlistId"), r.Form.Get("name")
	if r.Form.Has("playlistId") && !uuid.Valid(id) {
		return browseFailure(errNotFound)
	}
	if id == "" || r.Form.Has("name") {
		var err error
		name, err = personal.NormalizeName(name)
		if err != nil {
			return browseFailure(errParameter)
		}
	}
	tracks, err := playlistSongIDs(r.Form["songId"])
	if err != nil {
		return browseFailure(err)
	}
	if id != "" {
		result, err := h.changePlaylist(r, id, func([]string) (personal.Command, error) {
			return personal.Command{Kind: "replace_items", Name: name, TrackIDs: tracks}, nil
		})
		if err != nil {
			return browseFailure(err)
		}
		return h.playlistWriteResponse(r, result)
	}
	user := memberID(r)
	cmd := personal.Command{Kind: "create", Name: name, TrackIDs: tracks}
	if len(tracks) > personal.TrackLimit {
		return browseFailure(errParameter)
	}
	result, err := h.browse.Personal.Execute(r.Context(), user, uuid.New(), cmd)
	if err != nil {
		return browseFailure(err)
	}
	return h.playlistWriteResponse(r, result)
}

func (h *Handlers) playlistWriteResponse(r *http.Request, result personal.Result) protocol.Response {
	var receipt struct {
		Playlist personal.PlaylistState `json:"playlist"`
	}
	if err := json.Unmarshal(result.Body, &receipt); err != nil {
		return browseFailure(err)
	}
	return h.playlistDetail(r, receipt.Playlist.ID)
}

func (h *Handlers) updatePlaylist(r *http.Request) protocol.Response {
	id, err := playlistParamID(r)
	if err != nil {
		return browseFailure(err)
	}
	var name string
	if r.Form.Has("name") {
		name, err = personal.NormalizeName(r.Form.Get("name"))
		if err != nil {
			return browseFailure(errParameter)
		}
	}
	add, err := playlistSongIDs(r.Form["songIdToAdd"])
	if err != nil {
		return browseFailure(err)
	}
	remove := make(map[int]bool, len(r.Form["songIndexToRemove"]))
	for _, raw := range r.Form["songIndexToRemove"] {
		i, err := strconv.Atoi(raw)
		if err != nil || i < 0 {
			return browseFailure(errParameter)
		}
		remove[i] = true
	}
	_, err = h.changePlaylist(r, id, func(current []string) (personal.Command, error) {
		for i := range remove {
			if i >= len(current) {
				return personal.Command{}, errParameter
			}
		}
		tracks := make([]string, 0, len(current)+len(add))
		for i, id := range current {
			if !remove[i] {
				tracks = append(tracks, id)
			}
		}
		tracks = append(tracks, add...)
		return personal.Command{Kind: "replace_items", Name: name, TrackIDs: tracks}, nil
	})
	if err != nil {
		return browseFailure(err)
	}
	return protocol.Response{}
}

func (h *Handlers) deletePlaylist(r *http.Request) protocol.Response {
	id, err := playlistParamID(r)
	if err != nil {
		return browseFailure(err)
	}
	_, err = h.changePlaylist(r, id, func([]string) (personal.Command, error) {
		return personal.Command{Kind: "delete"}, nil
	})
	if err != nil {
		return browseFailure(err)
	}
	return protocol.Response{}
}

func playlistParamID(r *http.Request) (string, error) {
	id := r.Form.Get("playlistId")
	if id == "" {
		return "", errParameter
	}
	if !uuid.Valid(id) {
		return "", errNotFound
	}
	return id, nil
}

func playlistSongIDs(ids []string) ([]string, error) {
	out := make([]string, len(ids))
	for i, id := range ids {
		if !uuid.Valid(id) {
			return nil, errNotFound
		}
		out[i] = strings.ToLower(id)
	}
	return out, nil
}

func (h *Handlers) changePlaylist(r *http.Request, id string, build func([]string) (personal.Command, error)) (personal.Result, error) {
	ctx, user := r.Context(), memberID(r)
	for range 2 {
		state, tracks, err := h.browse.Personal.PlaylistTracks(ctx, user, id)
		if err != nil {
			return personal.Result{}, err
		}
		cmd, err := build(tracks)
		if err != nil {
			return personal.Result{}, err
		}
		cmd.PlaylistID, cmd.ExpectedRevision = state.ID, state.Revision
		result, err := h.browse.Personal.Execute(ctx, user, playlistRequestKey(user, cmd), cmd)
		if !errors.Is(err, personal.ErrRevisionConflict) {
			return result, err
		}
	}
	return personal.Result{}, personal.ErrRevisionConflict
}

func playlistRequestKey(user string, cmd personal.Command) string {
	data, _ := json.Marshal(cmd)
	sum := sha256.Sum256(append([]byte("subsonic-playlist:"+user+":"), data...))
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}
