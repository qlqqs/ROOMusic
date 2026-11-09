package render

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/playback/render/ffmpeg"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

func TestRenderSession(t *testing.T) {
	ff := pinnedFFmpeg(t)
	tool := openTestTool(t, ff, ffmpeg.Options{})
	a := makeFLAC(t, ff, 16, 44100, 997, 0.6)
	b := makeFLAC(t, ff, 16, 44100, 733, 0.6)
	high := makeFLAC(t, ff, 24, 44100, 1193, 0.6)
	source := fixtureSource(map[string]audioFixture{"a": a, "b": b, "high": high})

	for _, tc := range []struct {
		name, first string
		cursor      zones.Cursor
		pcm         []byte
		bits        int
		dsdCodec    string
		slots       int
	}{
		{name: "16位逐位一致", first: "a", pcm: a.pcm, bits: 16},
		{name: "24位逐位一致", first: "high", pcm: high.pcm, bits: 24},
		{name: "同格式无缝拼接", first: "a", cursor: testCursor(nextB), pcm: append(append([]byte(nil), a.pcm...), b.pcm...), bits: 16},
		{name: "DSD到WAV零进程", dsdCodec: "wav", slots: 0},
		{name: "DSD到FLAC仅编码名额", dsdCodec: "flac", slots: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.dsdCodec != "" {
				testDSDSessionCapacityAndClocks(t, tool, tc.dsdCodec, tc.slots)
				return
			}
			r := newTestRenderer(t, tool, source)
			spec := renderSpec("flac")
			spec.Start.TrackID, spec.Cursor = tc.first, tc.cursor
			s := openTestSession(t, r, spec)
			if got := s.Format(); got.BitDepth != tc.bits || got.Codec != "flac" {
				t.Fatalf("Format = %+v", got)
			}
			encoded := readBody(t, s)
			pcm := ffmpegOutput(t, ff, encoded, "-i", "pipe:0", "-map", "0:a:0", "-c:a", "pcm_s32le", "-f", "s32le", "pipe:1")
			if got, want := md5.Sum(pcm), md5.Sum(tc.pcm); got != want {
				t.Fatalf("PCM MD5 = %x, want %x（字节 %d/%d）", got, want, len(pcm), len(tc.pcm))
			}
			events := eventsThrough(t, s, "signal", 0)
			path := events[len(events)-1].Signal.(signalPath)
			if len(path.Steps) == 0 {
				t.Fatal("信号路径为空")
			}
			for _, step := range path.Steps {
				if step.Kind == "dither" {
					t.Fatalf("未处理的整数源不应出现抖动：%+v", path)
				}
			}
			data, err := json.Marshal(path)
			if err != nil {
				t.Fatal(err)
			}
			var snapshot map[string]json.RawMessage
			if err := json.Unmarshal(data, &snapshot); err != nil {
				t.Fatal(err)
			}
			if _, exists := snapshot["clipping"]; exists {
				t.Fatalf("未启用削波指示时不应包含 clipping：%s", data)
			}
		})
	}

	t.Run("播放头网格与换轨流尾顺序", func(t *testing.T) {
		r := newTestRenderer(t, tool, source)
		spec := renderSpec("flac")
		spec.Cursor = testCursor(nextB)
		s := openTestSession(t, r, spec)
		readBody(t, s)
		waitUntil(t, "渲染尾部屏障", func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.renderComplete })
		events := eventsThrough(t, s, "time", 0)
		observe(s, 0.45)
		events = append(events, eventsThrough(t, s, "time", 0.4)...)
		for _, event := range events {
			if event.Kind == "track_started" {
				t.Fatalf("播放头未到边界却已换轨：%+v", event)
			}
		}
		// 回退后立即推进到边界；如果重发旧网格，完整事件序列的断言会失败。
		observe(s, 0.2)
		if event, ok := s.dueEvent(testNow); ok {
			t.Errorf("回退后重发事件：%+v", event)
		}
		observe(s, 0.6)
		events = append(events, eventsThrough(t, s, "time", 0.6)...)
		observe(s, 1.2)
		events = append(events, eventsThrough(t, s, "stream_ended", 0)...)
		var times []string
		started := -1
		lastTime := -1
		for i, event := range events {
			switch event.Kind {
			case "time":
				times = append(times, fmt.Sprintf("%s:%.1f/%.1f", event.EntryID, event.StreamSeconds, event.Seconds))
				lastTime = i
			case "track_started":
				if started >= 0 || event.EntryID != "entry-b" || event.StreamSeconds != 0.6 || event.Transition != "gapless" {
					t.Errorf("换轨事件：%+v", event)
				}
				started = i
			}
		}
		want := []string{"entry-a:0.0/0.0", "entry-a:0.2/0.2", "entry-a:0.4/0.4", "entry-b:0.6/0.0", "entry-b:0.8/0.2", "entry-b:1.0/0.4", "entry-b:1.2/0.6"}
		if !reflect.DeepEqual(times, want) {
			t.Errorf("time = %v, want %v", times, want)
		}
		if started < 0 {
			t.Error("缺少第二首 track_started")
		}
		if lastTime != len(events)-2 || events[lastTime].StreamSeconds != 1.2 {
			t.Errorf("流尾不是先 time 后 stream_ended：%+v", events)
		}
	})

	t.Run("异格式立即通知接续流", func(t *testing.T) {
		r := newTestRenderer(t, tool, source)
		spec := renderSpec("flac")
		spec.Cursor = testCursor(func(after string) (zones.Entry, bool) {
			return zones.Entry{EntryID: "entry-high", TrackID: "high"}, after == "entry-a"
		})
		s := openTestSession(t, r, spec)
		// 不消费 Body，也不推进播放头；预选下一首时就必须收到边界。
		events := eventsThrough(t, s, "format_boundary", 0)
		event := events[len(events)-1]
		if event.NextEntryID != "entry-high" || event.Reason != "format" || s.playhead() != 0 {
			t.Fatalf("格式边界：%+v，播放头 %d", event, s.playhead())
		}
	})

	t.Run("首曲失败返回空流并按顺序接续", func(t *testing.T) {
		r := newTestRenderer(t, tool, source)
		spec := renderSpec("flac")
		spec.Start.TrackID = "missing"
		spec.Cursor = testCursor(func(after string) (zones.Entry, bool) {
			if after == "entry-a" {
				return zones.Entry{EntryID: "entry-bad", TrackID: "also-missing"}, true
			}
			return zones.Entry{EntryID: "entry-b", TrackID: "b"}, after == "entry-bad"
		})
		s := openTestSession(t, r, spec)
		if got := s.Format(); got != (zones.StreamFormat{}) {
			t.Errorf("失败首曲 Format 应为零值，得到 %+v", got)
		}
		data := readBody(t, s)
		if len(data) != 0 {
			t.Errorf("失败首曲 Body 应立即 EOF，得到 %d 字节", len(data))
		}
		// 同时收齐两种事件，不因错误的先后顺序提前停止检查。
		var got []string
		boundary, failures := false, 0
		for !boundary || failures < 2 {
			event := nextEvent(t, s)
			switch event.Kind {
			case "track_failed":
				failures++
				got = append(got, event.Kind+":"+event.EntryID)
				if event.Reason != "not_found" {
					t.Errorf("失败分类：%+v", event)
				}
			case "format_boundary":
				boundary = true
				got = append(got, event.Kind+":"+event.NextEntryID)
				if event.NextEntryID != "entry-b" {
					t.Errorf("没有跳过不可播条目：%+v", event)
				}
			}
		}
		want := []string{"track_failed:entry-a", "track_failed:entry-bad", "format_boundary:entry-b"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("失败首曲事件顺序 = %v, want %v", got, want)
		}
	})

	t.Run("定位从StartSeconds起算", func(t *testing.T) {
		r := newTestRenderer(t, tool, source)
		spec := renderSpec("flac")
		spec.StartSeconds = 0.2
		s := openTestSession(t, r, spec)
		readBody(t, s)
		events := eventsThrough(t, s, "time", 0)
		event := events[len(events)-1]
		if event.Seconds != 0.2 || event.EntryID != "entry-a" {
			t.Fatalf("首条定位 time：%+v", event)
		}
	})

	t.Run("Cursor持锁时关闭不死锁且Renderer等待退出", func(t *testing.T) {
		r := newTestRenderer(t, tool, source)
		var zoneLock sync.Mutex
		zoneLock.Lock()
		unlocked := false
		// 清理时先释放锁，再让 Renderer.Close 等待会话退出。
		defer func() {
			if !unlocked {
				zoneLock.Unlock()
			}
		}()
		entered := make(chan struct{})
		spec := renderSpec("flac")
		spec.Cursor = testCursor(func(string) (zones.Entry, bool) {
			close(entered)
			zoneLock.Lock()
			defer zoneLock.Unlock()
			return zones.Entry{}, false
		})
		s := openTestSession(t, r, spec)
		select {
		case <-entered:
		case <-time.After(testWait):
			t.Fatal("Cursor 未进入")
		}
		closed := make(chan struct{})
		go func() { s.Close(); close(closed) }()
		select {
		case <-closed:
		case <-time.After(time.Second):
			t.Fatal("持区锁时会话 Close 阻塞")
		}
		rendererClosed := make(chan struct{})
		go func() { r.Close(); close(rendererClosed) }()
		waitUntil(t, "Renderer 开始关闭", func() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.closed })
		select {
		case <-rendererClosed:
			t.Fatal("Cursor 未退出时 Renderer.Close 已返回")
		default:
		}
		zoneLock.Unlock()
		unlocked = true
		select {
		case <-rendererClosed:
		case <-time.After(testWait):
			t.Fatal("释放区锁后 Renderer.Close 未返回")
		}
		select {
		case <-s.done:
		default:
			t.Fatal("Renderer.Close 未等待会话退出")
		}
		var one [1]byte
		if n, err := s.Body().Read(one[:]); n != 0 || err != io.EOF {
			t.Errorf("关闭后 Body = %d, %v", n, err)
		}
		for range s.Events() {
		}
	})
}
