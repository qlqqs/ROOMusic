package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/dspsettings"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

type inputObject map[string]json.RawMessage

// 完整读取有界请求体，拒绝尾随内容、重复键、未知字段与显式 null。
func decodeInput(w http.ResponseWriter, r *http.Request, allowed ...string) (inputObject, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil || !utf8.Valid(body) || !json.Valid(body) {
		httpserver.WriteError(w, r, http.StatusBadRequest, "invalid_json", "请求格式无效")
		return nil, false
	}
	var fields inputObject
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil || !uniqueKeys(json.NewDecoder(bytes.NewReader(body))) {
		invalidInput(w, r)
		return nil, false
	}
	for name, value := range fields {
		found := false
		for _, key := range allowed {
			if name == key {
				found = true
				break
			}
		}
		if !found || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			invalidInput(w, r)
			return nil, false
		}
	}
	return fields, true
}

// 沿用个人数据与播放区入口的递归重复键检查，不把末个同名字段当作合法输入。
func uniqueKeys(decoder *json.Decoder) bool {
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return true
	}
	seen := make(map[string]bool)
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

func invalidInput(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, dspsettings.ErrInvalidInput)
}

func (fields inputObject) field(name string, destination any) bool {
	raw, ok := fields[name]
	return ok && json.Unmarshal(raw, destination) == nil
}

func (fields inputObject) peq() (zones.SettingsDocument, error) {
	return zones.ParsePEQDocument(fields["peq"])
}

func presetID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if !uuid.Valid(id) {
		invalidInput(w, r)
		return "", false
	}
	return strings.ToLower(id), true
}
