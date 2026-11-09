package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// inputObject 保留字段是否出现，避免将缺失或 null 当成合法零值。
type inputObject map[string]json.RawMessage

func invalidInput(w http.ResponseWriter, r *http.Request) {
	httpserver.WriteError(w, r, http.StatusBadRequest, "invalid_input", "请求参数无效")
}

func invalidJSON(w http.ResponseWriter, r *http.Request) {
	httpserver.WriteError(w, r, http.StatusBadRequest, "invalid_json", "请求格式无效")
}

// decodeInput 复用平台的大小限制；完整读完后再解码，拒绝尾随 JSON 和超限空白。
// JSON 类型检查先于字段取值检查，未知设置键由调用方映射为 invalid_input。
func decodeInput(w http.ResponseWriter, r *http.Request, destination any) (inputObject, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil || !utf8.Valid(body) || !json.Valid(body) {
		invalidJSON(w, r)
		return nil, false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	if !httpserver.DecodeJSON(w, r, destination) {
		return nil, false
	}
	var fields inputObject
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		invalidJSON(w, r)
		return nil, false
	}
	// 与现有个人数据接口一致，拒绝重复键（包括转义后等价的键）。
	// 否则结构体会合并重复 settings，而字段表只保留最后一个对象，导致校验漏项。
	if !uniqueKeys(json.NewDecoder(bytes.NewReader(body))) {
		invalidInput(w, r)
		return nil, false
	}
	return fields, true
}

func uniqueKeys(decoder *json.Decoder) bool {
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return true
	}
	seen := map[string]bool{}
	for decoder.More() {
		if delimiter == '{' {
			key, err := decoder.Token()
			if err != nil {
				return false
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return false
			}
			seen[name] = true
		}
		if !uniqueKeys(decoder) {
			return false
		}
	}
	_, err = decoder.Token()
	return err == nil
}

func (input inputObject) only(allowed ...string) bool {
	for field := range input {
		found := false
		for _, name := range allowed {
			if field == name {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func pathID(w http.ResponseWriter, r *http.Request, name string) (string, bool) {
	id := r.PathValue(name)
	if !uuid.Valid(id) {
		httpserver.WriteError(w, r, http.StatusBadRequest, "invalid_id", "标识无效")
		return "", false
	}
	return strings.ToLower(id), true
}

// required 拒绝缺失和 null，但保留 0、false 与空数组供业务规则判断。
func (input inputObject) required(names ...string) bool {
	for _, name := range names {
		raw, exists := input[name]
		if !exists || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return false
		}
	}
	return true
}

func validSettings(fields inputObject, required bool) bool {
	raw, exists := fields["settings"]
	if !exists {
		return !required
	}
	var settings inputObject
	if json.Unmarshal(raw, &settings) != nil || settings == nil ||
		!settings.only("leveling", "gapless", "crossfade", "crossfade_seconds", "quality", "headroom") {
		return false
	}
	if raw, exists := settings["headroom"]; exists {
		var headroom inputObject
		if json.Unmarshal(raw, &headroom) != nil || !headroom.only("enabled", "db", "clip_indicator") || !headroom.required("enabled", "db", "clip_indicator") {
			return false
		}
	}
	for name := range settings {
		if !settings.required(name) {
			return false
		}
	}
	return true
}

// validCommandFields 只检查传输字段；取值和归属顺序由 Service 的统一校验拥有。
func validCommandFields(fields inputObject, command string) bool {
	if !fields.required("command") {
		return false
	}
	var required []string
	var nullable []string
	switch command {
	case "play_tracks":
		required = []string{"track_ids", "start_index"}
	case "play_next", "enqueue":
		required = []string{"track_ids"}
	case "play_entry", "remove_entry":
		required = []string{"entry_id"}
	case "move_entry":
		required = []string{"entry_id", "delta"}
	case "set_mode":
		required = []string{"mode"}
	case "seek":
		required = []string{"position_seconds"}
	case "set_settings":
		required = []string{"settings"}
		if !validSettings(fields, true) {
			return false
		}
	case "transfer":
		required = []string{"target_zone_id"}
	case "import":
		required = []string{"entries", "mode", "position_seconds", "history"}
		nullable = []string{"current_entry_id"}
		if _, exists := fields["current_entry_id"]; !exists {
			return false
		}
		var entries []inputObject
		if json.Unmarshal(fields["entries"], &entries) != nil {
			return false
		}
		for _, entry := range entries {
			if !entry.only("entry_id", "track_id") || !entry.required("entry_id", "track_id") {
				return false
			}
		}
	case "clear_queue", "play", "pause", "stop", "next", "previous", "retry":
	default:
		return false
	}
	allowed := append([]string{"command"}, required...)
	allowed = append(allowed, nullable...)
	return fields.only(allowed...) && fields.required(required...)
}
