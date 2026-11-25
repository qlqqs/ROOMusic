package app

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/playback/render/ffmpeg"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

type subsonicStreamFixture struct {
	*zonesSampleFixture
	library playbackLibrary
}

func newSubsonicStreamFixture(t *testing.T, opts ...Option) *subsonicStreamFixture {
	t.Helper()
	a := newIntegrationTestApplication(t, opts...)
	return &subsonicStreamFixture{
		zonesSampleFixture: &zonesSampleFixture{integrationTestApplication: a, user: setupSubsonicAdmin(t, a)},
		library:            createPlaybackLibrary(t, a),
	}
}

func streamParams(track string, extra ...string) url.Values {
	p := subsonicParameters(subsonicAdmin, subsonicPassword, "token")
	p.Set("id", track)
	for i := 0; i < len(extra); i += 2 {
		p.Set(extra[i], extra[i+1])
	}
	return p
}

func (f *subsonicStreamFixture) track(t *testing.T, spec playbackTrackSpec, seconds float64) string {
	t.Helper()
	id := f.library.addTrack(t, f.integrationTestApplication, spec)
	mustExec(t, f.integrationTestApplication, "UPDATE tracks SET duration_seconds=$1 WHERE id=$2", seconds, id)
	return id
}

func (f *subsonicStreamFixture) get(endpoint, track string, extra ...string) *httptest.ResponseRecorder {
	return requestSubsonic(f.integrationTestApplication, http.MethodGet, endpoint, streamParams(track, extra...))
}

func streamFixtureBytes(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "playback", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func streamFFmpeg(t *testing.T, input []byte, args ...string) []byte {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), requireTestFFmpeg(t), append([]string{"-v", "error", "-nostdin"}, args...)...)
	cmd.Stdin = bytes.NewReader(input)
	return streamCommandOutput(t, cmd)
}

func streamCommandOutput(t *testing.T, cmd *exec.Cmd) []byte {
	t.Helper()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s: %v: %s", filepath.Base(cmd.Path), err, stderr.Bytes())
	}
	return data
}

func generatedStreamFLAC(t *testing.T, seconds int) []byte {
	t.Helper()
	// 扫频信号让偏移一整秒也会产生不同的样本值。
	source := fmt.Sprintf("aevalsrc=0.3*sin(2*PI*(270*t+0.25*t*t)):s=44100:d=%d", seconds)
	return streamFFmpeg(t, nil, "-f", "lavfi", "-i", source, "-ac", "2", "-sample_fmt", "s16", "-c:a", "flac", "-f", "flac", "pipe:1")
}

// DSF 沿用现有夹具的容器。将 ffmpeg 合成的半秒正弦用一阶调制编码为 DSD64，
// 每声道每块 4096 字节，两个声道使用同一信号。
func streamDSF(t *testing.T) []byte {
	t.Helper()
	const rate = 2822400
	pcm := streamFFmpeg(t, nil, "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=2822400:duration=0.5", "-f", "s16le", "pipe:1")
	if len(pcm) != rate/2*2 {
		t.Fatalf("DSD 测试信号长度有误：%d 字节", len(pcm))
	}
	data := dsdFormatSample("dsf", rate)
	const header, block = 92, 4096
	var errorSum int
	for i := 0; i < len(pcm)/16; i++ {
		var value byte
		for bit := 0; bit < 8; bit++ {
			errorSum += int(int16(binary.LittleEndian.Uint16(pcm[2*(i*8+bit):])))
			if errorSum >= 0 {
				value |= 1 << (7 - bit)
				errorSum -= 32768
			} else {
				errorSum += 32768
			}
		}
		pos := header + (i/block)*2*block + i%block
		data[pos], data[pos+block] = value, value
	}
	return data
}

func streamPCM(t *testing.T, audio []byte) []byte {
	t.Helper()
	return streamFFmpeg(t, audio, "-i", "pipe:0", "-c:a", "pcm_s32le", "-f", "s32le", "pipe:1")
}

