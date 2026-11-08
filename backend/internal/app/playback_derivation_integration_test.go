package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	libpkg "github.com/qlqq/roomusic/backend/internal/library"
	"github.com/qlqq/roomusic/backend/internal/playback"
	"github.com/qlqq/roomusic/backend/internal/playback/derivation"
)

// fakeDerivation 是不需要 ffmpeg 的 playback.Derivation：测试决定 Await 是否阻塞或失败，以及缓存里有哪些派生文件。
// 键由内容指纹决定（与真实实现一样不含曲目 ID），所以预检成功之后同一首曲目的音频请求命中缓存。
type fakeDerivation struct {
	directory string

	mu       sync.Mutex
	files    map[string][]byte
	requests []derivation.Request
	// conversions 是缓存未命中的次数，也就是需要真正转换的请求数；命中的预检不增加它。
	conversions int
	// await 在派生尚未缓存时被调用：返回 nil 表示转换成功（随后内容进入缓存），返回错误表示转换失败；nil 表示立即成功。
	await func(ctx context.Context, request derivation.Request) error
	// content 生成派生文件的内容；nil 时是以 fLaC 开头的 6000 多字节，比源文件小，用来区分“交付了派生文件”还是“交付了原文件”。
	content func(key string) []byte
}

func newFakeDerivation(t *testing.T) *fakeDerivation {
	t.Helper()
	return &fakeDerivation{directory: t.TempDir(), files: map[string][]byte{}}
}

func (fake *fakeDerivation) Supports(decoder string) bool {
	switch decoder {
	case "alac", "ape", "wavpack", "pcm_s16be":
		return true
	default:
		return false
	}
}

func (fake *fakeDerivation) Key(version derivation.SourceVersion) string {
	return "fake-" + version.ContentFingerprint
}

func (fake *fakeDerivation) derivedContent(key string) []byte {
	if fake.content != nil {
		return fake.content(key)
	}
	return flacBytes(6000 + len(key))
}

func (fake *fakeDerivation) Await(ctx context.Context, request derivation.Request) (derivation.Entry, error) {
	key := fake.Key(request.Source.Version)
	fake.mu.Lock()
	fake.requests = append(fake.requests, request)
	content, cached := fake.files[key]
	await := fake.await
	fake.mu.Unlock()
	if !cached {
		fake.mu.Lock()
		fake.conversions++
		fake.mu.Unlock()
		if await != nil {
			if err := await(ctx, request); err != nil {
				return derivation.Entry{}, err
			}
		}
		content = fake.derivedContent(key)
		fake.mu.Lock()
		fake.files[key] = content
		fake.mu.Unlock()
	}
	return derivation.Entry{Key: key, Size: int64(len(content))}, nil
}

func (fake *fakeDerivation) OpenEntry(key string) (*os.File, int64, error) {
	fake.mu.Lock()
	content, cached := fake.files[key]
	fake.mu.Unlock()
	if !cached {
		return nil, 0, derivation.ErrNotCached
	}
	path := filepath.Join(fake.directory, key)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return nil, 0, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	return file, int64(len(content)), nil
}

func (fake *fakeDerivation) requested() []derivation.Request {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return append([]derivation.Request(nil), fake.requests...)
}

func (fake *fakeDerivation) conversionCount() int {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return fake.conversions
}

func (fake *fakeDerivation) cachedContent(key string) []byte {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return fake.files[key]
}

// blockUntil 让转换停在 Await 里直到 release 关闭或调用方的 context 结束；entered 在第一个请求进入时关闭。
func (fake *fakeDerivation) blockUntil(release <-chan struct{}) (entered <-chan struct{}) {
	signal := make(chan struct{})
	var once sync.Once
	fake.await = func(ctx context.Context, _ derivation.Request) error {
		once.Do(func() { close(signal) })
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return signal
}

// derivationPreflightWait 把预检等待缩短到可以快速验证 202；生产值是 playback.DefaultPreflightWait（20 秒）。
const derivationPreflightWait = 150 * time.Millisecond

func newDerivationApplication(t *testing.T, fake *fakeDerivation, applied ...Option) *integrationTestApplication {
	t.Helper()
	options := append([]Option{WithDerivation(fake), WithPlaybackPreflightWait(derivationPreflightWait)}, applied...)
	return newIntegrationTestApplication(t, options...)
}

// alacSpec 是登记为 ALAC 的曲目：容器是 mp4，音频参数与扫描记录一致。
func alacSpec(relative string, content []byte) playbackTrackSpec {
	return playbackTrackSpec{
		relative: relative, content: content, sourceKind: "m4a", codec: "alac",
		bitDepth: 16, sampleRate: 44100, channels: 2,
	}
}

type derivationPreflightBody struct {
	TrackID        string `json:"track_id"`
	Status         string `json:"status"`
	Representation *struct {
		Kind      string  `json:"kind"`
		URL       string  `json:"url"`
		MIMEType  string  `json:"mime_type"`
		Codecs    *string `json:"codecs"`
		SizeBytes int64   `json:"size_bytes"`
	} `json:"representation"`
}

func preflightPath(trackID string) string { return "/api/v1/tracks/" + trackID + "/playback" }

func decodePreflight(t *testing.T, response *httptest.ResponseRecorder) derivationPreflightBody {
	t.Helper()
	var decoded derivationPreflightBody
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode preflight: %v: %s", err, response.Body.String())
	}
	return decoded
}

