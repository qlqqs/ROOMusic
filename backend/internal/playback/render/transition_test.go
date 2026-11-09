package render

import (
	"bytes"
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/playback/render/ffmpeg"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

type transitionBodyResult struct {
	data []byte
	err  error
}

// 等待通知或操纵播放头期间也持续消费 Body，避免编码输出背压阻止曲尾推进。
func collectTransitionBody(s *session) <-chan transitionBodyResult {
	done := make(chan transitionBodyResult, 1)
	go func() { data, err := io.ReadAll(s.Body()); done <- transitionBodyResult{data, err} }()
	return done
}

func awaitTransitionBody(t *testing.T, done <-chan transitionBodyResult) []byte {
	t.Helper()
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

func transitionPCM(t *testing.T, ff string, encoded []byte) []byte {
	t.Helper()
	return ffmpegOutput(t, ff, encoded, "-i", "pipe:0", "-map", "0:a:0", "-c:a", "pcm_s32le", "-f", "s32le", "pipe:1")
}

func assertTransitionPCM(t *testing.T, got, want []byte) {
	t.Helper()
	if len(got) != len(want) || md5.Sum(got) != md5.Sum(want) {
		t.Fatalf("PCM 字节数 %d/%d，MD5 %x/%x", len(got), len(want), md5.Sum(got), md5.Sum(want))
	}
}

func waitRenderComplete(t *testing.T, s *session) {
	t.Helper()
	waitUntil(t, "渲染完成", func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.renderComplete })
}

func assertBoundaryEnd(t *testing.T, s *session) {
	t.Helper()
	waitRenderComplete(t, s)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil {
		t.Errorf("边界结束出现错误：%v", s.failure)
	}
	// 播放头仍在零点，曲尾事件尚不能派发；直接核对待派发队列避免等待不存在的事件。
	for _, event := range s.pending {
		if event.event.Kind == "stream_ended" {
			t.Errorf("格式边界不应登记 stream_ended：%+v", event)
		}
	}
}

func TestRenderGaplessAndGap(t *testing.T) {
	ff := pinnedFFmpeg(t)
	tool := openTestTool(t, ff, ffmpeg.Options{})
	whole := makeFLAC(t, ff, 16, 44100, 997, 10)
	const cut = 5*44100 + 123 // 切点不在 200 ms 网格上。
	parts := make(map[string]audioFixture)
	for i, id := range []string{"a", "b"} {
		start, end := 0, cut
		if i == 1 {
			start, end = cut, len(whole.pcm)/8
		}
		path := filepath.Join(t.TempDir(), id+".flac")
		ffmpegOutput(t, ff, nil, "-i", whole.path, "-af", fmt.Sprintf("atrim=start_sample=%d:end_sample=%d,asetpts=PTS-STARTPTS", start, end), "-c:a", "flac", path)
		track := whole.track
		track.DurationSeconds = float64(end-start) / 44100
		parts[id] = audioFixture{path: path, track: track, pcm: whole.pcm[start*8 : end*8]}
	}
	for _, gapless := range []bool{true, false} {
		kind := "gapless"
		if !gapless {
			kind = "gap"
		}
		t.Run(kind, func(t *testing.T) {
			r := newTestRenderer(t, tool, fixtureSource(parts))
			spec := renderSpec("flac")
			spec.Cursor, spec.Settings.Gapless = testCursor(nextB), gapless
			s := openTestSession(t, r, spec)
			pcm := transitionPCM(t, ff, readBody(t, s))
			want := whole.pcm
			start := cut
			if !gapless {
				silence := make([]byte, 44100/2*8)
				want = append(append(append([]byte(nil), parts["a"].pcm...), silence...), parts["b"].pcm...)
				start += 44100 / 2
				if len(pcm) < start*8 || !bytes.Equal(pcm[cut*8:start*8], silence) {
					t.Fatal("曲间不是恰好半秒数字静音")
				}
			}
			assertTransitionPCM(t, pcm, want)
			waitRenderComplete(t, s)
			observe(s, float64(len(pcm)/8)/44100)
			events := eventsThrough(t, s, "stream_ended", 0)
			started := 0
			for _, e := range events {
				if e.Kind == "track_failed" || e.Kind == "format_boundary" {
					t.Errorf("意外事件：%+v", e)
				}
				if e.Kind == "track_started" {
					started++
					if e.EntryID != "entry-b" || e.Transition != kind || e.StreamSeconds != float64(start)/44100 {
						t.Errorf("换轨事件：%+v", e)
					}
				}
			}
			if started != 1 {
				t.Errorf("track_started 数量 = %d，期望 1", started)
			}
		})
	}
}

func TestRenderFormatBoundaryEndsCurrentStream(t *testing.T) {
	ff := pinnedFFmpeg(t)
	tool := openTestTool(t, ff, ffmpeg.Options{})
	a := makeFLAC(t, ff, 16, 44100, 997, 0.6)
	b := makeFLAC(t, ff, 16, 48000, 733, 0.6)
	r := newTestRenderer(t, tool, fixtureSource(map[string]audioFixture{"a": a, "b": b}))
	spec := renderSpec("flac")
	spec.Cursor = testCursor(nextB)
	s := openTestSession(t, r, spec)
	events := eventsThrough(t, s, "format_boundary", 0)
	e := events[len(events)-1]
	if e.Reason != "format" || e.NextEntryID != "entry-b" || s.playhead() != 0 {
		t.Fatalf("格式边界：%+v", e)
	}
	assertTransitionPCM(t, transitionPCM(t, ff, readBody(t, s)), a.pcm)
	assertBoundaryEnd(t, s)
}

func TestRenderNextNotReadyTimeout(t *testing.T) {
	ff := pinnedFFmpeg(t)
	a := makeFLAC(t, ff, 16, 44100, 997, 0.6)
	b := makeFLAC(t, ff, 16, 44100, 733, 0.6)
	tool, _, _, started := gatedTrackDecoder(t, ff, nil, nil, true, b.path)
	deadlines := make(chan testTicker, 1)
	r := New(Options{FFmpeg: tool, Sources: fixtureSource(map[string]audioFixture{"a": a, "b": b}), Clock: func() time.Time { return testNow }, NewTicker: func(d time.Duration) Ticker {
		ticker := testTicker{make(chan time.Time, 1)}
		if d == 5*time.Second {
			deadlines <- ticker
		}
		return ticker
	}})
	t.Cleanup(r.Close)
	spec := renderSpec("flac")
	spec.Cursor = testCursor(nextB)
	s := openTestSession(t, r, spec)
	body := collectTransitionBody(s)
	waitUntil(t, "下一首假解码器开始等待", func() bool { _, err := os.Stat(started); return err == nil })
	select {
	case ticker := <-deadlines:
		// 只推进曲尾的五秒等待，不触发 ffmpeg 自身十秒无输出超时。
		ticker.ticks <- testNow.Add(5 * time.Second)
	case <-time.After(testWait):
		t.Fatal("没有创建五秒就绪期限")
	}
	events := eventsThrough(t, s, "format_boundary", 0)
	e := events[len(events)-1]
	if e.Reason != "not_ready" || e.NextEntryID != "entry-b" {
		t.Fatalf("就绪超时事件：%+v", e)
	}
	for _, e := range events {
		if e.Kind == "track_failed" {
			t.Errorf("就绪超时不能冒充解码失败：%+v", e)
		}
	}
	assertTransitionPCM(t, transitionPCM(t, ff, awaitTransitionBody(t, body)), a.pcm)
	assertBoundaryEnd(t, s)
}

func TestRenderNextChanged(t *testing.T) {
	ff := pinnedFFmpeg(t)
	tool := openTestTool(t, ff, ffmpeg.Options{})
	b := makeFLAC(t, ff, 16, 44100, 733, 0.6)
	t.Run("已编码但播放头未到接缝要求重建", func(t *testing.T) {
		a := makeFLAC(t, ff, 16, 44100, 997, 0.6)
		r := newTestRenderer(t, tool, fixtureSource(map[string]audioFixture{"a": a, "b": b}))
		spec := renderSpec("flac")
		spec.Cursor = testCursor(nextB)
		s := openTestSession(t, r, spec)
		readBody(t, s)
		waitRenderComplete(t, s)
		state := s.changeState.Load()
		if state == nil || state.transition != 26460 || state.written <= state.transition || s.playhead() != 0 {
			t.Fatalf("未构造已编码、未播放的换轨：%+v", state)
		}
		s.NextChanged()
		// 通知以后播放头跨过接缝，仍应保留通知时的重建需求。
		observe(s, 0.8)
		events := eventsThrough(t, s, "rebuild_needed", 0)
		for _, e := range events {
			if e.Kind == "format_boundary" {
				t.Errorf("不应改发格式边界：%+v", e)
			}
		}
	})
	t.Run("中途换掉已就绪下一首并拼接新曲", func(t *testing.T) {
		a := makeFLAC(t, ff, 16, 44100, 997, 25)
		c := makeFLAC(t, ff, 16, 44100, 1193, 0.6)
		source := fixtureSource(map[string]audioFixture{"a": a, "b": b, "c": c})
		preparedB := make(chan *os.File, 1)
		preparedC := make(chan struct{}, 1)
		r := newTestRenderer(t, tool, testSource(func(ctx context.Context, user, id string) (Track, error) {
			track, err := source.Prepare(ctx, user, id)
			if id == "b" && err == nil {
				preparedB <- track.File
			}
			if id == "c" && err == nil {
				preparedC <- struct{}{}
			}
			return track, err
		}))
		var replace atomic.Bool
		spec := renderSpec("flac")
		spec.Cursor = testCursor(func(after string) (zones.Entry, bool) {
			id := "b"
			if replace.Load() {
				id = "c"
			}
			return zones.Entry{EntryID: "entry-" + id, TrackID: id}, after == "entry-a"
		})
		s := openTestSession(t, r, spec)
		body := collectTransitionBody(s)
		// 领先上限把渲染固定在第 20 秒：B 已预取，A 尚未读完，不依赖调度速度。
		waitUntil(t, "渲染达到二十秒领先上限", func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.written == 20*44100 })
		select {
		case file := <-preparedB:
			// 短曲的来源文件在填满 PCM 并 EOF 后关闭，保证替换的是已准备好的 B。
			waitUntil(t, "旧下一首解码完成", func() bool { _, err := file.Stat(); return errors.Is(err, os.ErrClosed) })
		case <-time.After(testWait):
			t.Fatal("旧下一首没有预取")
		}
		replace.Store(true)
		s.NextChanged()
		select {
		case <-preparedC:
		case <-time.After(testWait):
			t.Fatal("NextChanged 没有重取新下一首")
		}
		observe(s, 18)
		encoded := awaitTransitionBody(t, body)
		assertTransitionPCM(t, transitionPCM(t, ff, encoded), append(append([]byte(nil), a.pcm...), c.pcm...))
		waitRenderComplete(t, s)
		observe(s, 25.6)
		events := eventsThrough(t, s, "stream_ended", 0)
		starts := 0
		for _, e := range events {
			if e.Kind == "rebuild_needed" || e.Kind == "format_boundary" || e.Kind == "track_failed" {
				t.Errorf("未入链替换不应发出此事件：%+v", e)
			}
			if e.Kind == "track_started" {
				starts++
				if e.EntryID != "entry-c" || e.Transition != "gapless" || e.StreamSeconds != 25 {
					t.Errorf("新下一首换轨：%+v", e)
				}
			}
		}
		if starts != 1 {
			t.Errorf("track_started 数量 = %d，期望 1", starts)
		}
	})
}

