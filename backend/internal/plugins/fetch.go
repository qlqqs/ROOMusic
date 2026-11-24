package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/sources"
	"golang.org/x/net/http/httpguts"
)

type endpoint struct {
	Upstream
	address   string
	requester *sources.Requester
}

func (p *Source) initEndpoints() {
	p.upstreams = map[string]*endpoint{}
	for _, up := range p.Manifest.Upstreams {
		address := up.BaseURL
		if p.store.mock != "" {
			address = strings.TrimRight(p.store.mock, "/") + "/plugins/" + p.Manifest.ID + "/" + up.ID
		}
		interval := time.Duration(up.MinIntervalMS) * time.Millisecond
		d := sources.Declaration{ID: "plugin:" + p.Manifest.ID + "." + up.ID + "." + p.SHA256, MinInterval: interval}
		opts := sources.Options{DataDirectory: filepath.Dir(p.store.dir), Logger: p.store.logger, Limiter: sources.NewLimiter(interval, nil, nil), MockHost: sources.NewMockHost(p.store.mock)}
		p.upstreams[up.ID] = &endpoint{up, address, sources.NewRequester(d, opts)}
	}
}

type fetchArgs struct {
	Upstream string            `json:"upstream"`
	Method   string            `json:"method,omitempty"`
	Path     string            `json:"path"`
	Query    map[string]string `json:"query"`
	Accept   string            `json:"accept"`
}

type fetchResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    []byte            `json:"body_base64"`
}

func (p *Source) fetch(ctx context.Context, b []byte) []byte {
	var f fetchArgs
	if !strict(b, &f) || (f.Method != "" && f.Method != "GET") || !safePath(f.Path) || strings.Contains(strings.Split(f.Path, "/")[0], ":") || !httpguts.ValidHeaderFieldValue(f.Accept) || len(f.Accept) > 256 {
		return []byte(`{"error":"invalid_request"}`)
	}
	ep := p.upstreams[f.Upstream]
	if ep == nil {
		return []byte(`{"error":"unknown_upstream"}`)
	}
	if f.Accept == "" {
		f.Accept = "application/json"
	}
	res, err := p.get(ctx, ep, f.Path, f.Query, f.Accept)
	if err != nil {
		return []byte(`{"error":"upstream_unavailable"}`)
	}
	out, _ := json.Marshal(res)
	return out
}

func (p *Source) get(ctx context.Context, ep *endpoint, path string, query map[string]string, accept string) (fetchResponse, error) {
	address, err := url.Parse(strings.TrimRight(ep.address, "/") + "/" + strings.TrimLeft(path, "/"))
	if err != nil {
		return fetchResponse{}, err
	}
	q := url.Values{}
	for k, v := range query {
		q.Set(k, v)
	}
	for _, k := range p.Manifest.Keys {
		if k.Upstream == ep.ID && k.Inject.In == "query" {
			q.Del(k.Inject.Name)
		}
	}
	address.RawQuery = q.Encode()
	// 查询缓存之前也检查当前密钥，防止上游把密钥回显给插件。
	var keys map[string]string
	readKeys := func(ctx context.Context) error {
		if len(p.Manifest.Keys) > 0 {
			var err error
			keys, err = p.store.keys(ctx, "plugin:"+p.Manifest.ID)
			return err
		}
		return nil
	}
	if err = readKeys(ctx); err != nil {
		return fetchResponse{}, err
	}
	prepare := func(ctx context.Context, u *url.URL) (map[string]string, error) {
		if err := readKeys(ctx); err != nil {
			return nil, err
		}
		h := map[string]string{}
		q := u.Query()
		for _, k := range p.Manifest.Keys {
			if k.Upstream != ep.ID {
				continue
			}
			if k.Inject.In == "query" {
				q.Del(k.Inject.Name)
			}
			v := keys[k.Name]
			if v == "" {
				continue
			}
			if k.Inject.In == "header" {
				h[k.Inject.Name] = v
			} else {
				q.Set(k.Inject.Name, v)
			}
		}
		u.RawQuery = q.Encode()
		return h, nil
	}
	allow := func(status int, typ string, b []byte) bool {
		for _, v := range keys {
			if v != "" && (strings.Contains(typ, v) || bytes.Contains(b, []byte(v))) {
				return false
			}
		}
		return true
	}
	status, typ, b, err := ep.requester.GetUpstream(ctx, address.String(), accept, prepare, allow)
	return fetchResponse{status, map[string]string{"content-type": typ}, b}, err
}

func (p *Source) image(ctx context.Context, raw string) ([]byte, error) {
	p.store.mu.RLock()
	defer p.store.mu.RUnlock()
	if !p.current() {
		return nil, sources.ErrNotFound
	}
	u, ok := webURL(raw)
	if !ok || !safePath(u.EscapedPath()) {
		return nil, sources.ErrUnavailable
	}
	// 地址先匹配声明的原始基础地址，再应用 mock 替换。重叠的前缀取最长者。
	var selected *endpoint
	var path string
	longest := -1
	for _, ep := range p.upstreams {
		base, _ := url.Parse(ep.BaseURL)
		prefix := strings.TrimRight(base.EscapedPath(), "/")
		if strings.EqualFold(u.Host, base.Host) && u.Scheme == base.Scheme && (u.EscapedPath() == prefix || strings.HasPrefix(u.EscapedPath(), prefix+"/")) && len(prefix) > longest {
			selected, path, longest = ep, strings.TrimPrefix(u.EscapedPath(), prefix), len(prefix)
		}
	}
	if selected == nil {
		return nil, sources.ErrUnavailable
	}
	query := map[string]string{}
	for k, v := range u.Query() {
		if len(v) != 1 {
			return nil, errors.New("图片地址含重复查询参数")
		}
		query[k] = v[0]
	}
	res, err := p.get(ctx, selected, path, query, "image/*")
	if err != nil {
		return nil, err
	}
	if res.Status == 404 {
		return nil, sources.ErrNotFound
	}
	if res.Status != 200 {
		return nil, sources.ErrTryLater
	}
	return res.Body, nil
}
