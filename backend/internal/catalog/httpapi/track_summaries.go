package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func (handlers *Handlers) trackSummaries(w http.ResponseWriter, r *http.Request) {
	if !handlers.guard.RequireSameOrigin(w, r) {
		return
	}
	if _, err := handlers.guard.Authenticate(r); err != nil {
		handlers.guard.WriteUnauthenticated(w, r)
		return
	}
	ids, ok := decodeTrackSummaryIDs(w, r)
	if !ok {
		return
	}
	summaries, err := handlers.store.LoadTrackSummaries(r.Context(), ids)
	if err != nil {
		httpserver.WriteError(w, r, http.StatusServiceUnavailable, "database_unavailable", "无法读取曲目摘要")
		return
	}
	items := make([]catalog.TrackSummary, 0, len(summaries))
	for _, id := range ids {
		if summary, exists := summaries[id]; exists {
			items = append(items, summary)
		}
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// decodeTrackSummaryIDs 在 HTTP 边界拒绝非法身份；内部批量读取会跳过非法 UUID，不能代替请求校验。
func decodeTrackSummaryIDs(w http.ResponseWriter, r *http.Request) ([]string, bool) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil || !utf8.Valid(raw) || !json.Valid(raw) {
		httpserver.WriteError(w, r, http.StatusBadRequest, "invalid_json", "请求格式无效")
		return nil, false
	}
	invalid := func() ([]string, bool) {
		httpserver.WriteError(w, r, http.StatusBadRequest, "invalid_input", "曲目标识列表无效")
		return nil, false
	}
	// 复用同包严格对象检查，拒绝重复字段（包括转义后等价的字段名），不采用最后一个值。
	if uniqueArtistJSON(raw) != nil {
		return invalid()
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 1 {
		return invalid()
	}
	var input []string
	value, exists := fields["track_ids"]
	if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, &input) != nil || len(input) == 0 {
		return invalid()
	}
	ids := make([]string, 0, min(len(input), 1000))
	seen := make(map[string]bool, cap(ids))
	for _, id := range input {
		if !uuid.Valid(id) {
			return invalid()
		}
		id = strings.ToLower(id)
		if seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
		if len(ids) > 1000 {
			return invalid()
		}
	}
	return ids, true
}
