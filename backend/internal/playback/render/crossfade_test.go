package render

import (
	"context"
	"encoding/binary"
	"io"
	"math"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/dsp/chain"
	"github.com/qlqq/roomusic/backend/internal/dsp/fade"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

func TestRenderCrossfadeConditions(t *testing.T) {
	const rate = 44100
	for _, tc := range []struct {
		name                  string
		sameRelease, disabled bool
		aSeconds, bSeconds    float64
		want                  string
	}{
		{name: "条件满足", aSeconds: 10, bSeconds: 10, want: "crossfade"},
		{name: "同发行不淡化", sameRelease: true, aSeconds: 10, bSeconds: 10, want: "gapless"},
		{name: "A太短_恰好三倍", aSeconds: 9, bSeconds: 10, want: "gapless"},
		{name: "B太短_恰好三倍", aSeconds: 10, bSeconds: 9, want: "gapless"},
		{name: "开关关闭", disabled: true, aSeconds: 10, bSeconds: 10, want: "gapless"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := renderSpec("flac")
			spec.Settings.Crossfade, spec.Settings.CrossfadeSeconds = !tc.disabled, 3
			a := Track{ReleaseID: "release-a", DurationSeconds: tc.aSeconds}
			b := Track{ReleaseID: "release-b", DurationSeconds: tc.bSeconds}
			if tc.sameRelease {
				b.ReleaseID = a.ReleaseID
			}
			pcm := chain.Format{SampleRate: rate, Channels: 2}
			p := &player{session: &session{spec: spec, sourcePCM: pcm, outputPCM: pcm}, current: &decoder{track: a, format: pcm}}
			if got := p.planTransition(b); got.kind != tc.want {
				t.Fatalf("换轨方式 = %q，期望 %q；计划 %+v", got.kind, tc.want, got)
			}
		})
	}
}

