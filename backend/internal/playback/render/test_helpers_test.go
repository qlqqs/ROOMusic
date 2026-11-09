package render

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/playback/render/ffmpeg"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

const testWait = 5 * time.Second

var testNow = time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)

type testTicker struct{ ticks chan time.Time }

func (t testTicker) C() <-chan time.Time { return t.ticks }
func (t testTicker) Stop()               {}

type testSource func(context.Context, string, string) (Track, error)

func (f testSource) Prepare(ctx context.Context, userID, trackID string) (Track, error) {
	return f(ctx, userID, trackID)
}

type testCursor func(string) (zones.Entry, bool)

func (f testCursor) Next(after string) (zones.Entry, bool) { return f(after) }

func pinnedFFmpeg(t *testing.T) string {
	t.Helper()
	path := os.Getenv("ROOMUSIC_TEST_FFMPEG")
	if path == "" {
		t.Skip("请用 scripts/ffmpeg-pinned.sh 设置 ROOMUSIC_TEST_FFMPEG")
	}
	return path
}

func ffmpegOutput(t *testing.T, path string, input []byte, args ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testWait)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, append([]string{"-nostdin", "-hide_banner", "-loglevel", "error", "-threads", "1"}, args...)...)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("ffmpeg %v: %v\n%s", args, err, stderr.String())
	}
	return out
}

func openTestTool(t *testing.T, path string, options ffmpeg.Options) *ffmpeg.Tool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testWait)
	defer cancel()
	tool, err := ffmpeg.OpenWithOptions(ctx, path, options)
	if err != nil {
		t.Fatalf("打开测试 ffmpeg: %v", err)
	}
	return tool
}

type audioFixture struct {
	path  string
	track Track
	pcm   []byte
}