func requireStreamRender(t *testing.T, response *httptest.ResponseRecorder, mime string) []byte {
	t.Helper()
	if response.Code != 200 || response.Header().Get("Content-Type") != mime ||
		response.Header().Get("Accept-Ranges") != "none" || response.Header().Get("Content-Length") != "" || !response.Flushed {
		t.Fatalf("渲染响应 HTTP %d，头 %v，正文前缀 %q", response.Code, response.Header(), response.Body.Bytes()[:min(response.Body.Len(), 256)])
	}
	return response.Body.Bytes()
}

func TestPostgreSQLSubsonicStreamOriginalAndEqualizer(t *testing.T) {
	f := newSubsonicStreamFixture(t, withTestRenderer(t))
	c := zonesSampleCalls{t: t, f: f.zonesSampleFixture}
	source := streamFixtureBytes(t, "flac-16-44100.flac")
	id := f.track(t, playbackTrackSpec{relative: "曲目.flac", content: source, bitDepth: 16, sampleRate: 44100, channels: 2}, 3)
	full := f.get("stream", id)
	etag := full.Header().Get("ETag")
	if full.Code != 200 || !bytes.Equal(source, full.Body.Bytes()) || full.Header().Get("Content-Type") != "audio/flac" ||
		full.Header().Get("Accept-Ranges") != "bytes" || len(etag) < 3 || etag[0] != '"' || etag[len(etag)-1] != '"' {
		t.Fatalf("直发响应 HTTP %d，头 %v，字节数 %d", full.Code, full.Header(), full.Body.Len())
	}
	part := f.request(t, "GET", "/rest/stream?"+streamParams(id).Encode(), nil, nil, map[string]string{"Range": "bytes=197-513", "If-Range": etag})
	if part.Code != 206 || part.Header().Get("ETag") != etag || part.Header().Get("Content-Range") != fmt.Sprintf("bytes 197-513/%d", len(source)) || !bytes.Equal(part.Body.Bytes(), source[197:514]) {
		t.Fatalf("Range 响应 HTTP %d，头 %v，字节数 %d", part.Code, part.Header(), part.Body.Len())
	}
	client := onlyStreamDevice(t, c)
	settings := c.command("", client.ZoneID, map[string]any{"command": "set_settings", "settings": map[string]any{"peq": dspSamplePEQ()}}).Zone.Settings
	single := streamPCM(t, requireStreamRender(t, f.get("stream", id), "audio/flac"))
	if bytes.Equal(single, streamPCM(t, source)) || len(single) != 3*44100*2*4 {
		t.Fatalf("均衡输出未改变样本或时长不符，PCM 字节数 %d", len(single))
	}
	for _, method := range []string{"stream", "download"} {
		raw := f.get(method, id, "format", "raw")
		if raw.Code != 200 || !bytes.Equal(raw.Body.Bytes(), source) {
			t.Fatalf("%s 未返回原 FLAC", method)
		}
	}
	// 让网页端经正常的登记、认领和播放命令产生独立的会话渲染。
	device := zonesSampleDecode[zones.RegisterDeviceResult](t, c.call("", "POST", "/devices", map[string]any{"name": "网页试听", "kind": "browser", "volume": 1, "muted": false, "settings": settings}, 201), 201)
	caps := zonesSampleCapabilities()
	caps.StreamFormats = []zones.StreamFormatCapability{{Codec: "flac", MIMEType: "audio/flac", Lossless: true}}
	c.call("", "POST", "/devices/"+device.Device.DeviceID+"/output", map[string]any{"protocol": 1, "capabilities": caps, "takeover": false, "playing": nil}, 200)
	played := c.command("", device.Zone.ZoneID, map[string]any{"command": "play_tracks", "track_ids": []string{id}, "start_index": 0})
	if played.Zone.Stream == nil {
		t.Fatalf("网页会话没有输出流：%+v", played.Zone)
	}
	web := f.request(t, "GET", played.Zone.Stream.URL, nil, &f.user, nil)
	if web.Code != 200 || web.Header().Get("Content-Type") != "audio/flac" {
		t.Fatalf("网页流 HTTP %d，头 %v", web.Code, web.Header())
	}
	other := streamPCM(t, web.Body.Bytes())
	if !bytes.Equal(other, single) {
		t.Fatalf("同设置的网页与单曲 PCM 不一致，分别为 %d、%d 字节", len(other), len(single))
	}
}

