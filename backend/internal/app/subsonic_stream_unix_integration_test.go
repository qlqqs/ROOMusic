//go:build unix

package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/playback/render/ffmpeg"
)

// 替身记录本次请求的子进程，exec 后 PID 属于真实 ffmpeg；只在能力查询时隐藏 LAME。
func streamFFmpegWrapper(t *testing.T, hideLAME bool) (*ffmpeg.Tool, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ffmpeg")
	real, err := filepath.Abs(requireTestFFmpeg(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, path+".real"); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n"
	if hideLAME {
		script += `for flag do
	if [ "$flag" = "-encoders" ]; then
		"$0.real" "$@" | sed '/libmp3lame/d'
		exit $?
	fi
done
`
	}
	script += `echo "$$" >> "$0.pids"
exec "$0.real" "$@"
`
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	tool, err := ffmpeg.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path+".pids", 0); err != nil {
		t.Fatal(err)
	}
	return tool.Independent(2), path + ".pids"
}

func TestPostgreSQLSubsonicStreamMissingLAME(t *testing.T) {
	tool, _ := streamFFmpegWrapper(t, true)
	if tool.HasEncoder("libmp3lame") || !tool.HasEncoder("libopus") {
		t.Fatal("能力替身应仅隐藏 LAME")
	}
	f := newSubsonicStreamFixture(t, WithRenderer(tool))
	id := f.track(t, playbackTrackSpec{relative: "fallback.flac", content: streamFixtureBytes(t, "flac-16-44100.flac"), bitDepth: 16, sampleRate: 44100, channels: 2}, 3)
	audio := requireStreamRender(t, f.get("stream", id, "format", "mp3"), `audio/ogg; codecs="opus"`)
	if !bytes.HasPrefix(audio, []byte("OggS")) || !bytes.Contains(audio, []byte("OpusHead")) {
		t.Fatal("回退响应缺少 Ogg Opus 标识")
	}
	decoded := streamFFmpeg(t, audio, "-i", "pipe:0", "-ac", "1", "-ar", "8000", "-f", "s16le", "pipe:1")
	if len(decoded) != 3*8000*2 {
		t.Fatalf("回退后的 Opus 解码时长为 %.6f 秒", float64(len(decoded))/16000)
	}
}

func TestPostgreSQLSubsonicStreamDisconnect(t *testing.T) {
	tool, pidFile := streamFFmpegWrapper(t, false)
	f := newSubsonicStreamFixture(t, WithRenderer(tool))
	id := f.track(t, playbackTrackSpec{relative: "cancel.flac", content: generatedStreamFLAC(t, 30), bitDepth: 16, sampleRate: 44100, channels: 2}, 30)
	// 首块真实音频刷新到 TCP 连接后挂起写出；等客户端关闭连接再解除背压。
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writer := newBlockingResponseWriter(r.Context())
		writer.forward = w
		stop := context.AfterFunc(r.Context(), writer.unblock)
		defer stop()
		f.handler.ServeHTTP(writer, r)
	}))
	defer server.Close()
	response, err := server.Client().Get(server.URL + "/rest/stream?" + streamParams(id, "timeOffset", "0").Encode())
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || response.Header.Get("Content-Type") != "audio/flac" {
		t.Fatalf("取流失败：HTTP %d，%v", response.StatusCode, response.Header)
	}
	if _, err := io.CopyN(io.Discard, response.Body, 1); err != nil {
		t.Fatal(err)
	}
	log, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(log))
	if len(fields) != 2 {
		t.Fatalf("解码和编码应各启动一个 ffmpeg，实际 PID %q", fields)
	}
	var pids []int
	for _, field := range fields {
		pid, err := strconv.Atoi(field)
		if err != nil || pid <= 0 || syscall.Kill(pid, 0) != nil {
			t.Fatalf("断开前进程 %q 未运行", field)
		}
		pids = append(pids, pid)
	}
	if slots, err := tool.TryReserve(1); err == nil {
		slots.Close()
		t.Fatal("流活跃时仍有空闲名额")
	}
	start := time.Now()
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	for {
		alive := false
		for _, pid := range pids {
			switch err := syscall.Kill(pid, 0); {
			case err == nil:
				alive = true
			case errors.Is(err, syscall.ESRCH):
				continue
			default:
				t.Fatalf("检查进程 %d：%v", pid, err)
			}
		}
		if !alive {
			if slots, err := tool.TryReserve(2); err == nil {
				slots.Close()
				break
			}
		}
		if time.Since(start) >= time.Second {
			t.Fatalf("断开一秒后编码、解码进程或名额仍未释放，PID %v", pids)
		}
		time.Sleep(5 * time.Millisecond)
	}
	elapsed := time.Since(start)
	if elapsed > time.Second {
		t.Fatalf("释放耗时 %s，超过一秒", elapsed)
	}
	t.Logf("断开后两个 ffmpeg 进程退出且名额可复用，用时 %s", elapsed)
}
