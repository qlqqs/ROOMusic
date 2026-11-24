package plugins

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/http/httpguts"
)

type Manifest struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Version   string     `json:"version"`
	Author    string     `json:"author"`
	ABI       int        `json:"abi"`
	Upstreams []Upstream `json:"upstreams"`
	Keys      []Key      `json:"keys"`
}

type Upstream struct {
	ID            string `json:"id"`
	BaseURL       string `json:"base_url"`
	MinIntervalMS int    `json:"min_interval_ms"`
}

type Injection struct {
	In   string `json:"in"`
	Name string `json:"name"`
}

type Key struct {
	Name     string    `json:"name"`
	Optional bool      `json:"optional"`
	Inject   Injection `json:"inject"`
	Upstream string    `json:"upstream"`
}

var pluginID = regexp.MustCompile(`^[a-z0-9-]{3,40}$`)
var upstreamID = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)
var keyID = regexp.MustCompile(`^[a-z_]{1,32}$`)

func strict(b []byte, v any) bool {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	return utf8.Valid(b) && d.Decode(v) == nil && d.Decode(new(any)) == io.EOF
}

func label(v string, n int) bool {
	return strings.TrimSpace(v) != "" && len(v) <= n && strings.IndexFunc(v, unicode.IsControl) < 0
}

func webURL(raw string) (*url.URL, bool) {
	u, err := url.Parse(raw)
	return u, err == nil && label(raw, 2048) && u.Hostname() != "" && u.User == nil && (u.Scheme == "https" || u.Scheme == "http")
}

func parseManifest(b []byte) (Manifest, error) {
	var m Manifest
	if !strict(b, &m) || !pluginID.MatchString(m.ID) || m.ABI != 1 || !label(m.Name, 160) || !label(m.Version, 64) || !label(m.Author, 160) {
		return m, errors.New("清单标识、作者、版本、名称或 ABI 无效")
	}
	ups := map[string]bool{}
	for _, up := range m.Upstreams {
		u, ok := webURL(up.BaseURL)
		if !ok || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery || !safePath(u.EscapedPath()) || !upstreamID.MatchString(up.ID) || ups[up.ID] || up.MinIntervalMS < 0 || up.MinIntervalMS > 86400000 {
			return m, errors.New("清单上游地址、标识或请求间隔无效")
		}
		ups[up.ID] = true
	}
	names := map[string]bool{}
	targets := map[string]bool{}
	for _, k := range m.Keys {
		target := k.Upstream + "/" + k.Inject.In + "/" + strings.ToLower(k.Inject.Name)
		if !keyID.MatchString(k.Name) || names[k.Name] || targets[target] || !ups[k.Upstream] || !httpguts.ValidHeaderFieldName(k.Inject.Name) || len(k.Inject.Name) > 128 {
			return m, errors.New("清单密钥名称、位置或上游无效")
		}
		switch k.Inject.In {
		case "query":
		case "header":
			switch strings.ToLower(k.Inject.Name) {
			case "host", "user-agent", "accept", "connection", "transfer-encoding", "content-length", "upgrade", "proxy-authorization", "accept-encoding":
				return m, errors.New("密钥不能覆盖请求控制头")
			}
		default:
			return m, errors.New("密钥位置必须为 header 或 query")
		}
		names[k.Name] = true
		targets[target] = true
	}
	if m.Upstreams == nil {
		m.Upstreams = []Upstream{}
	}
	if m.Keys == nil {
		m.Keys = []Key{}
	}
	return m, nil
}

func safePath(raw string) bool {
	if strings.HasPrefix(raw, "//") || strings.ContainsAny(raw, "?#\\") {
		return false
	}
	for _, part := range strings.Split(raw, "/") {
		s, err := url.PathUnescape(part)
		if err != nil || s == "." || s == ".." || strings.ContainsAny(s, "/\\%") || strings.IndexFunc(s, unicode.IsControl) >= 0 {
			return false
		}
	}
	return true
}
