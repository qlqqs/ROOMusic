package render

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/dsp/chain"
	"github.com/qlqq/roomusic/backend/internal/playback/render/ffmpeg"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

// 合成节点只生成对应帧数的静音；延迟由 Wrap 丢头并在 Drain 补齐。
type skeletonClock struct {
	inRate, outRate, channels, delay int
	input, output                    int64
}

func (p *skeletonClock) Latency() int { return p.delay }
func (p *skeletonClock) Reset()       { p.input, p.output = 0, 0 }
func (p *skeletonClock) Process(in [][]float64) [][]float64 {
	p.input += int64(len(in[0]))
	end := p.input * int64(p.outRate) / int64(p.inRate)
	out := make([][]float64, p.channels)
	for ch := range out {
		out[ch] = make([]float64, end-p.output)
	}
	p.output = end
	return out
}
func skeletonStage(in, out chain.Format, delay int) chain.Stage {
	return chain.Stage{In: in, Out: out, Modifies: true, Node: chain.Wrap(&skeletonClock{inRate: in.SampleRate, outRate: out.SampleRate, channels: out.Channels, delay: delay}, in, out, chain.SignalStep{}, true)}
}

// 启动前在包内装配计划；不会修改运行中会话，也不新增产品测试开关。
func skeletonSession(t *testing.T, r *Renderer, spec zones.RenderSpec, configure func(*zones.StreamFormat, *renderPlan), start bool) *session {
	t.Helper()
	track, err := r.options.Sources.Prepare(context.Background(), spec.UserID, spec.Start.TrackID)
	if err != nil {
		t.Fatal(err)
	}
	format, plan, err := concreteFormat(spec, track)
	if err != nil {
		track.File.Close()
		t.Fatal(err)
	}
	configure(&format, &plan)
	s, err := newSession(context.Background(), r, spec, format, plan, track, nil)
	if err != nil {
		track.File.Close()
		t.Fatal(err)
	}
	if start {
		s.start()
		t.Cleanup(func() { s.Close(); <-s.done })
	} else {
		if err := s.prepare(); err != nil {
			s.Close()
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close(); s.player.close(); s.encoder.close() })
	}
	return s
}

func TestRenderStagesFormatEventsAndDuration(t *testing.T) {
	ff := pinnedFFmpeg(t)
	tool := openTestTool(t, ff, ffmpeg.Options{})
	a := makeFLAC(t, ff, 16, 44100, 997, 0.303)
	b := makeFLAC(t, ff, 16, 44100, 733, 0.217)
	r := newTestRenderer(t, tool, fixtureSource(map[string]audioFixture{"a": a, "b": b}))
	spec := renderSpec("wav")
	spec.Cursor = testCursor(nextB)
	s := skeletonSession(t, r, spec, func(f *zones.StreamFormat, p *renderPlan) {
		mid := chain.Format{SampleRate: 44100, Channels: 1, Layout: 4}
		out := chain.Format{SampleRate: 48000, Channels: 1, Layout: 4}
		p.stages = []chain.Stage{skeletonStage(p.sourcePCM, mid, 9000), skeletonStage(mid, out, 7)}
		p.outputPCM = out
		p.facts.stages = true
		f.SampleRate, f.Channels, f.Layout, f.BitDepth = 48000, 1, 4, 24
	}, true)
	data := readBody(t, s)
	waitRenderComplete(t, s)
	inFrames := int64((len(a.pcm) + len(b.pcm)) / 8)
	wantFrames := inFrames * 160 / 147
	if s.chain.OutputFormat() != s.outputPCM || s.chain.Latency() != 9803 {
		t.Fatalf("输出格式/延迟=%+v/%d", s.chain.OutputFormat(), s.chain.Latency())
	}
	if s.Format().SampleRate != 48000 || s.Format().Channels != 1 || s.Format().BitDepth != 24 || binary.LittleEndian.Uint32(data[24:28]) != 48000 || binary.LittleEndian.Uint16(data[22:24]) != 1 {
		t.Fatalf("流格式=%+v", s.Format())
	}
	if got := int64((len(data) - 44) / 3); got != wantFrames {
		t.Fatalf("流帧=%d，期望 %d", got, wantFrames)
	}
	s.mu.Lock()
	submitted, written := s.inputSubmitted, s.written
	s.mu.Unlock()
	if submitted != inFrames || written != wantFrames {
		t.Fatalf("输入/输出=%d/%d，期望 %d/%d", submitted, written, inFrames, wantFrames)
	}
	duration := float64(wantFrames) / 48000
	observe(s, duration)
	events := eventsThrough(t, s, "stream_ended", 0)
	cut := int64(len(a.pcm)/8) * 160 / 147
	started, times := 0, 0
	for _, e := range events {
		switch e.Kind {
		case "track_started":
			started++
			if e.EntryID != "entry-b" || math.Abs(e.StreamSeconds*48000-float64(cut)) > 1e-7 {
				t.Fatalf("换轨输出帧不符：%+v", e)
			}
		case "time":
			times++
			want := e.StreamSeconds
			if e.EntryID == "entry-b" {
				want -= float64(cut) / 48000
			}
			if math.Abs(e.Seconds-want) > 1e-9 {
				t.Fatalf("曲内秒数不符：%+v", e)
			}
		case "stream_ended":
			if e.StreamSeconds != duration {
				t.Fatalf("流时长=%g，期望 %g", e.StreamSeconds, duration)
			}
		case "track_failed", "format_boundary":
			t.Fatalf("意外事件：%+v", e)
		}
	}
	if started != 1 || times < 3 {
		t.Fatalf("换轨/位置事件=%d/%d", started, times)
	}
}

