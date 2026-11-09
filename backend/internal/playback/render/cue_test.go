package render

import (
	"fmt"
	"math"
	"path/filepath"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/playback/render/ffmpeg"
)

func TestRenderCUEAdjacentTracksBitExact(t *testing.T) {
	ff := pinnedFFmpeg(t)
	tool := openTestTool(t, ff, ffmpeg.Options{})
	for _, tc := range []struct {
		name, codec, extension, sampleFormat string
		rate, bits                           int
		start, cut                           int64
	}{
		// 75 Hz 索引 901、1202；明确写出来源边界，索引换算由 playback 包另测。
		{"WavPack_44100", "wavpack", "wv", "s16p", 44100, 16, 529788, 706776},
		// 32 kHz 的两个索引均非整除，分别向下对齐，不能按区间长度取整。
		{"FLAC_32000", "flac", "flac", "s32", 32000, 24, 384426, 512853},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "parent."+tc.extension)
			// 频率随时间变化且左右声道不同，避免整秒周期掩盖粗定位偏移。
			ffmpegOutput(t, ff, nil, "-f", "lavfi", "-i", fmt.Sprintf("aevalsrc=0.31*sin(2*PI*(997*t+7*t*t))|0.23*sin(2*PI*(733*t+11*t*t)):s=%d:d=20", tc.rate), "-c:a", tc.codec, "-sample_fmt", tc.sampleFormat, path)
			whole := audioFixture{
				path: path,
				track: Track{SourceKind: "file", Codec: tc.codec, SampleRate: tc.rate,
					Channels: 2, BitDepth: tc.bits, DurationSeconds: 20},
				pcm: ffmpegOutput(t, ff, nil, "-i", path, "-map", "0:a:0", "-c:a", "pcm_s32le", "-f", "s32le", "pipe:1"),
			}
			// 两条虚拟轨都重新打开同一个整轨文件，不生成已截好的分轨文件。
			a := cueFixture(whole, tc.start, &tc.cut)
			b := cueFixture(whole, tc.cut, nil)
			r := newTestRenderer(t, tool, fixtureSource(map[string]audioFixture{"a": a, "b": b}))
			spec := renderSpec("flac")
			spec.Cursor = testCursor(nextB)
			s := openTestSession(t, r, spec)
			if got := s.Format(); got.Codec != "flac" || got.SampleRate != tc.rate || got.BitDepth != tc.bits || got.Channels != 2 {
				t.Fatalf("无处理的输出格式变化：%+v", got)
			}
			pcm := transitionPCM(t, ff, readBody(t, s))
			assertTransitionPCM(t, pcm, whole.pcm[tc.start*8:])
			waitRenderComplete(t, s)
			observe(s, float64(len(pcm)/8)/float64(tc.rate))
			events := eventsThrough(t, s, "stream_ended", 0)
			starts := 0
			for _, event := range events {
				switch event.Kind {
				case "track_failed", "format_boundary", "rebuild_needed":
					t.Errorf("CUE 无缝渲染出现意外事件：%+v", event)
				case "track_started":
					starts++
					if event.EntryID != "entry-b" || event.Transition != "gapless" || event.StreamSeconds != float64(tc.cut-tc.start)/float64(tc.rate) {
						t.Errorf("相邻 CUE 轨的共享切点错误：%+v", event)
					}
				}
			}
			if starts != 1 {
				t.Errorf("track_started 数量 = %d，期望 1", starts)
			}
		})
	}
}

