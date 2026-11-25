package sources

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
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
	Keys                   func(context.Context) (map[string]string, error)
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
	wikimedia     bool
	language      string
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
	binaryCache             bool
	acceptResponse          func(int, string, []byte) bool
	prepare                 func(context.Context, *url.URL) (map[string]string, error)
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
	return r.GetAuthenticatedJSON(ctx, address, nil, "", decode)
}

// GetAuthenticatedJSON 的 address 只含公开参数，prepare 在每次真正出站前为本次请求填入密钥。
func (r *Requester) GetAuthenticatedJSON(ctx context.Context, address string, prepare func(context.Context, *url.URL) (map[string]string, error), keyName string, decode func([]byte) (empty, valid bool)) error {
	spec := requestSpec{method: http.MethodGet, address: address, accept: "application/json", limit: jsonLimit(ctx), timeout: 20 * time.Second, prepare: prepare}
	spec.decode = func(status int, body []byte) (bool, bool, error) {
		if keyName != "" && (status == http.StatusUnauthorized || status == http.StatusForbidden) {
			return false, false, &InvalidKeyError{Name: keyName}
		}
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

// GetUpstream 复用外部源的排队和缓存策略。密钥在真正出站前才装入请求。
func (r *Requester) GetUpstream(ctx context.Context, address, accept string, prepare func(context.Context, *url.URL) (map[string]string, error), allow func(int, string, []byte) bool) (int, string, []byte, error) {
	spec := requestSpec{method: http.MethodGet, address: address, accept: accept, limit: 4 << 20, timeout: 10 * time.Second, prepare: prepare, binaryCache: true, acceptResponse: allow}
	res, err := r.do(ctx, spec)
	return res.status, res.contentType, res.body, err
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
	if r.wikimedia {
		request.Header.Set("Accept-Encoding", "gzip")
	}
	if r.language != "" {
		request.Header.Set("Accept-Language", r.language)
	}
	keyInput := spec.method + " " + request.URL.String()
	if spec.method == http.MethodPost {
		keyInput += "\x00" + spec.cacheInput
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("Content-Encoding", "gzip")
	}
	if spec.binaryCache {
		keyInput += "\x00" + spec.accept
	}
	key := cacheKey(keyInput)
	queue, queued := ctx.Value(queueKey{}).(queueRequest)
	e, hit, failed := r.cache.lookup(ctx, key, background && !queued, spec.decode != nil || spec.binaryCache)
	if failed {
		r.hit(ctx)
		return response{}, ErrTryLater
	}
	if hit {
		if spec.binaryCache {
			body, err := base64.StdEncoding.DecodeString(e.Body)
			if err == nil && int64(len(body)) <= spec.limit && spec.acceptResponse(e.Status, e.ContentType, body) {
				r.hit(ctx)
				return response{status: e.Status, contentType: e.ContentType, body: body}, nil
			}
			return response{}, failure
		}
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
		res, err := r.attempt(ctx, c, background, request, attempt, spec, key, queue, failure)
		if err != nil || !res.retry {
			return res, err
		}
		if attempt >= 5 {
			r.cache.fail(key)
			return response{}, ErrTryLater
		}
		delay := retryDelay(res.retryAfter, r.cache.now(), attempt)
		if r.wikimedia && (res.status == http.StatusTooManyRequests || res.status == http.StatusServiceUnavailable) {
			if requested, ok := retryAfterDelay(res.retryAfter, r.cache.now()); ok {
				delay = requested
			} else {
				delay = max(delay, 5*time.Second)
			}
		}
		select {
		case <-ctx.Done():
			return response{}, ctx.Err()
		case <-r.after(delay):
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
	if requested, ok := retryAfterDelay(header, now); ok {
		return min(maximum, requested)
	}
	base := time.Second << attempt
	return min(maximum, base+time.Duration(rand.Int64N(int64(base)/2)))
}

func retryAfterDelay(header string, now time.Time) (time.Duration, bool) {
	header = strings.TrimSpace(header)
	if n, err := strconv.ParseUint(header, 10, 64); err == nil {
		return time.Duration(min(n, uint64(math.MaxInt64/int64(time.Second)))) * time.Second, true
	}
	date, err := http.ParseTime(header)
	if err != nil {
		return 0, false
	}
	return max(0, date.Sub(now)), true
}

func (r *Requester) attempt(ctx context.Context, c caller, background bool, request *http.Request, attempt int, spec requestSpec, key string, queue queueRequest, failure error) (response, error) {
	source := r.cache.source
	if queue.policy != nil {
		if pause := queue.policy.Pause(source); pause != nil {
			return response{}, pause
		}
	}
	if err := r.limiter.acquire(ctx, background); err != nil {
		return response{}, err
	}
	held := true
	defer func() {
		if held {
			r.limiter.release(false)
		}
	}()
	if queue.policy != nil {
		// 等待许可期间，先前的请求可能刚让来源进入冷却。
		if pause := queue.policy.Pause(source); pause != nil {
			return response{}, pause
		}
	}
	start := func() {
		if r.wikimedia || queue.policy != nil {
			r.limiter.startSerial()
		} else {
			r.limiter.release(true)
			held = false
		}
	}
	res, err := r.fetchAttempt(ctx, c, request, attempt, spec, start)
	if err != nil {
		return response{}, err
	}
	if res.denied != nil {
		if r.maxRedirects > 0 {
			return response{}, fmt.Errorf("%w: %w", failure, res.denied)
		}
		return response{}, failure
	}
	if err := ctx.Err(); err != nil && (queue.policy == nil || res.body == nil) {
		return response{}, err
	}
	throttled := res.status == http.StatusTooManyRequests || res.status == http.StatusServiceUnavailable
	network := res.retry && res.body == nil && !throttled && attempt == 1
	if queue.policy != nil && throttled {
		return response{}, r.pauseQueue(queue, "rate_limited", res.retryAfter, false)
	}
	result, err := r.decodeResponse(ctx, spec, res, key, background, failure)
	if queue.policy == nil {
		return result, err
	}
	if (err == nil && !result.retry) || errors.Is(err, ErrNotFound) {
		if res.status >= 200 && res.status < 300 || res.status == http.StatusNotFound {
			r.limiter.setLevel(queue.policy.recovered(source))
			if res.status != http.StatusNotFound && queue.success != nil {
				queue.success(source)
			}
		}
		if ctx.Err() != nil {
			return response{}, ctx.Err()
		}
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return response{}, err
	}
	if result.retry || errors.Is(err, ErrTryLater) || errors.Is(err, ErrUnavailable) {
		if pause := r.pauseQueue(queue, "source_unavailable", res.retryAfter, network); pause != nil {
			return response{}, pause
		}
	}
	return result, err
}

func (r *Requester) pauseQueue(queue queueRequest, reason, retryAfter string, network bool) *QueuePause {
	pause, level := queue.policy.failed(r.cache.source, reason, retryAfter, network)
	r.limiter.setLevel(level)
	return pause
}

// decodeResponse 只缓存来源适配器已确认有效的响应，重试交给外层调度。
func (r *Requester) decodeResponse(ctx context.Context, spec requestSpec, res response, key string, background bool, failure error) (response, error) {
	if res.status != 0 && !(res.body == nil && res.retry) {
		if spec.binaryCache && !res.retry {
			if res.body == nil || !spec.acceptResponse(res.status, res.contentType, res.body) {
				return response{}, failure
			}
			if res.status == 200 || res.status == 404 {
				r.cache.storeContent(ctx, key, res.status, []byte(base64.StdEncoding.EncodeToString(res.body)), res.contentType, res.status == 404, background)
			}
			return res, nil
		}
		empty, retry, err := spec.interpret(res)
		if ctx.Err() != nil && ctx.Value(queueKey{}) == nil {
			return response{}, ctx.Err()
		}
		if err == nil || errors.Is(err, ErrNotFound) {
			if spec.decode != nil && res.body != nil {
				body := res.body
				if spec.prepare != nil && res.status == http.StatusNotFound {
					body = nil
				}
				r.cache.store(ctx, key, res.status, body, empty, background)
			}
			if err != nil {
				return response{}, err
			}
			res.retry = false
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
	return res, nil
}

func (r *Requester) fetchAttempt(ctx context.Context, c caller, request *http.Request, attempt int, spec requestSpec, start func()) (response, error) {
	baseClient, err := r.newClient(r.mockHost.EgressOptions(egress.Options{Policy: egress.PublicOnly, Timeout: spec.timeout, MaxRedirects: r.maxRedirects}))
	if err != nil {
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
		for _, name := range []string{"Accept", "User-Agent", "Accept-Encoding", "Accept-Language"} {
			if value := request.Header.Get(name); value != "" {
				next.Header.Set(name, value)
			}
		}
		permitErr = c.check(next.Context())
		return permitErr
	}
	defer client.CloseIdleConnections()
	if err := c.check(ctx); err != nil {
		return response{}, err
	}
	requestCtx, cancel := context.WithTimeout(ctx, spec.timeout)
	defer cancel()
	req := request.Clone(requestCtx)
	if spec.prepare != nil {
		headers, err := spec.prepare(req.Context(), req.URL)
		if err != nil {
			return response{}, err
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
	}
	if spec.form != nil {
		if err := prepareForm(req, spec.form); err != nil {
			return response{}, err
		}
	}
	started := time.Now()
	start()
	res := fetch(&client, req, spec.limit, spec.binaryCache)
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

func fetch(client *http.Client, req *http.Request, limit int64, allStatuses bool) response {
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
	if allStatuses || req.Method == http.MethodPost || resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
		if resp.ContentLength > limit {
			return res
		}
		var reader io.Reader = resp.Body
		// 显式发送 Accept-Encoding 后，net/http 不代为解压。上限同时约束解压后的实际大小。
		if req.Header.Get("Accept-Encoding") == "gzip" && strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
			compressed, err := gzip.NewReader(resp.Body)
			if err != nil {
				return res
			}
			defer compressed.Close()
			reader = compressed
		}
		body, err := io.ReadAll(io.LimitReader(reader, min(limit, math.MaxInt64-1)+1))
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