func TestRenderCrossfadePCMAndTimeline(t *testing.T) {
	const rate, window = 44100, 3 * 44100
	const cut = 308704 // 有效结尾为 441007 帧，减去窗口后由 308707 向下对齐。
	spec := renderSpec("flac")
	spec.Settings.Crossfade, spec.Settings.CrossfadeSeconds = true, 3
	pcm := chain.Format{SampleRate: rate, Channels: 2}
	end := float64(441007) / rate
	a := Track{Codec: "flac", SampleRate: rate, Channels: 2, BitDepth: 16, ReleaseID: "release-a", DurationSeconds: 11, TrailingStart: &end}
	b := a
	b.ReleaseID, b.DurationSeconds, b.TrailingStart = "release-b", 10.5, nil
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s := &session{owner: newTestRenderer(t, nil, nil), ctx: ctx, spec: spec, sourcePCM: pcm, outputPCM: pcm, chain: chain.New(),
		format: zones.StreamFormat{Codec: "flac", SampleRate: rate, Channels: 2, BitDepth: 16}}
	// 两声道使用不同符号和幅度，既能检查曲线，也能检查声道未被串换。
	valuesA, valuesB := [2]float64{0.5, -0.25}, [2]float64{0.25, 0.125}
	outgoing := crossfadeMemoryDecoder(a, spec.Start, pcm, valuesA)
	p := &player{session: s, current: outgoing, last: spec.Start, selected: true}
	s.player = p
	t.Cleanup(p.close)
	p.plan = p.planTransition(b)
	if p.plan.kind != "crossfade" || p.plan.cut != cut || p.plan.frames != window || p.plan.startSeconds != 0 {
		t.Fatalf("淡化计划不符合素材：%+v", p.plan)
	}
	if p.plan.cut%8 != 0 || p.plan.cut+int64(p.plan.frames) > int64(math.Round(end*rate)) {
		t.Fatalf("截断点未按八帧对齐或窗口越过有效结尾：%+v", p.plan)
	}
	entryB, _ := nextB(spec.Start.EntryID)
	p.next = crossfadeMemoryDecoder(b, entryB, pcm, valuesB)
	if ready, _ := p.next.ring.readiness(pcm.Channels * 4); !ready {
		t.Fatal("同步素材未准备就绪")
	}
	wantFrames := cut + int64(math.Round(b.DurationSeconds*rate))
	// 抽查窗口首尾、四分点及实际量子拼接两侧，验证位置不会在下一块重置。
	points := map[int64]bool{}
	for _, k := range []int64{0, 1, window / 4, window / 2, window * 3 / 4, window - 1} {
		points[k] = false
	}
	firstSplit := int64(rate/5) - cut%int64(rate/5)
	points[firstSplit-1], points[firstSplit] = false, false
	var total int64
	for {
		block, _, err := p.read(rate / 5)
		if err != nil {
			t.Fatalf("读取第 %d 帧：%v", total, err)
		}
		if block.Frames == 0 {
			break
		}
		if total+int64(block.Frames) > wantFrames {
			t.Fatalf("输出超过期望帧数 %d，可能没有丢弃A尾部", wantFrames)
		}
		if block.Frames != rate/5 && total+int64(block.Frames) != wantFrames {
			t.Fatalf("流尾之前出现短块：起点 %d，帧数 %d", total, block.Frames)
		}
		overlaps := total < cut+window && total+int64(block.Frames) > cut
		if block.Exact == overlaps {
			t.Fatalf("块 %d 的 Exact=%v，是否包含淡化=%v", total, block.Exact, overlaps)
		}
		for i := range block.Frames {
			frame := total + int64(i)
			k := frame - cut
			_, sampleWindow := points[k]
			if k >= 0 && k < window && !sampleWindow {
				continue
			}
			for ch := range pcm.Channels {
				want := valuesA[ch]
				if k >= window {
					want = valuesB[ch]
				} else if k >= 0 {
					x := float64(k) / window
					want = valuesA[ch]*math.Pow(math.Cos(math.Pi*x/2), 4) + valuesB[ch]*math.Sqrt(math.Sin(math.Pi*x/2))
					fromFade := valuesA[ch]*fade.FadeOut(x) + valuesB[ch]*fade.FadeIn(x)
					if math.Abs(want-fromFade) > 1e-12 {
						t.Fatalf("dsp/fade 与公式不符：k=%d，声道=%d", k, ch)
					}
					points[k] = true
				}
				if got := block.Samples[ch][i]; math.IsNaN(got) || math.Abs(got-want) > 1e-12 {
					t.Fatalf("流帧 %d，窗口帧 %d，声道 %d：样本 %.16g，期望 %.16g", frame, k, ch, got, want)
				}
			}
		}
		total += int64(block.Frames)
	}
	if total != wantFrames {
		t.Fatalf("总帧数 = %d，期望 %d", total, wantFrames)
	}
	for point, checked := range points {
		if !checked {
			t.Errorf("窗口帧 %d 未被检查", point)
		}
	}
	if got := outgoing.position(); got != cut+window {
		t.Errorf("A消费到 %d，期望窗口尾 %d，之后的尾部应丢弃", got, cut+window)
	}
	if len(s.segments) != 2 || s.segments[1].frame != cut {
		t.Fatalf("分段没有从截断点开始：%+v", s.segments)
	}
	segmentB := s.segments[1].value
	if segmentB.EntryID != entryB.EntryID || segmentB.TrackID != entryB.TrackID || segmentB.StreamStartSeconds != float64(cut)/rate || segmentB.TrackStartSeconds != 0 {
		t.Fatalf("B分段错误：%+v", segmentB)
	}
	// 同步驱动真实派发逻辑，不启动渲染协程，也不依赖墙钟或子进程调度。
	s.inputSubmitted, s.written, s.renderComplete = total, total, true
	observe(s, float64(cut-1)/rate)
	for event, ok := s.dueEvent(testNow); ok; event, ok = s.dueEvent(testNow) {
		if event.Kind == "track_started" {
			t.Fatalf("截断点之前提前换轨：%+v", event)
		}
	}
	observe(s, float64(cut)/rate)
	started := 0
	for event, ok := s.dueEvent(testNow); ok; event, ok = s.dueEvent(testNow) {
		if event.Kind != "track_started" {
			continue
		}
		started++
		if event.EntryID != entryB.EntryID || event.Transition != "crossfade" || event.StreamSeconds != float64(cut)/rate {
			t.Fatalf("淡化换轨事件错误：%+v", event)
		}
		if len(event.Segments) != 1 || event.Segments[0] != segmentB {
			t.Fatalf("换轨事件没有携带B的新分段：%+v", event.Segments)
		}
	}
	if started != 1 {
		t.Fatalf("截断点收到 %d 次换轨，期望恰好一次", started)
	}
}

// 同步填充解码器现有的字节环并发布 EOF，避免真实预取竞态让淡化随机降级。
// 仍由生产 decoder.read 执行拆帧、声道转换，再由 player.read 完成实际混音。
func crossfadeMemoryDecoder(track Track, entry zones.Entry, format chain.Format, values [2]float64) *decoder {
	frames := int(math.Round(track.DurationSeconds * float64(format.SampleRate)))
	raw := make([]byte, frames*format.Channels*4)
	for frame := range frames {
		for ch := range format.Channels {
			bits := uint32(int32(values[ch] * 2147483648))
			binary.LittleEndian.PutUint32(raw[(frame*format.Channels+ch)*4:], bits)
		}
	}
	ring := newByteRing(len(raw))
	ring.put(raw)
	ring.finish(io.EOF)
	done := make(chan struct{})
	close(done)
	return &decoder{track: track, entry: entry, format: format,
		ring: ring, integer: true, exact: true, sourceBits: track.BitDepth, gain: 1,
		cancel: func() {}, done: done}
}
