package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/provider"
)

func invalidInput(w http.ResponseWriter, r *http.Request) {
	httpserver.WriteError(w, r, 400, "invalid_input", "请求参数无效")
}

// decodeInput 沿用平台的 1 MiB 上限及错误信封，拒绝尾随 JSON 和未声明字段。
func decodeInput(w http.ResponseWriter, r *http.Request, target any) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil || !utf8.Valid(body) || !json.Valid(body) {
		httpserver.WriteError(w, r, 400, "invalid_json", "请求格式无效")
		return false
	}
	body = bytes.TrimSpace(body)
	if body[0] != '{' {
		invalidInput(w, r)
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		invalidInput(w, r)
		return false
	}
	return true
}

// PostgreSQL 的文本列不能保存 NUL；在实际输入边界报告参数错误。
func validText(value string) bool { return !strings.ContainsRune(value, 0) }

func conversationID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if !uuid.Valid(id) {
		httpserver.WriteError(w, r, 400, "invalid_id", "会话标识无效")
		return "", false
	}
	return strings.ToLower(id), true
}

func validEndpoint(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(endpoint, "#") || !validHost(u.Hostname()) {
		return false
	}
	if strings.HasSuffix(u.Host, ":") {
		return false
	}
	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		return err == nil && port >= 1 && port <= 65535
	}
	return true
}

func validHost(host string) bool {
	if host == "" {
		return false
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.Zone() == ""
	}
	host = strings.TrimSuffix(host, ".")
	if len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

func normalizeHost(host string) string {
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.Unmap().String()
	}
	return strings.TrimSuffix(strings.ToLower(host), ".")
}

func validProtocol(protocol string) bool {
	return protocol == string(provider.ProtocolOpenAI) || protocol == string(provider.ProtocolMessages) || protocol == string(provider.ProtocolResponses)
}