func TestRenderFivePreparationFailures(t *testing.T) {
	ff := pinnedFFmpeg(t)
	tool := openTestTool(t, ff, ffmpeg.Options{})
	a := makeFLAC(t, ff, 16, 44100, 997, 0.6)
	source := fixtureSource(map[string]audioFixture{"a": a})
	var mu sync.Mutex
	var prepared, requested []string
	r := newTestRenderer(t, tool, testSource(func(ctx context.Context, user, id string) (Track, error) {
		mu.Lock()
		prepared = append(prepared, id)
		mu.Unlock()
		return source.Prepare(ctx, user, id)
	}))
	spec := renderSpec("flac")
	spec.Cursor = testCursor(func(after string) (zones.Entry, bool) {
		mu.Lock()
		defer mu.Unlock()
		requested = append(requested, after)
		// 第六首仍可取得：必须由连续五次失败的限额停止，而非队尾停止。
		index := len(requested)
		return zones.Entry{EntryID: fmt.Sprintf("entry-bad-%d", index), TrackID: fmt.Sprintf("bad-%d", index)}, index <= 6
	})
	s := openTestSession(t, r, spec)
	assertTransitionPCM(t, transitionPCM(t, ff, readBody(t, s)), a.pcm)
	waitRenderComplete(t, s)
	observe(s, 0.6)
	events := eventsThrough(t, s, "stream_ended", 0)
	var failed []string
	for _, e := range events {
		if e.Kind == "format_boundary" || e.Kind == "track_started" {
			t.Errorf("失败后应结束本流：%+v", e)
		}
		if e.Kind == "track_failed" {
			failed = append(failed, e.EntryID)
			if e.Reason != "not_found" || e.StreamSeconds != 0.6 {
				t.Errorf("失败分类或登记帧错误：%+v", e)
			}
		}
	}
	wantFailed := []string{"entry-bad-1", "entry-bad-2", "entry-bad-3", "entry-bad-4", "entry-bad-5"}
	if !reflect.DeepEqual(failed, wantFailed) {
		t.Errorf("track_failed = %v，期望 %v", failed, wantFailed)
	}
	mu.Lock()
	defer mu.Unlock()
	if want := []string{"a", "bad-1", "bad-2", "bad-3", "bad-4", "bad-5"}; !reflect.DeepEqual(prepared, want) {
		t.Errorf("准备条目 = %v，期望 %v", prepared, want)
	}
	if want := []string{"entry-a", "entry-bad-1", "entry-bad-2", "entry-bad-3", "entry-bad-4"}; !reflect.DeepEqual(requested, want) {
		t.Errorf("Cursor 重取 = %v，期望 %v", requested, want)
	}
}
