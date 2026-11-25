package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/personal"
	"github.com/qlqq/roomusic/backend/internal/subsonic/protocol"
)

func (h *Handlers) setRating(r *http.Request) protocol.Response {
	id, raw := r.Form.Get("id"), r.Form.Get("rating")
	if id == "" || raw == "" {
		return protocol.Failure(protocol.MissingParameter, "Missing id or rating")
	}
	digits := raw
	if raw[0] == '+' || raw[0] == '-' {
		digits = raw[1:]
	}
	if digits == "" || strings.Trim(digits, "0123456789") != "" {
		return protocol.Failure(protocol.MissingParameter, "Invalid integer rating")
	}
	rating, err := strconv.Atoi(raw)
	if err != nil || rating < 0 || rating > 5 {
		return protocol.Failure(protocol.General, "Rating must be between 0 and 5")
	}
	user := r.Context().Value(userKey{}).(identity.User)
	_, err = h.ratings.SetRatingByID(r.Context(), user.ID, id, rating)
	switch {
	case err == nil:
		return protocol.Response{}
	case errors.Is(err, personal.ErrNotFound), errors.Is(err, personal.ErrInvalidInput):
		return protocol.Failure(protocol.NotFound, "Content not found")
	default:
		return protocol.Failure(protocol.General, "Service unavailable")
	}
}
