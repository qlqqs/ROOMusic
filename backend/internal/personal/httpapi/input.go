package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/personal"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// object 保留字段是否出现，避免 null、缺失和合法零值混淆。
type object map[string]json.RawMessage

func invalidInput(w http.ResponseWriter, r *http.Request) {
	httpserver.WriteError(w, r, http.StatusBadRequest, "invalid_input", "请求参数无效")
}

func decodeObject(w http.ResponseWriter, r *http.Request, empty bool, allowed ...string) (object, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		httpserver.WriteError(w, r, 400, "invalid_json", "请求格式无效")
		return nil, false
	}
	body = bytes.TrimSpace(body)
	if empty && len(body) == 0 {
		return object{}, true
	}
	if !utf8.Valid(body) || !json.Valid(body) {
		httpserver.WriteError(w, r, 400, "invalid_json", "请求格式无效")
		return nil, false
	}
	if body[0] != '{' || !uniqueKeys(json.NewDecoder(bytes.NewReader(body))) {
		invalidInput(w, r)
		return nil, false
	}
	var result object
	if err := json.Unmarshal(body, &result); err != nil {
		invalidInput(w, r)
		return nil, false
	}
	for name := range result {
		found := false
		for _, field := range allowed {
			if name == field {
				found = true
				break
			}
		}
		if !found {
			invalidInput(w, r)
			return nil, false
		}
	}
	return result, true
}

// uniqueKeys 在对象的每一层拒绝重复键，包含转义后等价的字段名。
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

func (input object) field(name string, destination any) bool {
	raw, exists := input[name]
	return exists && !bytes.Equal(raw, []byte("null")) && json.Unmarshal(raw, destination) == nil
}
func (input object) revision() (int64, bool) {
	var revision int64
	ok := input.field("expected_revision", &revision) && revision >= 1 && revision <= personal.MaxRevision
	return revision, ok
}
func (input object) ids(name string, required bool, limit int) ([]string, bool) {
	if _, exists := input[name]; !exists {
		return []string{}, !required
	}
	var ids []string
	if !input.field(name, &ids) || ids == nil || len(ids) > limit || (required && len(ids) == 0) {
		return nil, false
	}
	for i, id := range ids {
		if !uuid.Valid(id) {
			return nil, false
		}
		ids[i] = strings.ToLower(id)
	}
	return ids, true
}
func query(w http.ResponseWriter, r *http.Request, allowed ...string) (url.Values, bool) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		invalidInput(w, r)
		return nil, false
	}
	for key, entries := range values {
		valid := false
		for _, name := range allowed {
			if key == name {
				valid = true
				break
			}
		}
		if !valid {
			invalidInput(w, r)
			return nil, false
		}
		if len(entries) != 1 || entries[0] == "" {
			if key == "page" || key == "page_size" {
				httpserver.WriteError(w, r, 400, "invalid_pagination", "分页参数无效")
			} else {
				invalidInput(w, r)
			}
			return nil, false
		}
	}
	return values, true
}
func parsePage(w http.ResponseWriter, r *http.Request, defaultSize, maxSize int, revisionMinimum int64) (personal.Page, bool) {
	allowed := []string{"page", "page_size"}
	if revisionMinimum >= 0 {
		allowed = append(allowed, "revision")
	}
	values, ok := query(w, r, allowed...)
	if !ok {
		return personal.Page{}, false
	}
	page, size, err := httpserver.ParsePagination(r, defaultSize, maxSize)
	if err != nil {
		httpserver.WriteError(w, r, 400, "invalid_pagination", "分页参数无效")
		return personal.Page{}, false
	}
	result := personal.Page{Number: page, Size: size}
	if raw, exists := values["revision"]; exists {
		revision, err := strconv.ParseInt(raw[0], 10, 64)
		if err != nil || revision < revisionMinimum || revision > personal.MaxRevision {
			invalidInput(w, r)
			return result, false
		}
		result.Revision = &revision
	}
	return result, true
}
func pathID(w http.ResponseWriter, r *http.Request, name string) (string, bool) {
	id := r.PathValue(name)
	if !uuid.Valid(id) {
		invalidInput(w, r)
		return "", false
	}
	return strings.ToLower(id), true
}