func TestDelayedNextRemoval(t *testing.T) {
	ff := pinnedFFmpeg(t)
	tool := openTestTool(t, ff, ffmpeg.Options{})
	fixture := makeFLAC(t, ff, 16, 44100, 997, 0.3)
	for _, tc := range []struct {
		name                    string
		submitted, beforeCommit bool
	}{{"仅预缓冲重选", false, false}, {"已提交零输出重建", true, false}, {"读取与提交之间通知", true, true}} {
		t.Run(tc.name, func(t *testing.T) {
			removed := false
			r := newTestRenderer(t, tool, fixtureSource(map[string]audioFixture{"a": fixture, "b": fixture, "c": fixture}))
			spec := renderSpec("wav")
			spec.Format.MaxBitDepth = 16
			spec.Cursor = testCursor(func(after string) (zones.Entry, bool) {
				id := "b"
				if removed {
					id = "c"
				}
				return zones.Entry{EntryID: "entry-" + id, TrackID: id}, after == "entry-a"
			})
			s := skeletonSession(t, r, spec, func(_ *zones.StreamFormat, p *renderPlan) {
				p.stages = []chain.Stage{skeletonStage(p.sourcePCM, p.sourcePCM, 44100)}
				p.facts.stages = true
			}, false)
			submit := func(b *chain.Buffer) {
				s.mu.Lock()
				s.inputSubmitted += int64(b.Frames)
				s.publishChangeStateLocked()
				s.mu.Unlock()
				if got := s.chain.Process(b); got.Frames != 0 {
					t.Fatalf("应尚无输出，实际 %d", got.Frames)
				}
			}
			read := func() *chain.Buffer {
				b, _, err := s.player.read(8820)
				if err != nil {
					t.Fatal(err)
				}
				return b
			}
			submit(read())
			if s.player.next == nil || s.player.next.entry.EntryID != "entry-b" {
				t.Fatal("未预取 B")
			}
			if tc.submitted {
				b := read()
				if s.player.last.EntryID != "entry-b" {
					t.Fatal("量子未跨入 B")
				}
				if tc.beforeCommit {
					removed = true
					s.NextChanged()
				}
				submit(b)
			}
			if !tc.beforeCommit {
				removed = true
				s.NextChanged()
			}
			changed, transition := s.takeNextChange()
			if !changed || transition != tc.submitted {
				t.Fatalf("通知/污染锁存=%v/%v", changed, transition)
			}
			if err := s.player.nextChanged(false, transition); err != nil {
				t.Fatal(err)
			}
			if tc.submitted {
				select {
				case e := <-s.immediateEvents:
					if e.Kind != "rebuild_needed" {
						t.Fatalf("事件=%+v", e)
					}
				default:
					t.Fatal("缺少 rebuild_needed")
				}
			} else {
				if s.player.next == nil || s.player.next.entry.EntryID != "entry-c" {
					t.Fatal("未重选 C")
				}
				select {
				case e := <-s.immediateEvents:
					t.Fatalf("只预缓冲不应重建：%+v", e)
				default:
				}
			}
		})
	}
}