func makeFLAC(t *testing.T, ff string, bits, rate, frequency int, duration float64) audioFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "source.flac")
	sampleFormat := "s16"
	if bits == 24 {
		sampleFormat = "s32"
	}
	// 用浮点表达式生成信号，24 位素材的低八位不是由 16 位素材补零而来。
	ffmpegOutput(t, ff, nil, "-f", "lavfi", "-i", fmt.Sprintf("aevalsrc=0.37*sin(2*PI*%d*t):s=%d:d=%g", frequency, rate, duration), "-ac", "2", "-c:a", "flac", "-sample_fmt", sampleFormat, path)
	pcm := ffmpegOutput(t, ff, nil, "-i", path, "-map", "0:a:0", "-c:a", "pcm_s32le", "-f", "s32le", "pipe:1")
	return audioFixture{path: path, track: Track{Codec: "flac", SourceKind: "file", SampleRate: rate, Channels: 2, BitDepth: bits, DurationSeconds: duration}, pcm: pcm}
}
func fixtureSource(fixtures map[string]audioFixture) testSource {
	return func(_ context.Context, _, id string) (Track, error) {
		fixture, ok := fixtures[id]
		if !ok {
			return Track{}, &SourceError{Reason: "not_found"}
		}
		track := fixture.track
		var err error
		track.File, err = os.Open(fixture.path)
		return track, err
	}
}
func renderSpec(codec string) zones.RenderSpec {
	mime := map[string]string{"flac": "audio/flac", "wav": "audio/wav", "opus": `audio/ogg; codecs="opus"`, "aac": "audio/aac", "alac": "application/vnd.apple.mpegurl"}[codec]
	return zones.RenderSpec{Seq: 1, UserID: "user", Start: zones.Entry{EntryID: "entry-a", TrackID: "a"}, Settings: zones.ZoneSettings{Leveling: "off", Gapless: true, Quality: "lossless", SampleRate: zones.DefaultSampleRateDocument(), Downmix: zones.DefaultDownmixDocument(), PEQ: zones.DefaultPEQDocument(), Convolution: zones.DefaultConvolutionDocument()}, Format: zones.StreamFormat{Codec: codec, MimeType: mime, MaxChannels: 2}}
}
func nextB(after string) (zones.Entry, bool) {
	return zones.Entry{EntryID: "entry-b", TrackID: "b"}, after == "entry-a"
}
func newTestRenderer(t *testing.T, tool *ffmpeg.Tool, source TrackSource) *Renderer {
	t.Helper()
	r := New(Options{FFmpeg: tool, Sources: source, Clock: func() time.Time { return testNow }, NewTicker: func(time.Duration) Ticker { return testTicker{make(chan time.Time)} }})
	t.Cleanup(r.Close)
	return r
}
func openTestSession(t *testing.T, r *Renderer, spec zones.RenderSpec) *session {
	t.Helper()
	s, err := r.Open(context.Background(), spec)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(s.Close)
	select {
	case <-s.(*session).prepared:
	case <-time.After(testWait):
		t.Fatal("会话准备未完成")
	}
	return s.(*session)
}
func readBody(t *testing.T, s *session) []byte {
	t.Helper()
	type result struct {
		data []byte
		err  error
	}
	done := make(chan result, 1)
	go func() { data, err := io.ReadAll(s.Body()); done <- result{data, err} }()
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("Body: %v", got.err)
		}
		return got.data
	case <-time.After(testWait):
		t.Fatal("Body 未结束")
		return nil
	}
}
func waitUntil(t *testing.T, description string, ready func() bool) {
	t.Helper()
	deadline := time.NewTimer(testWait)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for !ready() {
		select {
		case <-deadline.C:
			t.Fatalf("等待超时：%s", description)
		case <-ticker.C:
		}
	}
}
func nextEvent(t *testing.T, s *session) zones.Event {
	t.Helper()
	select {
	case event, ok := <-s.Events():
		if !ok {
			t.Fatal("事件通道提前关闭")
		}
		return event
	case <-time.After(testWait):
		t.Fatal("等待事件超时")
		return zones.Event{}
	}
}
func eventsThrough(t *testing.T, s *session, kind string, seconds float64) []zones.Event {
	t.Helper()
	var events []zones.Event
	for {
		event := nextEvent(t, s)
		events = append(events, event)
		if event.Kind == kind && (kind != "time" || event.StreamSeconds == seconds) {
			return events
		}
	}
}
func observe(s *session, seconds float64) {
	s.Observe(zones.PlayheadSample{StreamSeq: s.spec.Seq, StreamSeconds: seconds, At: testNow})
}

// 自检期间使用真实计时器；只有被测会话创建的计时器才由测试手动触发。
type controlledTimer struct {
	ticks    chan time.Time
	stopped  atomic.Bool
	duration time.Duration
}

func (t *controlledTimer) C() <-chan time.Time { return t.ticks }
func (t *controlledTimer) Stop() bool          { return !t.stopped.Swap(true) }

type realTestTimer struct{ *time.Timer }

func (t realTestTimer) C() <-chan time.Time { return t.Timer.C }

type timerControl struct {
	armed  atomic.Bool
	mu     sync.Mutex
	timers []*controlledTimer
}

func (c *timerControl) newTimer(d time.Duration) ffmpeg.Timer {
	if !c.armed.Load() {
		return realTestTimer{time.NewTimer(d)}
	}
	timer := &controlledTimer{ticks: make(chan time.Time, 1), duration: d}
	c.mu.Lock()
	c.timers = append(c.timers, timer)
	c.mu.Unlock()
	return timer
}
func (c *timerControl) active(d time.Duration) *controlledTimer {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.timers) - 1; i >= 0; i-- {
		timer := c.timers[i]
		if timer.duration == d && !timer.stopped.Load() {
			return timer
		}
	}
	return nil
}
func (c *timerControl) fire(t *testing.T, d time.Duration) {
	t.Helper()
	var timer *controlledTimer
	waitUntil(t, "注入的超时计时器", func() bool { timer = c.active(d); return timer != nil })
	timer.ticks <- testNow.Add(d)
}
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