func TestRenderCUEOffsetTrackPositionAndSeek(t *testing.T) {
	ff := pinnedFFmpeg(t)
	tool := openTestTool(t, ff, ffmpeg.Options{})
	whole := makeFLAC(t, ff, 24, 32000, 997, 16)
	// 索引 901、1127 的源样本边界；起点在父文件第 12 秒之后，终点在 EOF 之前。
	const start int64 = 384426
	end := int64(480853)
	track := cueFixture(whole, start, &end)
	for _, tc := range []struct {
		name         string
		startSeconds float64
		seekFrames   int64
	}{
		{"曲内起点", 0, 0},
		// 0.437516 × 32000 = 14000.512，按合同四舍五入到 14001 帧。
		{"曲内定位到非整样本秒数", 0.437516, 14001},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRenderer(t, tool, fixtureSource(map[string]audioFixture{"a": track}))
			spec := renderSpec("flac")
			spec.StartSeconds = tc.startSeconds
			s := openTestSession(t, r, spec)
			pcm := transitionPCM(t, ff, readBody(t, s))
			assertTransitionPCM(t, pcm, whole.pcm[(start+tc.seekFrames)*8:end*8])
			// RenderSession 不单独发布时长；以实际 PCM 时长和流尾事件核对 DurationSeconds。
			duration := float64(len(pcm)/8) / 32000
			wantDuration := track.track.DurationSeconds - float64(tc.seekFrames)/32000
			if math.Abs(duration-wantDuration) > 1e-12 {
				t.Errorf("曲内渲染时长 = %.12f，期望 %.12f", duration, wantDuration)
			}
			waitRenderComplete(t, s)
			observe(s, duration)
			events := eventsThrough(t, s, "stream_ended", 0)
			times, signals := 0, 0
			for _, event := range events {
				switch event.Kind {
				case "time":
					wantStream := float64(times*6400) / 32000
					if event.EntryID != "entry-a" || event.StreamSeconds != wantStream || math.Abs(event.Seconds-(tc.startSeconds+wantStream)) > 1e-12 {
						t.Errorf("位置应为曲内坐标，不应包含父文件偏移：%+v", event)
					}
					times++
				case "signal":
					if len(event.Segments) == 0 {
						continue
					}
					signals++
					seg := event.Segments[0]
					if len(event.Segments) != 1 || seg.EntryID != "entry-a" || seg.TrackID != "a" || seg.StreamStartSeconds != 0 || seg.TrackStartSeconds != tc.startSeconds {
						t.Errorf("CUE 分段应从曲内定位点开始：%+v", event.Segments)
					}
					path, ok := event.Signal.(signalPath)
					if !ok || path.Quality != "lossless" || len(path.Steps) != 2 {
						t.Fatalf("关闭处理后应只有无损来源与编码：%+v", event.Signal)
					}
					if source := path.Steps[0]; source.Kind != "source" || source.Params["codec"] != whole.track.Codec || source.Lossy || source.Compat {
						t.Errorf("信号来源应显示父音频编码，而不是 CUE：%+v", source)
					}
					if path.Steps[1].Kind != "encode" {
						t.Errorf("无处理输出缺少编码步骤：%+v", path)
					}
				case "stream_ended":
					if math.Abs(event.StreamSeconds-wantDuration) > 1e-12 {
						t.Errorf("CUE 曲内流尾位置错误：%+v，期望 %.12f", event, wantDuration)
					}
				case "track_started", "track_failed", "format_boundary", "rebuild_needed":
					t.Errorf("单轨渲染出现意外事件：%+v", event)
				}
			}
			if want := int((end-start-tc.seekFrames)/6400) + 1; times != want || signals != 1 {
				t.Errorf("曲内事件数量：time=%d（期望 %d），分段 signal=%d（期望 1）", times, want, signals)
			}
		})
	}
}

// cueFixture 只提供 TrackSource 已换算的源样本边界；不复制生产 75 Hz 索引换算逻辑。
func cueFixture(whole audioFixture, start int64, end *int64) audioFixture {
	track := whole.track
	track.SourceKind, track.ReleaseID = "cue_virtual", "cue-release"
	track.CueStartFrame, track.CueEndFrame = &start, end
	stop := int64(len(whole.pcm) / (track.Channels * 4))
	if end != nil {
		stop = *end
	}
	track.DurationSeconds = float64(stop-start) / float64(track.SampleRate)
	return audioFixture{path: whole.path, track: track}
}