func TestHLSDenseShortTracks(t *testing.T) {
	ff := pinnedFFmpeg(t)
	tool := openTestTool(t, ff, ffmpeg.Options{})
	fixture := makeFLAC(t, ff, 16, 44100, 997, 0.04)
	fixtures := make(map[string]audioFixture)
	for i := range 30 {
		fixtures[fmt.Sprint(i)] = fixture
	}
	r := newTestRenderer(t, tool, fixtureSource(fixtures))
	spec := renderSpec("alac")
	spec.Start = zones.Entry{EntryID: "0", TrackID: "0"}
	spec.Cursor = testCursor(func(after string) (zones.Entry, bool) {
		var i int
		fmt.Sscan(after, &i)
		i++
		id := fmt.Sprint(i)
		return zones.Entry{EntryID: id, TrackID: id}, i < 30
	})
	s := openTestSession(t, r, spec)
	ctx, cancel := context.WithTimeout(context.Background(), testWait)
	defer cancel()
	playlist, err := s.HLS().Playlist(ctx)
	if err != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		t.Fatalf("30 首 × 40ms，HLS 首批等待未解除：%v；已提交=%d，已输出=%d，待派发=%d", err, s.inputSubmitted, s.written, len(s.pending))
	}
	if !strings.Contains(string(playlist), "#EXT-X-ENDLIST") {
		t.Fatalf("短流未自然结束：%s", playlist)
	}
	waitRenderComplete(t, s)
	observe(s, 1.2)
	events := eventsThrough(t, s, "stream_ended", 0)
	started := 0
	for _, e := range events {
		if e.Kind == "track_started" {
			started++
		}
		if e.Kind == "track_failed" || e.Kind == "format_boundary" {
			t.Fatalf("意外事件：%+v", e)
		}
	}
	if started != 29 {
		t.Fatalf("换轨数=%d，期望 29", started)
	}
}

func TestProcessingDepthAndDither(t *testing.T) {
	for _, tc := range []struct {
		name, leveling  string
		gain            float64
		headroom, stage bool
		cap, bits       int
		processing      bool
	}{
		{name: "关闭处理", leveling: "off", bits: 16},
		{name: "0dB电平化", leveling: "track", bits: 16},
		{name: "非0dB电平化", leveling: "track", gain: -3, bits: 24, processing: true},
		{name: "启用0dB恒等Headroom", leveling: "off", headroom: true, bits: 24, processing: true},
		{name: "仅启用削波指示", leveling: "off", bits: 16},
		{name: "处理但能力限16位", leveling: "track", gain: -3, cap: 16, bits: 16, processing: true},
		{name: "启用恒等Stage", leveling: "off", stage: true, bits: 24, processing: true},
		{name: "恒等Stage能力限16位", leveling: "off", stage: true, cap: 16, bits: 16, processing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := renderSpec("wav")
			spec.Settings.Leveling = tc.leveling
			spec.Settings.Headroom = zones.Headroom{Enabled: tc.headroom, DB: 0, ClipIndicator: true}
			spec.Format.MaxBitDepth = tc.cap
			track := Track{Codec: "flac", SampleRate: 44100, Channels: 2, BitDepth: 16, TrackGainDB: &tc.gain}
			format, plan, err := concreteFormat(spec, track)
			if err != nil {
				t.Fatal(err)
			}
			var stages []chain.Stage
			if tc.stage {
				// 骨架尚无用户 Stage 设置入口；用真实恒等节点检查共享判定及后继位深准入。
				node := chain.Wrap(&skeletonIdentity{}, plan.sourcePCM, plan.sourcePCM, chain.SignalStep{}, true)
				stages = []chain.Stage{{Node: node, In: plan.sourcePCM, Out: plan.sourcePCM, Modifies: true}}
			} else if format.BitDepth != tc.bits {
				t.Fatalf("协商位深=%d，期望 %d", format.BitDepth, tc.bits)
			}
			c, err := chain.Build(chain.Spec{Format: plan.sourcePCM, Stages: stages, Headroom: chain.HeadroomSettings{Enabled: tc.headroom, DB: 0}, OutputBits: format.BitDepth})
			if err != nil {
				t.Fatal(err)
			}
			s := &session{spec: spec, format: format, sourcePCM: plan.sourcePCM, outputPCM: plan.outputPCM, chain: c}
			facts := plan.facts
			facts.stages = c.ModifiesSamples()
			if hasProcessing(facts) != tc.processing {
				t.Fatalf("处理判定=%+v", facts)
			}
			if tc.stage {
				// 未加能力上限的 16 位出口必须拒绝启用节点的后继；24 位出口可接受。
				if got := s.acceptsSource(track, spec.Start); got != (tc.cap == 16) {
					t.Fatalf("16 位出口后继准入=%v", got)
				}
				if tc.cap == 0 {
					s.format.BitDepth = 24
					if !s.acceptsSource(track, spec.Start) {
						t.Fatal("24 位出口拒绝后继")
					}
				}
			}
			step, ok := s.ditherStep(&decoder{track: track, entry: spec.Start, integer: true, sourceBits: 16})
			if ok != tc.processing || ok && (step.Kind != "dither" || step.Params["bits"] != tc.bits) {
				t.Fatalf("抖动=%+v/%v，出口=%d", step, ok, s.format.BitDepth)
			}
		})
	}
}

type skeletonIdentity struct{}

func (*skeletonIdentity) Latency() int                       { return 0 }
func (*skeletonIdentity) Reset()                             {}
func (*skeletonIdentity) Process(in [][]float64) [][]float64 { return in }

