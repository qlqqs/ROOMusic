package httpapi

import (
	"bytes"
	"net/http"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/personal"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func (h *Handlers) playlists(w http.ResponseWriter, r *http.Request, user string) {
	page, ok := parsePage(w, r, 20, 20, -1)
	if !ok {
		return
	}
	result, err := h.service.Playlists(r.Context(), user, page)
	respond(w, r, result, err)
}
func (h *Handlers) playlist(w http.ResponseWriter, r *http.Request, user string) {
	id, ok := pathID(w, r, "playlist_id")
	if !ok {
		return
	}
	if _, ok = query(w, r); !ok {
		return
	}
	result, err := h.service.Playlist(r.Context(), user, id)
	respond(w, r, result, err)
}
func (h *Handlers) playlistItems(w http.ResponseWriter, r *http.Request, user string) {
	id, ok := pathID(w, r, "playlist_id")
	if !ok {
		return
	}
	page, ok := parsePage(w, r, 50, 100, 1)
	if !ok {
		return
	}
	result, err := h.service.PlaylistItems(r.Context(), user, id, page)
	respond(w, r, result, err)
}
func (h *Handlers) playlistQueue(w http.ResponseWriter, r *http.Request, user string) {
	id, ok := pathID(w, r, "playlist_id")
	if !ok {
		return
	}
	if _, ok = query(w, r); !ok {
		return
	}
	result, err := h.service.PlaylistQueue(r.Context(), user, id)
	respond(w, r, result, err)
}
func (h *Handlers) containingTrack(w http.ResponseWriter, r *http.Request, user string) {
	id, ok := pathID(w, r, "track_id")
	if !ok {
		return
	}
	if _, ok = query(w, r); !ok {
		return
	}
	result, err := h.service.ContainingTrack(r.Context(), user, id)
	respond(w, r, result, err)
}
func (h *Handlers) execute(w http.ResponseWriter, r *http.Request, user, kind string) {
	command := personal.Command{Kind: kind}
	var ok bool
	if kind != "create" {
		command.PlaylistID, ok = pathID(w, r, "playlist_id")
		if !ok {
			return
		}
	}
	if kind == "remove_item" || kind == "move_item" {
		command.ItemID, ok = pathID(w, r, "item_id")
		if !ok {
			return
		}
	}
	if _, ok = query(w, r); !ok {
		return
	}
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 || !uuid.Valid(strings.TrimSpace(keys[0])) {
		invalidInput(w, r)
		return
	}
	key := strings.ToLower(strings.TrimSpace(keys[0]))
	fields := map[string][]string{
		"create": {"name", "track_ids"}, "rename": {"name", "expected_revision"}, "delete": {"expected_revision"},
		"add_items": {"expected_revision", "track_ids"}, "remove_item": {"expected_revision"}, "restore_item": {"token"},
		"move_item": {"expected_revision", "action", "before_item_id"},
	}
	input, ok := decodeObject(w, r, false, fields[kind]...)
	if !ok {
		return
	}
	if !decodeCommand(input, &command) {
		invalidInput(w, r)
		return
	}
	result, err := h.service.Execute(r.Context(), user, key, command)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, result.Status, result.Body)
}
func decodeCommand(input object, command *personal.Command) bool {
	var ok bool
	if command.Kind != "create" && command.Kind != "restore_item" {
		command.ExpectedRevision, ok = input.revision()
		if !ok {
			return false
		}
	}
	switch command.Kind {
	case "create", "rename":
		if !input.field("name", &command.Name) {
			return false
		}
		var err error
		command.Name, err = personal.NormalizeName(command.Name)
		if err != nil {
			return false
		}
		if command.Kind == "create" {
			command.TrackIDs, ok = input.ids("track_ids", false, 10000)
			if !ok {
				return false
			}
		}
	case "add_items":
		command.TrackIDs, ok = input.ids("track_ids", true, 10000)
		if !ok {
			return false
		}
	case "restore_item":
		if !input.field("token", &command.Token) || !uuid.Valid(command.Token) {
			return false
		}
		command.Token = strings.ToLower(command.Token)
	case "move_item":
		if !input.field("action", &command.Action) {
			return false
		}
		raw, exists := input["before_item_id"]
		switch command.Action {
		case "up", "down", "first", "last":
			if exists {
				return false
			}
		case "before":
			if !exists {
				return false
			}
			if !bytes.Equal(raw, []byte("null")) {
				var id string
				if !input.field("before_item_id", &id) || !uuid.Valid(id) {
					return false
				}
				id = strings.ToLower(id)
				command.BeforeItemID = &id
			}
		default:
			return false
		}
	}
	return true
}