func TestPostgreSQLSubsonicStreamFormatsAndOffsets(t *testing.T) {
	f := newSubsonicStreamFixture(t, withTestRenderer(t))
	source := generatedStreamFLAC(t, 34)
	parent := f.track(t, playbackTrackSpec{relative: "disc.flac", content: source, bitDepth: 16, sampleRate: 44100, channels: 2}, 34)
	pcm := streamPCM(t, source)
	const stride = 2 * 4
	t.Run("MP3", func(t *testing.T) {
		encoded := requireStreamRender(t, f.get("stream", parent, "maxBitRate", "128", "format", "mp3"), "audio/mpeg")
		args := []string{"-v", "error", "-select_streams", "a:0", "-show_entries", "stream=codec_name,bit_rate", "-of", "default=noprint_wrappers=1", "-i", "pipe:0"}
		var cmd *exec.Cmd
		if path, err := exec.LookPath("ffprobe"); err == nil {
			cmd = exec.CommandContext(t.Context(), path, args...)
		} else {
			// 固定的 ffmpeg 镜像也提供 ffprobe，复用已准备的镜像。
			image := strings.TrimSpace(string(streamCommandOutput(t, exec.CommandContext(t.Context(), "../../../scripts/ffmpeg-pinned.sh", "--image"))))
			cmd = exec.CommandContext(t.Context(), "docker", append([]string{"run", "--rm", "-i", "--network", "none", "--entrypoint", "/ffprobe", image}, args...)...)
		}
		cmd.Stdin = bytes.NewReader(encoded)
		fields := strings.Fields(string(streamCommandOutput(t, cmd)))
		if len(fields) != 2 || fields[0] != "codec_name=mp3" {
			t.Fatalf("ffprobe 输出 %v", fields)
		}
		rate, err := strconv.Atoi(strings.TrimPrefix(fields[1], "bit_rate="))
		if err != nil || rate < 125000 || rate > 131000 {
			t.Fatalf("ffprobe 码率 %v", fields)
		}
		t.Logf("ffprobe 检出 MP3，码率 %d bit/s", rate)
	})
	t.Run("offset", func(t *testing.T) {
		got := streamPCM(t, requireStreamRender(t, f.get("stream", parent, "timeOffset", "30"), "audio/flac"))
		if !bytes.Equal(got, pcm[30*44100*stride:]) {
			t.Fatalf("30 秒后的 PCM 不符，字节数 %d", len(got))
		}
	})
	for _, tc := range []struct {
		name, kind string
		rate, bits int
		seconds    float64
		source     []byte
	}{
		{"ape", "ape_apev2", 44100, 16, 0.25, streamFixtureBytes(t, "ape-16-44100.ape")},
		{"dsf", "dsf_id3", 2822400, 1, 0.5, streamDSF(t)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := f.track(t, playbackTrackSpec{relative: "source." + tc.name, content: tc.source, sourceKind: tc.kind, codec: tc.name, bitDepth: tc.bits, sampleRate: tc.rate, channels: 2}, tc.seconds)
			audio := requireStreamRender(t, f.get("stream", id), "audio/flac")
			if !bytes.HasPrefix(audio, []byte("fLaC")) {
				t.Fatalf("响应缺少 FLAC 容器头")
			}
			decoded := streamFFmpeg(t, audio, "-i", "pipe:0", "-ar", "8000", "-ac", "1", "-f", "s16le", "pipe:1")
			if diff := len(decoded) - int(tc.seconds*8000*2); diff < -32 || diff > 32 {
				t.Fatalf("解码所得时长 %.6f，期望 %.6f 秒", float64(len(decoded))/16000, tc.seconds)
			}
			var peak int
			for i := 0; i < len(decoded); i += 2 {
				peak = max(peak, int(int16(binary.LittleEndian.Uint16(decoded[i:]))))
			}
			if peak < 256 {
				t.Fatalf("%s 解码信号峰值过低：%d", tc.name, peak)
			}
			raw := f.get("download", id)
			if raw.Code != 200 || !bytes.Equal(raw.Body.Bytes(), tc.source) {
				t.Fatalf("下载 %s 与源文件不同，HTTP %d，字节数 %d", tc.name, raw.Code, raw.Body.Len())
			}
		})
	}
}