func TestRenderExplicitLayout(t *testing.T) {
	// 0x33 是四声道 quad，刻意不同于默认四声道 4.0 的 0x107。
	track := Track{Codec: "wav_extensible", SampleRate: 44100, Channels: 4, BitDepth: 16, Layout: 0x33}
	spec := renderSpec("wav")
	spec.Format.MaxChannels = 4
	f, p, err := concreteFormat(spec, track)
	if err != nil {
		t.Fatal(err)
	}
	if track.Layout != 0x33 || p.sourcePCM.Layout != 0x33 || p.outputPCM.Layout != 0x33 || f.Layout != 0x33 {
		t.Fatalf("显式布局未贯通：%+v / %+v", f, p)
	}
}

// 在启动边界截获解码参数，不创建子进程或音频夹具。
type downmixDecoderStarter struct{ args []string }

func (s *downmixDecoderStarter) Start(_ context.Context, options ffmpeg.StartOptions) (*ffmpeg.Process, error) {
	s.args = options.Args
	return nil, context.Canceled
}

func TestRenderDownmixExplicitLayout(t *testing.T) {
	// 5.1 后环绕掩码不同于默认六声道的侧环绕布局，确保使用显式 WAV 掩码。
	track := Track{Codec: "wav_extensible", SampleRate: 44100, Channels: 6, BitDepth: 16, Layout: 0x3f}
	for _, tc := range []struct {
		name    string
		enabled bool
		mode    string
		bits    int
	}{
		{name: "开启矩阵降混", enabled: true, mode: "matrix", bits: 24},
		{name: "关闭时仅选择前两声道", mode: "select", bits: 16},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := renderSpec("wav")
			var err error
			spec.Settings.Downmix, err = zones.NewDownmixDocument(zones.DownmixSettings{Enabled: tc.enabled})
			if err != nil {
				t.Fatal(err)
			}
			format, plan, err := concreteFormat(spec, track)
			if err != nil {
				t.Fatal(err)
			}
			if plan.sourcePCM.Channels != 6 || plan.sourcePCM.Layout != 0x3f {
				t.Fatalf("来源声道/显式布局未保留：%+v", plan.sourcePCM)
			}
			if format.Channels != 2 || format.Layout != 0x3 || format.BitDepth != tc.bits {
				t.Fatalf("交付格式=%+v，期望双声道 0x3、%d 位", format, tc.bits)
			}
			if tc.enabled {
				if len(plan.stages) != 1 || len(plan.tail) != 0 {
					t.Fatalf("矩阵应在 Stages：%+v", plan)
				}
			} else if len(plan.stages) != 0 || len(plan.tail) != 1 || plan.tail[0].Modifies {
				t.Fatalf("纯选择应在 Tail 且不算处理：%+v", plan)
			}
			c, err := plan.buildChain(spec.Settings, format.BitDepth)
			if err != nil {
				t.Fatal(err)
			}
			if c.OutputFormat().Channels != 2 || c.OutputFormat().Layout != 0x3 {
				t.Fatalf("实际链输出=%+v", c.OutputFormat())
			}
			steps := c.Steps()
			if len(steps) != 1 || steps[0].Kind != "downmix" || steps[0].Params["mode"] != tc.mode || steps[0].Params["from_layout"] != "5.1" || steps[0].Params["to_layout"] != "2.0" {
				t.Fatalf("降混信号步骤=%+v", steps)
			}
			if hasProcessing(plan.facts) != tc.enabled || c.ModifiesSamples() != tc.enabled {
				t.Fatalf("处理判定=%+v，链修改样本=%v", plan.facts, c.ModifiesSamples())
			}
			out := c.Process(&chain.Buffer{Samples: [][]float64{{0.25}, {0.5}, {0.125}, {0}, {-0.25}, {-0.5}}, Frames: 1, Exact: true, SourceBits: 16})
			if len(out.Samples) != 2 || out.Exact != !tc.enabled {
				t.Fatalf("输出声道/Exact=%d/%v", len(out.Samples), out.Exact)
			}
			if !tc.enabled && (out.SourceBits != 16 || out.Samples[0][0] != 0.25 || out.Samples[1][0] != 0.5) {
				t.Fatalf("Tail 未保持前两声道及无损位深：%+v", out)
			}
			starter := &downmixDecoderStarter{}
			_, err = openDecoder(context.Background(), starter, nil, track, spec.Start, plan.sourcePCM, 0, spec.Settings.Leveling)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("未到达解码启动边界：%v", err)
			}
			if slices.Contains(starter.args, "-ac") {
				t.Fatalf("解码不应通过 ffmpeg 降混：%v", starter.args)
			}
		})
	}
}
