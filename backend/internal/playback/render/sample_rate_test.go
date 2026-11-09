package render

import (
	"context"
	"math"
	"reflect"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/dsp/chain"
	"github.com/qlqq/roomusic/backend/internal/playback/render/ffmpeg"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

// 用户节点只观察实际收到的帧数，不自行变率或生成样本。
type sampleRateUserProbe struct{ frames int64 }

func (p *sampleRateUserProbe) Process(b *chain.Buffer) *chain.Buffer {
	p.frames += int64(b.Frames)
	return b
}
func (p *sampleRateUserProbe) Reset() { p.frames = 0 }
func (*sampleRateUserProbe) Step() chain.SignalStep {
	return chain.SignalStep{Kind: "test_user", Quality: "enhanced"}
}

func TestRenderSampleRateAAC(t *testing.T) {
	testRenderSampleRate(t, "aac", 192000, 384000)
}

func TestRenderSampleRateOpus(t *testing.T) {
	testRenderSampleRate(t, "opus", 44100, 0)
}

func testRenderSampleRate(t *testing.T, codec string, sourceRate, customRate int) {
	t.Helper()
	ff := pinnedFFmpeg(t)
	tool := openTestTool(t, ff, ffmpeg.Options{})
	fixture := makeFLAC(t, ff, 24, sourceRate, 997, 0.303)
	r := newTestRenderer(t, tool, fixtureSource(map[string]audioFixture{"a": fixture}))
	spec := renderSpec(codec)
	if customRate != 0 {
		// 即使用户要求更高采样率，有损交付仍须在用户处理之前降至 48 kHz。
		document, err := zones.NewSampleRateDocument(zones.SampleRateSettings{
			Strategy: "custom", CustomRate: &customRate, Filter: "linear_precise",
		})
		if err != nil {
			t.Fatal(err)
		}
		spec.Settings.SampleRate = document
	}
	track, err := r.options.Sources.Prepare(context.Background(), spec.UserID, spec.Start.TrackID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = track.File.Close() })
	format, plan, err := concreteFormat(spec, track)
	if err != nil {
		t.Fatal(err)
	}
	probe := &sampleRateUserProbe{}
	plan.user = []chain.Stage{{Node: probe, In: plan.outputPCM, Out: plan.outputPCM}}
	s, err := newSession(context.Background(), r, spec, format, plan, track, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.start()
	t.Cleanup(func() { s.Close(); <-s.done })

	encoded := readBody(t, s)
	waitRenderComplete(t, s)
	// 等渲染完成后再读链和观察节点，避免与启动协程中的 Build 并发。
	if s.sourcePCM.SampleRate != sourceRate || s.outputPCM.SampleRate != 48000 || s.Format().SampleRate != 48000 {
		t.Fatalf("源/链输出/交付采样率=%d/%d/%d", s.sourcePCM.SampleRate, s.outputPCM.SampleRate, s.Format().SampleRate)
	}
	if s.chain.InputFormat() != s.sourcePCM || s.chain.OutputFormat() != s.outputPCM {
		t.Fatalf("实际处理链格式=%+v → %+v", s.chain.InputFormat(), s.chain.OutputFormat())
	}
	inputFrames := int64(len(fixture.pcm) / (track.Channels * 4))
	wantFrames := inputFrames * 48000 / int64(sourceRate)
	s.mu.Lock()
	submitted, written := s.inputSubmitted, s.written
	s.mu.Unlock()
	if submitted != inputFrames || written != wantFrames || probe.frames != wantFrames {
		t.Fatalf("来源提交/用户节点/编码输入帧=%d/%d/%d，期望 %d/%d/%d", submitted, probe.frames, written, inputFrames, wantFrames, wantFrames)
	}
	if codec == "aac" {
		// ADTS 的 sampling_frequency_index=3 表示 48 kHz，核实产物而非仅检查计划。
		if len(encoded) < 7 || encoded[0] != 0xff || encoded[1]&0xf6 != 0xf0 {
			t.Fatal("产出的 AAC 缺少合法 ADTS 头")
		}
		if index := encoded[2] >> 2 & 0xf; index != 3 {
			t.Fatalf("AAC 实际采样率索引=%d，期望 3（48 kHz）", index)
		}
	}

	duration := float64(wantFrames) / 48000
	observe(s, duration)
	events := eventsThrough(t, s, "stream_ended", 0)
	signals, times := 0, 0
	for _, event := range events {
		switch event.Kind {
		case "signal":
			signals++
			path, ok := event.Signal.(signalPath)
			if !ok {
				t.Fatalf("信号路径类型=%T", event.Signal)
			}
			var kinds []string
			for _, step := range path.Steps {
				kinds = append(kinds, step.Kind)
				if step.Kind == "sample_rate" {
					want := map[string]any{"from_rate": sourceRate, "to_rate": 48000, "filter": "linear_precise"}
					if !reflect.DeepEqual(step.Params, want) || step.Quality != "high" || !step.Compat || step.Lossy {
						t.Fatalf("兼容性 SRC 信号=%+v", step)
					}
				}
			}
			// 精确顺序同时排除重复 SRC、旧 convert，并确认变率位于用户节点之前。
			if !reflect.DeepEqual(kinds, []string{"source", "sample_rate", "test_user", "encode"}) {
				t.Fatalf("信号路径顺序=%v", kinds)
			}
			if path.Quality != "low" || path.Steps[0].Params["sample_rate"] != sourceRate || path.Steps[3].Params["sample_rate"] != 48000 {
				t.Fatalf("有损路径评级或两端采样率不符：%+v", path)
			}
		case "time":
			times++
			if event.EntryID != "entry-a" || math.Abs(event.Seconds-event.StreamSeconds) > 1e-9 || event.StreamSeconds > duration+1e-9 {
				t.Fatalf("输出域位置事件=%+v，时长=%g", event, duration)
			}
		case "stream_ended":
			if math.Abs(event.StreamSeconds-duration) > 1e-9 {
				t.Fatalf("流尾时长=%g，期望 %g", event.StreamSeconds, duration)
			}
		case "track_failed", "format_boundary", "rebuild_needed":
			t.Fatalf("意外事件：%+v", event)
		}
	}
	if signals == 0 || times == 0 {
		t.Fatalf("缺少信号或位置事件：signal=%d，time=%d", signals, times)
	}
}
