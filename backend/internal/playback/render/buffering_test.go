package render

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/playback/render/ffmpeg"
)

// 只替换会话的解码 stdout；探测、自检仍交给固定版本的真实 ffmpeg。
// 文件闸门在测试确认状态后放行，避免靠长时间 sleep 模拟预缓冲或超时。
func gatedDecoder(t *testing.T, ff string, prefix, suffix []byte, hangAfter bool) (*ffmpeg.Tool, *timerControl, string, string) {
	t.Helper()
	return gatedTrackDecoder(t, ff, prefix, suffix, hangAfter, "")
}

// onlyPath 非空时只拦截指定来源，其余来源及编码仍由真实 ffmpeg 处理。
func gatedTrackDecoder(t *testing.T, ff string, prefix, suffix []byte, hangAfter bool, onlyPath string) (*ffmpeg.Tool, *timerControl, string, string) {
	t.Helper()
	dir := t.TempDir()
	marker, release, started := filepath.Join(dir, "enabled"), filepath.Join(dir, "release"), filepath.Join(dir, "started")
	first, rest := filepath.Join(dir, "first.pcm"), filepath.Join(dir, "rest.pcm")
	for path, data := range map[string][]byte{first: prefix, rest: suffix} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	script := fmt.Sprintf(`#!/bin/sh
case " $* " in
 *" -i file:/dev/fd/3 "*)
  if [ -e %s ] && { [ -z %s ] || [ "$(readlink /proc/$$/fd/3)" = %s ]; }; then
   : > %s
   cat %s
   while [ ! -e %s ]; do sleep 0.01; done
   cat %s
   %s
   exit 0
  fi
 ;;
esac
exec %s "$@"
`, shellQuote(marker), shellQuote(onlyPath), shellQuote(onlyPath), shellQuote(started), shellQuote(first), shellQuote(release), shellQuote(rest), map[bool]string{true: "while :; do sleep 1; done", false: ":"}[hangAfter], shellQuote(ff))
	path := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	timers := &timerControl{}
	tool := openTestTool(t, path, ffmpeg.Options{NewTimer: timers.newTimer})
	if err := os.WriteFile(marker, nil, 0600); err != nil {
		t.Fatal(err)
	}
	timers.armed.Store(true)
	return tool, timers, release, started
}

func TestRenderPrebuffer(t *testing.T) {
	ff := pinnedFFmpeg(t)
	fixture := makeFLAC(t, ff, 16, 8000, 997, 4)
	// 环容量 = 8000 × 2 × 4 × 10；恰好少一帧到 33%。
	threshold := 8000 * 2 * 4 * 10 * 33 / 100
	for _, tc := range []struct {
		name   string
		prefix int
		suffix []byte
		hang   bool
	}{
		{"33%之前不出字节_达到阈值后出", threshold - 8, fixture.pcm[threshold-8 : threshold], true},
		{"短曲EOF解除预缓冲等待", 8000 * 2 * 4 / 5, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tool, _, release, _ := gatedDecoder(t, ff, fixture.pcm[:tc.prefix], tc.suffix, tc.hang)
			r := newTestRenderer(t, tool, fixtureSource(map[string]audioFixture{"a": fixture}))
			s := openTestSession(t, r, renderSpec("wav"))
			ring := s.player.current.ring
			waitUntil(t, "预缓冲填满测试前缀", func() bool { ring.mu.Lock(); defer ring.mu.Unlock(); return ring.size == tc.prefix })
			type result struct {
				n   int
				err error
			}
			firstRead := make(chan result, 1)
			go func() { var data [1]byte; n, err := s.Body().Read(data[:]); firstRead <- result{n, err} }()
			select {
			case got := <-firstRead:
				t.Fatalf("33%% 前 Body 已返回：%+v", got)
			case <-time.After(30 * time.Millisecond):
			}
			if err := os.WriteFile(release, nil, 0600); err != nil {
				t.Fatal(err)
			}
			select {
			case got := <-firstRead:
				if got.n != 1 || got.err != nil {
					t.Fatalf("就绪后 Body 首字节：%+v", got)
				}
			case <-time.After(testWait):
				t.Fatal("达到阈值或 EOF 后仍无字节")
			}
		})
	}
}

func TestRenderDecodeTimeout(t *testing.T) {
	ff := pinnedFFmpeg(t)
	fixture := makeFLAC(t, ff, 16, 8000, 997, 0.2)
	for _, tc := range []struct {
		name   string
		prefix []byte
	}{
		{"起始10秒无输出", nil},
		{"已有输出后停滞10秒", fixture.pcm[:8]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tool, timers, _, started := gatedDecoder(t, ff, tc.prefix, nil, true)
			r := newTestRenderer(t, tool, fixtureSource(map[string]audioFixture{"a": fixture}))
			s := openTestSession(t, r, renderSpec("wav"))
			ring := s.player.current.ring
			waitUntil(t, "假解码器启动", func() bool { _, err := os.Stat(started); return err == nil })
			waitUntil(t, "已有 PCM 已进入缓冲", func() bool { ring.mu.Lock(); defer ring.mu.Unlock(); return ring.size == len(tc.prefix) })
			// 停滞用例等待消费完初始数据后的下一次读取计时器，不能触发起始计时器冒充停滞。
			if len(tc.prefix) > 0 {
				waitUntil(t, "起始计时结束并开始阻塞读取", func() bool {
					timers.mu.Lock()
					defer timers.mu.Unlock()
					count := 0
					for _, timer := range timers.timers {
						if timer.duration == 10*time.Second {
							count++
						}
					}
					return count >= 3 && timers.timers[len(timers.timers)-1].duration == 10*time.Second && !timers.timers[len(timers.timers)-1].stopped.Load()
				})
			}
			timers.fire(t, 10*time.Second)
			readBody(t, s)
			observe(s, 1)
			events := eventsThrough(t, s, "track_failed", 0)
			event := events[len(events)-1]
			if event.EntryID != "entry-a" || event.Reason != "decode_failed" {
				t.Fatalf("超时失败事件：%+v", event)
			}
		})
	}
}

func TestRenderOpenProcessCapacityTimeout(t *testing.T) {
	ff := pinnedFFmpeg(t)
	timers := &timerControl{}
	tool := openTestTool(t, ff, ffmpeg.Options{NewTimer: timers.newTimer})
	slots, err := tool.Reserve(context.Background(), 3*runtime.GOMAXPROCS(0))
	if err != nil {
		t.Fatal(err)
	}
	defer slots.Close()
	r := newTestRenderer(t, tool, fixtureSource(nil))
	timers.armed.Store(true)
	result := make(chan error, 1)
	go func() {
		s, err := r.Open(context.Background(), renderSpec("flac"))
		if s != nil {
			s.Close()
		}
		result <- err
	}()
	timers.fire(t, 5*time.Second)
	select {
	case err := <-result:
		if !errors.Is(err, ErrBusy) || !errors.Is(err, ffmpeg.ErrBusy) {
			t.Fatalf("Open 名额超时 = %v", err)
		}
	case <-time.After(testWait):
		t.Fatal("注入 5 秒超时后 Open 未返回")
	}
}