// derivedETag 是派生表示的强校验值：SHA-256("derived|"+曲目+"|"+缓存键) 的前 32 位十六进制，带引号。
func derivedETag(trackID, key string) string {
	digest := sha256.Sum256([]byte("derived|" + trackID + "|" + key))
	return `"` + hex.EncodeToString(digest[:])[:32] + `"`
}

func TestPostgreSQLPlaybackFormatsAddTheDerivedFormatWhenDerivationIsEnabled(t *testing.T) {
	application := newDerivationApplication(t, newFakeDerivation(t))
	user := application.createUser(t, "user")
	response := application.request(t, http.MethodGet, "/api/v1/playback/formats", nil, &user, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("formats: HTTP %d: %s", response.Code, response.Body.String())
	}
	var decoded struct {
		Items []struct {
			SourceKind string  `json:"source_kind"`
			Codec      string  `json:"codec"`
			Delivery   string  `json:"delivery"`
			MIMEType   string  `json:"mime_type"`
			Codecs     *string `json:"codecs"`
		} `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode formats: %v", err)
	}
	if len(decoded.Items) != 11 {
		t.Fatalf("formats = %s, want the six original formats plus five derived", response.Body.String())
	}
	for _, item := range decoded.Items[:6] {
		if item.Delivery != "original" {
			t.Errorf("%s/%s delivery = %q, want original", item.SourceKind, item.Codec, item.Delivery)
		}
	}
	derived := decoded.Items[6]
	if derived.SourceKind != "m4a" || derived.Codec != "alac" || derived.Delivery != "derived" || derived.MIMEType != "audio/flac" || derived.Codecs != nil {
		t.Fatalf("derived format = %+v, want m4a/alac delivered as audio/flac", derived)
	}
}

func TestPostgreSQLPlaybackPreflightWaitsForTheDerivationAndAudioServesTheCachedFile(t *testing.T) {
	fake := newFakeDerivation(t)
	release := make(chan struct{})
	entered := fake.blockUntil(release)
	application := newDerivationApplication(t, fake)
	library := createPlaybackLibrary(t, application)
	original := mp4Bytes(4096)
	trackID := library.addTrack(t, application, alacSpec("专辑/01 曲目.m4a", original))
	user := application.createUser(t, "user")

	// 预检等待派生：等待时间用完仍在进行时得到 202，任务不因此取消；请求携带成员与曲目，源文件版本带着扫描记录的音频参数。
	response := application.request(t, http.MethodGet, preflightPath(trackID), nil, &user, nil)
	if response.Code != http.StatusAccepted || response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("waiting preflight: HTTP %d, Cache-Control %q, Content-Type %q: %s", response.Code, response.Header().Get("Cache-Control"), response.Header().Get("Content-Type"), response.Body.String())
	}
	if preparing := decodePreflight(t, response); preparing.TrackID != trackID || preparing.Status != "preparing" || preparing.Representation != nil {
		t.Fatalf("202 body = %+v, want track %s preparing", preparing, trackID)
	}
	select {
	case <-entered:
	default:
		t.Fatal("the preflight never reached the derivation")
	}
	first := fake.requested()
	if len(first) != 1 || first[0].User != user.id || first[0].TrackID != trackID {
		t.Fatalf("derivation requests = %+v, want one for the member and track", first)
	}
	version := first[0].Source.Version
	if version.BitDepth != 16 || version.SampleRate != 44100 || version.Channels != 2 || version.SourceKind != "m4a" || version.Codec != "alac" ||
		version.Size != int64(len(original)) || version.ContentFingerprint != "fingerprint-"+trackID {
		t.Fatalf("source version = %+v", version)
	}

	// 音频请求从不启动转换：派生文件还没有时是 409，请求数不变。
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		notReady := application.request(t, method, audioPath(trackID), nil, &user, map[string]string{"Range": "bytes=0-9"})
		if method == http.MethodGet {
			requireAPIError(t, notReady, http.StatusConflict, "derivation_not_ready")
		} else if notReady.Code != http.StatusConflict {
			t.Fatalf("HEAD audio before the derivation is ready = HTTP %d, want 409", notReady.Code)
		}
		requireNoPathLeak(t, application, notReady)
	}
	if got := len(fake.requested()); got != 1 {
		t.Fatalf("an audio request started a conversion: %d derivation requests", got)
	}

	// 转换完成后再次预检得到派生表示；大小是派生文件的大小。
	close(release)
	response = application.request(t, http.MethodGet, preflightPath(trackID), nil, &user, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("preflight after the derivation finished: HTTP %d: %s", response.Code, response.Body.String())
	}
	ready := decodePreflight(t, response)
	key := fake.Key(version)
	derived := fake.cachedContent(key)
	if ready.TrackID != trackID || ready.Representation == nil || ready.Representation.Kind != "derived" || ready.Representation.URL != audioPath(trackID) ||
		ready.Representation.MIMEType != "audio/flac" || ready.Representation.Codecs != nil || ready.Representation.SizeBytes != int64(len(derived)) {
		t.Fatalf("derived preflight = %+v (derived file is %d bytes)", ready, len(derived))
	}
	requireNoPathLeak(t, application, response)

	// 音频交付的是派生文件，头与强校验值描述它而不是原文件。
	etag := derivedETag(trackID, key)
	full := application.request(t, http.MethodGet, audioPath(trackID), nil, &user, nil)
	if full.Code != http.StatusOK || !bytes.Equal(full.Body.Bytes(), derived) || bytes.Equal(full.Body.Bytes(), original) {
		t.Fatalf("derived audio: HTTP %d, %d bytes (want the %d-byte derived file)", full.Code, full.Body.Len(), len(derived))
	}
	for name, want := range map[string]string{
		"Content-Type":           "audio/flac",
		"Cache-Control":          "private, no-store",
		"Accept-Ranges":          "bytes",
		"X-Content-Type-Options": "nosniff",
		"Content-Length":         strconv.Itoa(len(derived)),
		"ETag":                   etag,
	} {
		if got := full.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if full.Header().Get("ETag") == `"`+fmt.Sprintf("%x", sha256.Sum256(original))[:32]+`"` {
		t.Error("the derived ETag equals a digest of the original bytes")
	}
	if head := application.request(t, http.MethodHead, audioPath(trackID), nil, &user, nil); head.Code != http.StatusOK || head.Body.Len() != 0 ||
		head.Header().Get("ETag") != etag || head.Header().Get("Content-Length") != strconv.Itoa(len(derived)) {
		t.Fatalf("HEAD: HTTP %d, %d body bytes, ETag %q, Content-Length %q", head.Code, head.Body.Len(), head.Header().Get("ETag"), head.Header().Get("Content-Length"))
	}

	// Range、If-Range、条件请求与 416 的规则与直出相同，但都针对派生文件。
	partial := application.request(t, http.MethodGet, audioPath(trackID), nil, &user, map[string]string{"Range": "bytes=10-19"})
	if partial.Code != http.StatusPartialContent || !bytes.Equal(partial.Body.Bytes(), derived[10:20]) ||
		partial.Header().Get("Content-Range") != fmt.Sprintf("bytes 10-19/%d", len(derived)) || partial.Header().Get("ETag") != etag {
		t.Fatalf("Range: HTTP %d, %d bytes, Content-Range %q", partial.Code, partial.Body.Len(), partial.Header().Get("Content-Range"))
	}
	if response := application.request(t, http.MethodGet, audioPath(trackID), nil, &user, map[string]string{"Range": "bytes=10-19", "If-Range": `"stale"`}); response.Code != http.StatusOK || response.Body.Len() != len(derived) {
		t.Fatalf("stale If-Range: HTTP %d, %d bytes; want the complete derived file", response.Code, response.Body.Len())
	}
	if response := application.request(t, http.MethodGet, audioPath(trackID), nil, &user, map[string]string{"If-None-Match": etag}); response.Code != http.StatusNotModified || response.Body.Len() != 0 {
		t.Fatalf("If-None-Match: HTTP %d, %d body bytes", response.Code, response.Body.Len())
	}
	unsatisfiable := application.request(t, http.MethodGet, audioPath(trackID), nil, &user, map[string]string{"Range": fmt.Sprintf("bytes=%d-", len(derived))})
	requireAPIError(t, unsatisfiable, http.StatusRequestedRangeNotSatisfiable, "range_not_satisfiable")
	if got, want := unsatisfiable.Header().Get("Content-Range"), fmt.Sprintf("bytes */%d", len(derived)); got != want {
		t.Fatalf("416 Content-Range = %q, want %q", got, want)
	}

	// 缓存命中的预检不再启动转换。
	if again := application.request(t, http.MethodGet, preflightPath(trackID), nil, &user, nil); again.Code != http.StatusOK {
		t.Fatalf("second preflight: HTTP %d", again.Code)
	}
	if got := fake.conversionCount(); got != 2 {
		t.Fatalf("conversions = %d, want 2 (the waiting preflight and the one that finished it); a cache hit must not convert again", got)
	}
	// 源文件始终只读。
	if info, err := os.Stat(filepath.Join(library.root, "专辑", "01 曲目.m4a")); err != nil || !info.ModTime().Equal(playbackModified) || info.Size() != int64(len(original)) {
		t.Fatalf("source file changed during derived playback: %v, %v", info, err)
	}
}

func TestPostgreSQLPlaybackDirectFormatsNeverTouchTheDerivation(t *testing.T) {
	fake := newFakeDerivation(t)
	application := newDerivationApplication(t, fake)
	library := createPlaybackLibrary(t, application)
	user := application.createUser(t, "user")
	flac := library.addTrack(t, application, playbackTrackSpec{relative: "a.flac", content: flacBytes(8192)})
	aac := library.addTrack(t, application, playbackTrackSpec{relative: "b.m4a", content: mp4Bytes(6000), sourceKind: "m4a", codec: "aac"})

	for trackID, size := range map[string]int64{flac: 8192, aac: 6000} {
		response := application.request(t, http.MethodGet, preflightPath(trackID), nil, &user, nil)
		body := decodePreflight(t, response)
		if response.Code != http.StatusOK || body.Representation == nil || body.Representation.Kind != "original" || body.Representation.SizeBytes != size {
			t.Fatalf("preflight = HTTP %d %s, want the original representation of %d bytes", response.Code, response.Body.String(), size)
		}
		if audio := application.request(t, http.MethodGet, audioPath(trackID), nil, &user, nil); audio.Code != http.StatusOK || int64(audio.Body.Len()) != size {
			t.Fatalf("audio: HTTP %d, %d bytes, want the %d-byte original", audio.Code, audio.Body.Len(), size)
		}
	}
	if requests := fake.requested(); len(requests) != 0 {
		t.Fatalf("directly playable formats started %d conversions", len(requests))
	}
}

func TestPostgreSQLPlaybackChecksPrecedeAnyConversion(t *testing.T) {
	fake := newFakeDerivation(t)
	application := newDerivationApplication(t, fake)
	library := createPlaybackLibrary(t, application)
	user := application.createUser(t, "user")
	content := mp4Bytes(4096)
	changedSize := int64(len(content) - 1)
	changedTime := playbackModified.Add(time.Second)

	withSpec := func(update func(*playbackTrackSpec)) string {
		spec := alacSpec(fmt.Sprintf("track-%d.m4a", time.Now().UnixNano()), content)
		update(&spec)
		return library.addTrack(t, application, spec)
	}
	gone := withSpec(func(spec *playbackTrackSpec) { spec.skipFile = true })
	sizeChanged := withSpec(func(spec *playbackTrackSpec) { spec.recordedSize = &changedSize })
	timeChanged := withSpec(func(spec *playbackTrackSpec) { spec.recordedModified = &changedTime })
	notMP4 := withSpec(func(spec *playbackTrackSpec) { spec.content = flacBytes(4096) })
	noFingerprint := withSpec(func(spec *playbackTrackSpec) { spec.noFingerprint = true })
	playable := withSpec(func(*playbackTrackSpec) {})

	for _, testCase := range []struct {
		name    string
		trackID string
		status  int
		code    string
	}{
		{"unknown track", "00000000-0000-4000-8000-000000000000", http.StatusNotFound, "not_found"},
		{"file is gone", gone, http.StatusConflict, "source_unavailable"},
		{"size differs from the scan", sizeChanged, http.StatusConflict, "source_changed"},
		{"modification time differs from the scan", timeChanged, http.StatusConflict, "source_changed"},
		{"container differs from the format", notMP4, http.StatusConflict, "source_changed"},
		{"the scan recorded no content fingerprint", noFingerprint, http.StatusConflict, "source_changed"},
	} {
		for _, target := range []string{preflightPath(testCase.trackID), audioPath(testCase.trackID)} {
			t.Run(testCase.name+" "+target[strings.LastIndex(target, "/")+1:], func(t *testing.T) {
				response := application.request(t, http.MethodGet, target, nil, &user, nil)
				requireAPIError(t, response, testCase.status, testCase.code)
				requireNoPathLeak(t, application, response)
			})
		}
	}
	requireAPIError(t, application.request(t, http.MethodGet, preflightPath("not-a-uuid"), nil, &user, nil), http.StatusBadRequest, "invalid_id")
	requireAPIError(t, application.request(t, http.MethodGet, preflightPath(playable), nil, nil, nil), http.StatusUnauthorized, "unauthorized")

	mustExec(t, application, `UPDATE library_roots SET status='disabled' WHERE id=$1::uuid`, library.rootID)
	for _, target := range []string{preflightPath(playable), audioPath(playable)} {
		requireAPIError(t, application.request(t, http.MethodGet, target, nil, &user, nil), http.StatusConflict, "root_disabled")
	}
	if requests := fake.requested(); len(requests) != 0 {
		t.Fatalf("a failed check still started %d conversions: %+v", len(requests), requests)
	}
}

func TestPostgreSQLPlaybackDerivationFailuresHaveStableErrorCodes(t *testing.T) {
	fake := newFakeDerivation(t)
	application := newDerivationApplication(t, fake)
	library := createPlaybackLibrary(t, application)
	user := application.createUser(t, "user")

	for _, testCase := range []struct {
		name       string
		err        error
		status     int
		code       string
		retryAfter string
	}{
		{"ffmpeg failed", &derivation.FailedError{Category: derivation.FailureFFmpegExit, Err: errors.New("exit status 1"), Stderr: "cannot read /secret/library/track.m4a"}, http.StatusConflict, "derivation_failed", ""},
		{"output did not match the source", &derivation.FailedError{Category: derivation.FailureVerifyMismatch}, http.StatusConflict, "derivation_failed", ""},
		{"conversion timed out", &derivation.FailedError{Category: derivation.FailureTimeout, Err: context.DeadlineExceeded}, http.StatusConflict, "derivation_failed", ""},
		{"output over the limit", &derivation.FailedError{Category: derivation.FailureOutputLimit}, http.StatusConflict, "derivation_failed", ""},
		{"concurrency or queue limit", derivation.ErrBusy, http.StatusServiceUnavailable, "derivation_busy", "2"},
		{"manager shutting down", derivation.ErrClosed, http.StatusServiceUnavailable, "derivation_busy", "2"},
		{"source changed during the conversion", fmt.Errorf("%w: content differs from the scan", playback.ErrSourceChanged), http.StatusConflict, "source_changed", ""},
		{"source unreadable during the conversion", fmt.Errorf("%w: open failed", playback.ErrSourceUnavailable), http.StatusConflict, "source_unavailable", ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fake.await = func(context.Context, derivation.Request) error { return testCase.err }
			trackID := library.addTrack(t, application, alacSpec(fmt.Sprintf("failure-%d.m4a", time.Now().UnixNano()), mp4Bytes(4096)))
			response := application.request(t, http.MethodGet, preflightPath(trackID), nil, &user, nil)
			requireAPIError(t, response, testCase.status, testCase.code)
			if got := response.Header().Get("Retry-After"); got != testCase.retryAfter {
				t.Errorf("Retry-After = %q, want %q", got, testCase.retryAfter)
			}
			requireNoPathLeak(t, application, response)
			for _, internal := range []string{"secret", "ffmpeg", "verify_mismatch", "exit status", "fake-"} {
				if strings.Contains(response.Body.String(), internal) {
					t.Fatalf("the response leaks %q: %s", internal, response.Body.String())
				}
			}
			// 失败不留下可交付的派生文件：音频请求仍是“尚未完成”。
			requireAPIError(t, application.request(t, http.MethodGet, audioPath(trackID), nil, &user, nil), http.StatusConflict, "derivation_not_ready")
		})
	}
}

func TestPostgreSQLPlaybackUnexpectedDerivationErrorsAreInternalErrorsAndLogged(t *testing.T) {
	logs := &lockedBuffer{}
	fake := newFakeDerivation(t)
	fake.await = func(context.Context, derivation.Request) error { return errors.New("disk exploded at /secret/path") }
	application := newIntegrationTestApplicationWithLogger(t, slog.New(slog.NewJSONHandler(logs, nil)), WithDerivation(fake), WithPlaybackPreflightWait(derivationPreflightWait))
	library := createPlaybackLibrary(t, application)
	trackID := library.addTrack(t, application, alacSpec("a.m4a", mp4Bytes(4096)))
	user := application.createUser(t, "user")

	response := application.request(t, http.MethodGet, preflightPath(trackID), nil, &user, nil)
	requireAPIError(t, response, http.StatusInternalServerError, "internal")
	if strings.Contains(response.Body.String(), "secret") {
		t.Fatalf("the response leaks the underlying error: %s", response.Body.String())
	}
	if events := logEvents(t, logs, "playback.prepare.failed"); len(events) != 1 {
		t.Fatalf("playback.prepare.failed events = %d, want 1: %s", len(events), logs.String())
	}
}

func TestPostgreSQLPlaybackPreflightWaitEndsWhenTheApplicationShutsDown(t *testing.T) {
	fake := newFakeDerivation(t)
	entered := fake.blockUntil(make(chan struct{}))
	// 等待时间远长于测试：只有生命周期结束才能让预检返回。
	application := newDerivationApplication(t, fake, WithPlaybackPreflightWait(time.Minute))
	library := createPlaybackLibrary(t, application)
	trackID := library.addTrack(t, application, alacSpec("a.m4a", mp4Bytes(4096)))
	user := application.createUser(t, "user")

	finished := make(chan *httptest.ResponseRecorder, 1)
	go func() { finished <- application.request(t, http.MethodGet, preflightPath(trackID), nil, &user, nil) }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the preflight never reached the derivation")
	}
	application.shutdown()
	select {
	case response := <-finished:
		requireAPIError(t, response, http.StatusServiceUnavailable, "derivation_busy")
		if response.Header().Get("Retry-After") != "2" {
			t.Fatalf("Retry-After = %q, want 2", response.Header().Get("Retry-After"))
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the long poll outlived the application lifecycle; server shutdown would be blocked")
	}
}

func TestPostgreSQLPlaybackDerivedStreamIsAbortedOnShutdown(t *testing.T) {
	logs := &lockedBuffer{}
	fake := newFakeDerivation(t)
	fake.content = func(string) []byte { return flacBytes(streamFileSize) }
	application := newIntegrationTestApplicationWithLogger(t, slog.New(slog.NewJSONHandler(logs, nil)),
		WithDerivation(fake), WithPlaybackPreflightWait(derivationPreflightWait))
	library := createPlaybackLibrary(t, application)
	trackID := library.addTrack(t, application, alacSpec("long.m4a", mp4Bytes(4096)))
	user := application.createUser(t, "user")
	if response := application.request(t, http.MethodGet, preflightPath(trackID), nil, &user, nil); response.Code != http.StatusOK {
		t.Fatalf("preflight: HTTP %d: %s", response.Code, response.Body.String())
	}

	writer, done := startStream(t, application, &user, trackID, context.Background())
	application.shutdown()
	finishStream(t, writer, done)

	if written := writer.bytesWritten(); written == 0 || written >= streamFileSize {
		t.Fatalf("delivered %d of %d derived bytes; want some audio, then a cut before the end", written, streamFileSize)
	}
	requireShutdownEvent(t, logs, trackID)
}

// 以下用真实的 ffmpeg 与合成 ALAC 素材验证端到端的无损派生；没有设置 ROOMUSIC_TEST_FFMPEG 时跳过。
// scripts/test-integration.sh 总是设置它（取出固定版本），所以本地集成与 CI 不会静默跳过。

func requireTestFFmpeg(t *testing.T) string {
	t.Helper()
	path := os.Getenv("ROOMUSIC_TEST_FFMPEG")
	if path == "" {
		t.Skip("ROOMUSIC_TEST_FFMPEG is not set")
	}
	return path
}

func openRealDerivation(t *testing.T, ffmpeg string) *derivation.Manager {
	t.Helper()
	manager, err := derivation.Open(context.Background(), derivation.Config{
		Dir: filepath.Join(t.TempDir(), "media-cache"), MaxBytes: 1 << 30, FFmpegPath: ffmpeg,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("open derivation: %v", err)
	}
	t.Cleanup(func() {
		if err := manager.Close(10 * time.Second); err != nil {
			t.Errorf("close derivation: %v", err)
		}
	})
	return manager
}

// addFixtureTrack 把合成素材复制进目录并登记真实的内容指纹，这样转换前的指纹复核能通过。
func (library playbackLibrary) addFixtureTrack(t *testing.T, application *integrationTestApplication, fixture, relative string, bitDepth, sampleRate int) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("..", "..", "testdata", "playback", fixture))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	fingerprint, err := libpkg.ContentFingerprintOf(context.Background(), filepath.Ext(fixture), bytes.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatalf("fingerprint fixture: %v", err)
	}
	spec := alacSpec(relative, content)
	spec.bitDepth, spec.sampleRate, spec.fingerprint = bitDepth, sampleRate, fingerprint
	return library.addTrack(t, application, spec)
}

// readStreamInfo 读取 FLAC 的 STREAMINFO：采样率、声道数、位深与总采样数。
func readStreamInfo(t *testing.T, content []byte) (rate, channels, bits int, total uint64) {
	t.Helper()
	// "fLaC" + 块头（类型 0 = STREAMINFO，长度 34）+ STREAMINFO：10 字节块大小与帧大小之后是 20 位采样率、3 位声道-1、5 位位深-1、36 位总采样数。
	if len(content) < 8+34 || string(content[:4]) != "fLaC" || content[4]&0x7f != 0 {
		t.Fatalf("not a FLAC stream with a leading STREAMINFO: % x", content[:min(len(content), 8)])
	}
	info := content[8 : 8+34]
	packed := uint64(info[10])<<56 | uint64(info[11])<<48 | uint64(info[12])<<40 | uint64(info[13])<<32 |
		uint64(info[14])<<24 | uint64(info[15])<<16 | uint64(info[16])<<8 | uint64(info[17])
	rate = int(packed >> 44)
	channels = int(packed>>41&0x7) + 1
	bits = int(packed>>36&0x1f) + 1
	total = packed & (1<<36 - 1)
	return rate, channels, bits, total
}

// pcmDigest 返回文件解码后的 32 位 PCM 的 MD5，用来独立证明派生文件与源文件在音频上相同。
func pcmDigest(t *testing.T, ffmpeg string, content []byte) string {
	t.Helper()
	command := exec.Command(ffmpeg, "-hide_banner", "-nostdin", "-v", "error", "-i", "pipe:0", "-map", "0:a:0", "-c:a", "pcm_s32le", "-f", "md5", "-")
	command.Stdin = bytes.NewReader(content)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("ffmpeg digest: %v", err)
	}
	return strings.TrimSpace(string(output))
}

func TestPostgreSQLPlaybackDerivesALACLosslesslyWithFFmpeg(t *testing.T) {
	ffmpeg := requireTestFFmpeg(t)
	manager := openRealDerivation(t, ffmpeg)
	application := newIntegrationTestApplication(t, WithDerivation(manager))
	library := createPlaybackLibrary(t, application)
	user := application.createUser(t, "user")
	other := application.createUser(t, "user")

	for _, fixture := range []struct {
		file, relative            string
		bits, rate, wantedSamples int
	}{
		{"alac-16-44100.m4a", "专辑/16-44100.m4a", 16, 44100, 2 * 44100},
		{"alac-24-48000.m4a", "专辑/24-48000.m4a", 24, 48000, 48000},
	} {
		t.Run(fixture.file, func(t *testing.T) {
			trackID := library.addFixtureTrack(t, application, fixture.file, fixture.relative, fixture.bits, fixture.rate)
			original, err := os.ReadFile(filepath.Join("..", "..", "testdata", "playback", fixture.file))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}

			// 音频请求不启动转换：派生之前是 409。
			requireAPIError(t, application.request(t, http.MethodGet, audioPath(trackID), nil, &user, nil), http.StatusConflict, "derivation_not_ready")

			// 两个成员同时预检同一首曲目：合并成一次转换，都得到派生表示。
			results := make(chan *httptest.ResponseRecorder, 2)
			for _, member := range []integrationTestUser{user, other} {
				go func() { results <- application.request(t, http.MethodGet, preflightPath(trackID), nil, &member, nil) }()
			}
			var size int64
			for range 2 {
				response := <-results
				if response.Code != http.StatusOK {
					t.Fatalf("preflight: HTTP %d: %s", response.Code, response.Body.String())
				}
				body := decodePreflight(t, response)
				if body.Representation == nil || body.Representation.Kind != "derived" || body.Representation.MIMEType != "audio/flac" || body.Representation.SizeBytes <= 0 {
					t.Fatalf("preflight = %s", response.Body.String())
				}
				size = body.Representation.SizeBytes
			}

			full := application.request(t, http.MethodGet, audioPath(trackID), nil, &user, nil)
			if full.Code != http.StatusOK || int64(full.Body.Len()) != size || full.Header().Get("Content-Type") != "audio/flac" {
				t.Fatalf("derived audio: HTTP %d, %d bytes, Content-Type %q (preflight said %d bytes)", full.Code, full.Body.Len(), full.Header().Get("Content-Type"), size)
			}
			derived := full.Body.Bytes()
			rate, channels, bits, total := readStreamInfo(t, derived)
			if rate != fixture.rate || channels != 2 || bits != fixture.bits || total != uint64(fixture.wantedSamples) {
				t.Fatalf("STREAMINFO = %d Hz, %d channels, %d bits, %d samples; want %d Hz, 2 channels, %d bits, %d samples",
					rate, channels, bits, total, fixture.rate, fixture.bits, fixture.wantedSamples)
			}
			// 无损：解码后的 PCM 与源文件逐位相同。
			if got, want := pcmDigest(t, ffmpeg, derived), pcmDigest(t, ffmpeg, original); got != want {
				t.Fatalf("derived PCM digest %s differs from the source %s", got, want)
			}

			// Range 取到的是派生文件的对应字节。
			partial := application.request(t, http.MethodGet, audioPath(trackID), nil, &user, map[string]string{"Range": "bytes=100-199"})
			if partial.Code != http.StatusPartialContent || !bytes.Equal(partial.Body.Bytes(), derived[100:200]) {
				t.Fatalf("Range: HTTP %d, %d bytes", partial.Code, partial.Body.Len())
			}

			// 缓存命中的预检立即返回，大小不变，强校验值稳定。
			etag := full.Header().Get("ETag")
			started := time.Now()
			if again := application.request(t, http.MethodGet, preflightPath(trackID), nil, &user, nil); again.Code != http.StatusOK || time.Since(started) > 2*time.Second {
				t.Fatalf("cached preflight: HTTP %d after %s", again.Code, time.Since(started))
			}
			if again := application.request(t, http.MethodGet, audioPath(trackID), nil, &user, nil); again.Header().Get("ETag") != etag || etag == "" {
				t.Fatalf("ETag changed between reads: %q, %q", etag, again.Header().Get("ETag"))
			}

			// 源文件被改动后不再交付缓存里的派生文件：大小或修改时间与扫描记录不符就是 source_changed。
			path := filepath.Join(library.root, filepath.FromSlash(fixture.relative))
			changed := playbackModified.Add(time.Minute)
			if err := os.Chtimes(path, changed, changed); err != nil {
				t.Fatalf("touch source: %v", err)
			}
			for _, target := range []string{preflightPath(trackID), audioPath(trackID)} {
				requireAPIError(t, application.request(t, http.MethodGet, target, nil, &user, nil), http.StatusConflict, "source_changed")
			}
			if err := os.Chtimes(path, playbackModified, playbackModified); err != nil {
				t.Fatalf("restore source time: %v", err)
			}
			if again := application.request(t, http.MethodGet, audioPath(trackID), nil, &user, nil); again.Code != http.StatusOK {
				t.Fatalf("restored source did not play again: HTTP %d", again.Code)
			}
		})
	}
	if stats := manager.Stats(); stats.Entries != 2 {
		t.Fatalf("cache entries = %d, want one per fixture (concurrent preflights must share one conversion)", stats.Entries)
	}

	// 停用目录后，缓存里的派生文件也不再交付。
	mustExec(t, application, `UPDATE library_roots SET status='disabled' WHERE id=$1::uuid`, library.rootID)
	var trackID string
	if err := application.database.QueryRow(`SELECT id::text FROM tracks LIMIT 1`).Scan(&trackID); err != nil {
		t.Fatalf("read track: %v", err)
	}
	for _, target := range []string{preflightPath(trackID), audioPath(trackID)} {
		requireAPIError(t, application.request(t, http.MethodGet, target, nil, &user, nil), http.StatusConflict, "root_disabled")
	}
}

func TestPostgreSQLPlaybackRejectsASourceRewrittenInPlaceBeforeConverting(t *testing.T) {
	ffmpeg := requireTestFFmpeg(t)
	manager := openRealDerivation(t, ffmpeg)
	application := newIntegrationTestApplication(t, WithDerivation(manager))
	library := createPlaybackLibrary(t, application)
	user := application.createUser(t, "user")
	trackID := library.addFixtureTrack(t, application, "alac-16-44100.m4a", "rewritten.m4a", 16, 44100)

	// 保留大小与修改时间的原地改写：扫描记录的指纹对不上，转换前的内容复核必须拒绝。
	path := filepath.Join(library.root, "rewritten.m4a")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	// M4A 的内容指纹覆盖整个 mdat 负载：翻转负载里的一个字节，大小与修改时间都不变。
	mdat := bytes.Index(content, []byte("mdat"))
	if mdat < 0 {
		t.Fatal("fixture has no mdat atom")
	}
	content[mdat+8] ^= 0xff
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("rewrite source: %v", err)
	}
	if err := os.Chtimes(path, playbackModified, playbackModified); err != nil {
		t.Fatalf("restore modification time: %v", err)
	}

	requireAPIError(t, application.request(t, http.MethodGet, preflightPath(trackID), nil, &user, nil), http.StatusConflict, "source_changed")
	if stats := manager.Stats(); stats.Entries != 0 {
		t.Fatalf("a rewritten source produced %d cache entries", stats.Entries)
	}
}