func TestPostgreSQLSubsonicStreamCUE(t *testing.T) {
	f := newSubsonicStreamFixture(t, withTestRenderer(t))
	source := streamFixtureBytes(t, "flac-16-44100.flac")
	if err := os.WriteFile(filepath.Join(f.library.root, "disc.flac"), source, 0600); err != nil {
		t.Fatal(err)
	}
	const cue = `TITLE "合成分轨"
PERFORMER "合成艺人"
FILE "disc.flac" FLAC
 TRACK 01 AUDIO
  TITLE "前段"
  INDEX 01 00:00:00
 TRACK 02 AUDIO
  TITLE "中段"
  INDEX 01 00:00:08
 TRACK 03 AUDIO
  TITLE "尾段"
  INDEX 01 00:02:08
`
	if err := os.WriteFile(filepath.Join(f.library.root, "album.cue"), []byte(cue), 0600); err != nil {
		t.Fatal(err)
	}
	runScan(t, f.integrationTestApplication, &f.user)
	var id string
	if err := f.database.QueryRow("SELECT id FROM tracks WHERE source_root_id=$1 AND source_kind='cue_virtual' AND title='中段'", f.library.rootID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	// 75 Hz 的分轨边界映射到 44.1 kHz 双声道的完整 PCM 样本帧。
	const stride = 2 * 4
	const first, last = 8 * (44100 / 75) * stride, 158 * (44100 / 75) * stride
	pcm := streamPCM(t, source)
	check := func(method string, offset int, extra ...string) {
		t.Helper()
		got := streamPCM(t, requireStreamRender(t, f.get(method, id, extra...), "audio/flac"))
		want := pcm[first+offset*44100*stride : last]
		if !bytes.Equal(got, want) {
			t.Fatalf("%s CUE 起止帧有误，实际 %d 字节，期望 %d", method, len(got), len(want))
		}
	}
	check("stream", 0)
	check("stream", 1, "timeOffset", "1")
	c := zonesSampleCalls{t: t, f: f.zonesSampleFixture}
	c.command("", onlyStreamDevice(t, c).ZoneID, map[string]any{"command": "set_settings", "settings": map[string]any{"peq": dspSamplePEQ()}})
	check("download", 0)
	check("stream", 0, "format", "raw")
}

func TestPostgreSQLSubsonicStreamSpeedAndCapacity(t *testing.T) {
	tool, err := ffmpeg.Open(t.Context(), requireTestFFmpeg(t))
	if err != nil {
		t.Fatal(err)
	}
	tool = tool.Independent(2)
	f := newSubsonicStreamFixture(t, WithRenderer(tool))
	id := f.track(t, playbackTrackSpec{relative: "long.flac", content: generatedStreamFLAC(t, 300), bitDepth: 16, sampleRate: 44100, channels: 2}, 300)
	slots, err := tool.TryReserve(2)
	if err != nil {
		t.Fatal(err)
	}
	defer slots.Close()
	start := time.Now()
	busy := f.get("stream", id, "timeOffset", "0")
	took := time.Since(start)
	code := 0
	detail := requireSubsonicJSON(t, busy, 200, &code)["error"].(map[string]any)
	if took > time.Second || detail["message"] != "服务器忙" {
		t.Fatalf("名额耗尽后 %v 返回 %v", took, detail)
	}
	slots.Close()
	start = time.Now()
	audio := requireStreamRender(t, f.get("stream", id, "timeOffset", "0"), "audio/flac")
	took = time.Since(start)
	t.Logf("五分钟曲目整首输出用时 %s", took)
	if took > 10*time.Second {
		t.Fatalf("渲染五分钟曲目用时 %v，超过 10 秒", took)
	}
	pcm := streamFFmpeg(t, audio, "-i", "pipe:0", "-ar", "8000", "-ac", "1", "-f", "s16le", "pipe:1")
	if len(pcm) != 300*8000*2 {
		t.Fatalf("五分钟流的实际时长 %.3f 秒", float64(len(pcm))/16000)
	}
	mustExec(t, f.integrationTestApplication, "UPDATE library_roots SET status='disabled' WHERE id=$1", f.library.rootID)
	for _, extra := range [][]string{nil, {"timeOffset", "1"}} {
		requireSubsonicFailure(t, f.get("stream", id, extra...), 200, 70)
	}
}
