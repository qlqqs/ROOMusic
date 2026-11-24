package sources

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/buildinfo"
	"github.com/qlqq/roomusic/backend/internal/platform/egress"
)

type ClientFactory func(egress.Options) (*http.Client, error)

type Options struct {
	MockHost               MockHost
	MaxRedirects           int
	AllowRedirect          func(*url.URL) bool
	DataDirectory, BaseURL string
	Logger                 *slog.Logger
	NewClient              ClientFactory
	Limiter                *Limiter
	Now                    func() time.Time
	After                  func(time.Duration) <-chan time.Time
}

type Requester struct {
	mockHost      MockHost
	maxRedirects  int
	allowRedirect func(*url.URL) bool
	cache         cache
	limiter       *Limiter
	newClient     ClientFactory
	after         func(time.Duration) <-chan time.Time
}

func NewRequester(d Declaration, opts Options) *Requester {
	if opts.Limiter == nil {
		opts.Limiter = DefaultLimiter(d)
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.After == nil {
		opts.After = time.After
	}
	if opts.NewClient == nil {
		opts.NewClient = egress.NewClient
	}
	dir := ""
	if opts.DataDirectory != "" {
		dir = filepath.Join(opts.DataDirectory, "sources", "cache", d.ID)
	}
	return &Requester{mockHost: opts.MockHost,
		cache:   cache{dir: dir, source: d.ID, logger: opts.Logger, now: opts.Now, failures: make(map[string]time.Time)},
		limiter: opts.Limiter, newClient: opts.NewClient, after: opts.After,
		maxRedirects: opts.MaxRedirects, allowRedirect: opts.AllowRedirect,
	}
}

func (r *Requester) Limiter() *Limiter { return r.limiter }

func (r *Requester) StartCacheCleanup(ctx context.Context) {
	if r.cache.dir == "" {
		return
	}
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			r.cache.clean(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func UserAgent() string {
	version := buildinfo.Current().Version
	if version == "" {
		version = "dev"
	}
	return "ROOMusic/" + version + " ( " + buildinfo.ProjectURL + " )"
}

func (c caller) check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.permit != nil {
		return c.permit(ctx)
	}
	return nil
}

type response struct {
	denied      *egress.Error
	contentType string
	status      int
	body        []byte
	retry       bool
	retryAfter  string
}

type requestSpec struct {
	method, address, accept string
	cacheInput              string
	limit                   int64
	timeout                 time.Duration
	form                    func(context.Context) (url.Values, error)
	decode                  func(int, []byte) (empty, retry bool, err error)
}

func jsonLimit(ctx context.Context) int64 {
	if _, background := ctx.Value(backgroundKey{}).(caller); background {
		return 8 << 20
	}
	return 2 << 20
}

// GetJSON 只在 decode 确认结构可用后缓存响应。decode 的两个返回值依次表示空结果和结构有效。
func (r *Requester) GetJSON(ctx context.Context, address string, decode func([]byte) (empty, valid bool)) error {
	spec := requestSpec{method: http.MethodGet, address: address, accept: "application/json", limit: jsonLimit(ctx), timeout: 20 * time.Second}
	spec.decode = func(status int, body []byte) (bool, bool, error) {
		if status == http.StatusNotFound {
			return true, false, ErrNotFound
		}
		if status == http.StatusOK && body != nil {
			if empty, valid := decode(body); valid {
				return empty, false, nil
			}
		}
		return false, false, ErrUnavailable
	}
	_, err := r.do(ctx, spec)
	return err
}

// PostForm 在每次出站前重新取表单并压缩。cacheInput 只含决定查询结果的参数。
// decode 接收包括非 200 在内的 HTTP 响应，区分应重试的失败、终止错误和可缓存的结果。
func (r *Requester) PostForm(ctx context.Context, address, cacheInput string, form func(context.Context) (url.Values, error), decode func(int, []byte) (empty, retry bool, err error)) error {
	_, err := r.do(ctx, requestSpec{method: http.MethodPost, address: address, accept: "application/json", cacheInput: cacheInput, limit: jsonLimit(ctx), timeout: 20 * time.Second, form: form, decode: decode})
	return err
}

// GetBytes 读取大小受限的二进制响应，只使用后台失败记忆。
func (r *Requester) GetBytes(ctx context.Context, address, accept string, maxBytes int64) ([]byte, string, error) {
	res, err := r.do(ctx, requestSpec{method: http.MethodGet, address: address, accept: accept, limit: maxBytes, timeout: 60 * time.Second})
	return res.body, res.contentType, err
}

func (r *Requester) do(ctx context.Context, spec requestSpec) (response, error) {
	c, background := ctx.Value(backgroundKey{}).(caller)
	if err := c.check(ctx); err != nil {
		return response{}, err
	}
	failure := ErrUnavailable
	if background {
		failure = ErrTryLater
	}
	request, err := http.NewRequestWithContext(ctx, spec.method, spec.address, nil)
	if err != nil {
		return response{}, failure
	}
	request.URL.RawQuery = request.URL.Query().Encode()
	request.URL.Fragment = ""
	request.URL.Host = strings.ToLower(request.URL.Host)
	request.Header.Set("Accept", spec.accept)
	request.Header.Set("User-Agent", UserAgent())
	keyInput := spec.method + " " + request.URL.String()
	if spec.method == http.MethodPost {
		keyInput += "\x00" + spec.cacheInput
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("Content-Encoding", "gzip")
	}
	key := cacheKey(keyInput)
	e, hit, failed := r.cache.lookup(ctx, key, background, spec.decode != nil)
	if failed {
		r.hit(ctx)
		return response{}, ErrTryLater
	}
	if hit {
		if e.Status == http.StatusNotFound && spec.method == http.MethodGet {
			r.hit(ctx)
			return response{}, ErrNotFound
		}
		if int64(len(e.Body)) > spec.limit {
			return response{}, failure
		}
		_, _, err := spec.decode(e.Status, []byte(e.Body))
		if err == nil || errors.Is(err, ErrNotFound) {
			r.hit(ctx)
			return response{}, err
		}
		r.cache.warning(ctx)
	}
	for attempt := 1; ; attempt++ {
		res, err := r.attempt(ctx, c, background, request, attempt, spec)
		if err != nil {
			return response{}, err
		}
		if r.maxRedirects > 0 && res.denied != nil {
			return response{}, fmt.Errorf("%w: %w", failure, res.denied)
		}
		if err := ctx.Err(); err != nil {
			return response{}, err
		}
		if res.status != 0 && !(res.body == nil && res.retry) {
			empty, retry, err := spec.interpret(res)
			if ctx.Err() != nil {
				return response{}, ctx.Err()
			}
			if err == nil || errors.Is(err, ErrNotFound) {
				if spec.decode != nil && res.body != nil {
					r.cache.store(ctx, key, res.status, res.body, empty, background)
				}
				if err != nil {
					return response{}, err
				}
				return res, nil
			}
			if !errors.Is(err, ErrUnavailable) && !retry {
				return response{}, err
			}
			res.retry = res.retry || retry
		}
		if !background || !res.retry {
			return response{}, failure
		}
		if attempt >= 5 {
			r.cache.fail(key)
			return response{}, ErrTryLater
		}
		select {
		case <-ctx.Done():
			return response{}, ctx.Err()
		case <-r.after(retryDelay(res.retryAfter, r.cache.now(), attempt)):
		}
	}
}

func (spec requestSpec) interpret(res response) (empty, retry bool, err error) {
	if spec.decode != nil {
		return spec.decode(res.status, res.body)
	}
	if res.status == http.StatusOK && res.body != nil {
		return false, false, nil
	}
	if res.status == http.StatusNotFound {
		return true, false, ErrNotFound
	}
	return false, false, ErrUnavailable
}

func (r *Requester) hit(ctx context.Context) {
	if r.cache.logger != nil {
		r.cache.logger.InfoContext(ctx, "命中外部源缓存", "event", "sources.cache.hit", "module", "sources", "message", "命中外部源缓存", "source", r.cache.source)
	}
}

func retryDelay(header string, now time.Time, attempt int) time.Duration {
	const maximum = 60 * time.Second
	header = strings.TrimSpace(header)
	if n, err := strconv.ParseUint(header, 10, 64); err == nil {
		return time.Duration(min(n, 60)) * time.Second
	}
	if date, err := http.ParseTime(header); err == nil {
		return min(maximum, max(0, date.Sub(now)))
	}
	base := time.Second << attempt
	return min(maximum, base+time.Duration(rand.Int64N(int64(base)/2)))
}

func (r *Requester) attempt(ctx context.Context, c caller, background bool, request *http.Request, attempt int, spec requestSpec) (response, error) {
	if err := r.limiter.acquire(ctx, background); err != nil {
		return response{}, err
	}
	baseClient, err := r.newClient(r.mockHost.EgressOptions(egress.Options{Policy: egress.PublicOnly, Timeout: spec.timeout, MaxRedirects: r.maxRedirects}))
	if err != nil {
		r.limiter.release(false)
		return response{}, nil
	}
	client := *baseClient
	client.Jar = nil
	// 跳转仍占本次许可，后台在连接下一跳前再次核对源开关。
	var permitErr error
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if baseClient.CheckRedirect != nil {
			if err := baseClient.CheckRedirect(next, via); err != nil {
				return err
			}
		}
		if err := r.checkRedirect(next.URL, via); err != nil {
			return err
		}
		next.Header = make(http.Header)
		next.Header.Set("Accept", request.Header.Get("Accept"))
		next.Header.Set("User-Agent", request.Header.Get("User-Agent"))
		permitErr = c.check(next.Context())
		return permitErr
	}
	defer client.CloseIdleConnections()
	if err := c.check(ctx); err != nil {
		r.limiter.release(false)
		return response{}, err
	}
	requestCtx, cancel := context.WithTimeout(ctx, spec.timeout)
	defer cancel()
	req := request.WithContext(requestCtx)
	if spec.form != nil {
		if err := prepareForm(req, spec.form); err != nil {
			r.limiter.release(false)
			return response{}, err
		}
	}
	started := time.Now()
	r.limiter.release(true)
	res := fetch(&client, req, spec.limit)
	if logger := r.cache.logger; logger != nil {
		attrs := []any{"event", "sources.request.finished", "module", "sources", "message", "外部源请求结束", "source", r.cache.source, "domain", req.URL.Hostname(), "duration_ms", time.Since(started).Milliseconds(), "attempt", attempt}
		if res.status != 0 {
			attrs = append(attrs, "status", res.status)
		} else {
			attrs = append(attrs, "error_code", "request_failed")
		}
		logger.InfoContext(ctx, "外部源请求结束", attrs...)
	}
	if permitErr != nil {
		return response{}, permitErr
	}
	return res, nil
}

func prepareForm(req *http.Request, form func(context.Context) (url.Values, error)) error {
	values, err := form(req.Context())
	if err != nil {
		return err
	}
	var body bytes.Buffer
	writer := gzip.NewWriter(&body)
	if _, err := io.WriteString(writer, values.Encode()); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	req.Body = io.NopCloser(&body)
	req.ContentLength = int64(body.Len())
	return nil
}

func (r *Requester) checkRedirect(target *url.URL, via []*http.Request) error {
	if len(via) > r.maxRedirects {
		return &egress.Error{Code: egress.CodeTooManyRedirects}
	}
	fromHTTP := via[len(via)-1].URL.Scheme == "http"
	if target.Scheme != "https" && !(fromHTTP && target.Scheme == "http") {
		return &egress.Error{Code: egress.CodeSchemeNotAllowed}
	}
	if target.User != nil || (target.Port() != "" && target.Port() != "443" && !r.mockHost.Matches(target)) || r.allowRedirect == nil || !r.allowRedirect(target) {
		return &egress.Error{Code: egress.CodeAddressNotAllowed}
	}
	return nil
}

func fetch(client *http.Client, req *http.Request, limit int64) response {
	resp, err := client.Do(req)
	if err != nil {
		// 地址策略与禁止重定向产生的错误无需重试。
		var denied *egress.Error
		if errors.Is(err, egress.ErrDenied) {
			errors.As(err, &denied)
			return response{denied: denied}
		}
		return response{retry: true}
	}
	defer resp.Body.Close()
	res := response{status: resp.StatusCode, contentType: resp.Header.Get("Content-Type"), retryAfter: resp.Header.Get("Retry-After")}
	res.retry = resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable
	if req.Method == http.MethodPost || resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
		if resp.ContentLength > limit {
			return res
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, min(limit, math.MaxInt64-1)+1))
		if int64(len(body)) > limit {
			return res
		}
		if err == nil {
			res.body = body
		} else if resp.StatusCode == http.StatusOK {
			res.retry = true
		}
	}
	return res
}
