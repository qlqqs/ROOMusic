package render

import (
	"math"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/playback/render/ffmpeg"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

func TestRenderHeadroomSignal(t *testing.T) {
	ff := pinnedFFmpeg(t)
	tool := openTestTool(t, ff, ffmpeg.Options{})
	fixture := makeFLAC(t, ff, 16, 44100, 997, 0.2)
	r := newTestRenderer(t, tool, fixtureSource(map[string]audioFixture{"a": fixture}))
	spec := renderSpec("flac")
	spec.Settings.Headroom = zones.Headroom{Enabled: true, DB: -3}
	s := openTestSession(t, r, spec)
	readBody(t, s)
	events := eventsThrough(t, s, "signal", 0)
	path := events[len(events)-1].Signal.(signalPath)
	var kinds []string
	for _, step := range path.Steps {
		kinds = append(kinds, step.Kind)
	}
	if want := []string{"source", "headroom", "dither", "encode"}; !reflect.DeepEqual(kinds, want) {
		t.Fatalf("Headroom 信号路径 = %v，期望 %v", kinds, want)
	}
}

func TestRenderClippingWindow(t *testing.T) {
	ff := pinnedFFmpeg(t)
	tool := openTestTool(t, ff, ffmpeg.Options{})
	fixture := makeFLAC(t, ff, 16, 44100, 997, 0.2)
	// 一个量子内的 0.37 幅度正弦叠加 12 dB，确实超过满刻度。
	gain := 12.0
	fixture.track.TrackGainDB = &gain
	var elapsed atomic.Int64
	clipTicks := make(chan time.Time)
	r := New(Options{
		FFmpeg: tool, Sources: fixtureSource(map[string]audioFixture{"a": fixture}),
		Clock: func() time.Time { return testNow.Add(time.Duration(elapsed.Load())) },
		NewTicker: func(interval time.Duration) Ticker {
			if interval == 250*time.Millisecond {
				return testTicker{clipTicks}
			}
			return testTicker{make(chan time.Time)}
		},
	})
	t.Cleanup(r.Close)
	spec := renderSpec("flac")
	spec.Settings.Leveling = "track"
	spec.Settings.Headroom = zones.Headroom{Enabled: true, DB: 0, ClipIndicator: true}
	s := openTestSession(t, r, spec)
	readBody(t, s)
	var clipped signalPath
	for {
		event := nextEvent(t, s)
		if event.Kind == "signal" {
			path := event.Signal.(signalPath)
			if path.Clipping != nil && *path.Clipping {
				clipped = path
				break
			}
		}
	}
	for _, at := range []time.Duration{250 * time.Millisecond, 500 * time.Millisecond} {
		elapsed.Store(int64(at))
		select {
		case clipTicks <- testNow.Add(at):
		case <-time.After(testWait):
			t.Fatal("削波复评计时器未被消费")
		}
		// 无缓冲 tick 已被消费，再通过同一派发协程的屏障确认复评结束。
		if err := s.immediate(zones.Event{Kind: "test_barrier"}); err != nil {
			t.Fatal(err)
		}
		events := eventsThrough(t, s, "test_barrier", 0)
		cleared := false
		for _, event := range events {
			if event.Kind != "signal" {
				continue
			}
			path := event.Signal.(signalPath)
			if path.Clipping != nil && !*path.Clipping {
				cleared = true
				if !reflect.DeepEqual(path.Steps, clipped.Steps) || path.Quality != clipped.Quality {
					t.Fatalf("削波变化覆盖了信号路径：%+v", path)
				}
				if !event.At.Equal(testNow.Add(at)) {
					t.Fatalf("削波熄灭时间 = %v，期望 %v", event.At, testNow.Add(at))
				}
			}
		}
		if cleared != (at >= 450*time.Millisecond) {
			t.Fatalf("%v 时削波熄灭 = %v", at, cleared)
		}
	}
}

func TestRenderProcessingSpeedPreservesSignal(t *testing.T) {
	ff := pinnedFFmpeg(t)
	tool := openTestTool(t, ff, ffmpeg.Options{})
	fixture := makeFLAC(t, ff, 16, 44100, 997, 2)
	var elapsed atomic.Int64
	// 每次读时钟推进 100 ms，使真实渲染循环跨过上报周期，不等待墙钟。
	r := New(Options{
		FFmpeg: tool, Sources: fixtureSource(map[string]audioFixture{"a": fixture}),
		Clock:     func() time.Time { return testNow.Add(time.Duration(elapsed.Add(int64(100 * time.Millisecond)))) },
		NewTicker: func(time.Duration) Ticker { return testTicker{make(chan time.Time)} },
	})
	t.Cleanup(r.Close)
	s := openTestSession(t, r, renderSpec("flac"))
	readBody(t, s)
	for {
		event := nextEvent(t, s)
		if event.Kind != "signal" {
			continue
		}
		path := event.Signal.(signalPath)
		if path.ProcessingSpeed == nil {
			continue
		}
		speed := *path.ProcessingSpeed
		if speed <= 0 || math.IsNaN(speed) || math.IsInf(speed, 0) || math.Abs(speed*10-math.Round(speed*10)) > 1e-9 {
			t.Fatalf("处理速度无效或未量化到 0.1：%v", speed)
		}
		var kinds []string
		for _, step := range path.Steps {
			kinds = append(kinds, step.Kind)
		}
		if want := []string{"source", "encode"}; !reflect.DeepEqual(kinds, want) || path.Quality != "lossless" {
			t.Fatalf("处理速度覆盖了信号路径：%+v", path)
		}
		return
	}
}
