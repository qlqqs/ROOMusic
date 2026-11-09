package render

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"testing"
	"time"

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

func TestRenderCrossfadeWaitAtCut(t *testing.T) {
	for _, outcome := range []string{"ready", "timeout", "cancel"} {
		t.Run(outcome, func(t *testing.T) {
			// 缩小源帧率以逐样本核对整条流；不经过编码器或格式协商。
			const rate, window, cut, oldEnd = 80, 3 * 80, 7 * 80, 11 * 80
			spec := renderSpec("flac")
			spec.Settings.Crossfade, spec.Settings.CrossfadeSeconds = true, 3
			pcm := chain.Format{SampleRate: rate, Channels: 2}
			effectiveEnd := 10.0
			a := Track{Codec: "flac", SampleRate: rate, Channels: 2, BitDepth: 16,
				ReleaseID: "release-a", DurationSeconds: 11, TrailingStart: &effectiveEnd}
			b := a
			b.ReleaseID, b.DurationSeconds, b.TrailingStart = "release-b", 10, nil
			valuesA, valuesB := [2]float64{0.5, -0.25}, [2]float64{0.25, 0.125}
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			r := newTestRenderer(t, nil, nil)
			type deadline struct {
				duration time.Duration
				ticker   testTicker
			}
			deadlines := make(chan deadline, 8)
			r.options.NewTicker = func(d time.Duration) Ticker {
				ticker := testTicker{make(chan time.Time, 1)}
				deadlines <- deadline{d, ticker}
				return ticker
			}
			s := &session{owner: r, ctx: ctx, spec: spec, sourcePCM: pcm, outputPCM: pcm, chain: chain.New(),
				format:          zones.StreamFormat{Codec: "flac", SampleRate: rate, Channels: 2, BitDepth: 16},
				immediateEvents: make(chan zones.Event, 8)}
			outgoing := crossfadeMemoryDecoder(a, spec.Start, pcm, valuesA)
			entryB, _ := nextB(spec.Start.EntryID)
			incoming := crossfadeMemoryDecoder(b, entryB, pcm, valuesB)
			// 保留合成 PCM，但在切点前不发布任何字节或终态。
			raw := append([]byte(nil), incoming.ring.data...)
			incoming.ring = newByteRing(len(raw))
			incomingClosed := false
			incoming.cancel = func() { incomingClosed = true }
			p := &player{session: s, current: outgoing, next: incoming, last: spec.Start, selected: true}
			s.player = p
			t.Cleanup(p.close)
			p.plan = p.planTransition(b)
			if p.plan.kind != "crossfade" || p.plan.cut != cut || p.plan.frames != window {
				t.Fatalf("淡化计划错误：%+v", p.plan)
			}
			if ready, _ := incoming.ring.readiness(pcm.Channels * 4); ready {
				t.Fatal("下一首不应在切点前就绪")
			}

			transition, transitionFrame := "crossfade", int64(cut)
			if outcome == "timeout" {
				transition, transitionFrame = "gapless", oldEnd
			}
			var total int64
			checkBlock := func(block *chain.Buffer) {
				t.Helper()
				overlaps := outcome == "ready" && total < cut+window && total+int64(block.Frames) > cut
				if block.Exact == overlaps {
					t.Fatalf("块 %d 的 Exact=%v，是否包含淡化=%v", total, block.Exact, overlaps)
				}
				for i := range block.Frames {
					frame := total + int64(i)
					for ch := range pcm.Channels {
						want := valuesA[ch]
						if frame >= transitionFrame {
							want = valuesB[ch]
							if outcome == "ready" && frame < cut+window {
								x := float64(frame-cut) / window
								want = valuesA[ch]*math.Pow(math.Cos(math.Pi*x/2), 4) + valuesB[ch]*math.Sqrt(math.Sin(math.Pi*x/2))
							}
						}
						if got := block.Samples[ch][i]; math.IsNaN(got) || math.Abs(got-want) > 1e-12 {
							t.Fatalf("流帧 %d，声道 %d：样本 %.16g，期望 %.16g", frame, ch, got, want)
						}
					}
				}
				total += int64(block.Frames)
			}
			block, _, err := p.read(cut)
			if err != nil || block == nil || block.Frames != cut {
				t.Fatalf("读取至切点失败：块 %+v，错误 %v", block, err)
			}
			checkBlock(block)
			select {
			case <-deadlines:
				t.Fatal("切点前不应创建等待期限")
			default:
			}

			type readResult struct {
				block *chain.Buffer
				err   error
			}
			result := make(chan readResult, 1)
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				block, _, err := p.read(rate / 5)
				result <- readResult{block, err}
			}()
			t.Cleanup(func() {
				cancel()
				incoming.ring.finish(context.Canceled)
				<-finished
			})
			var waiting deadline
			select {
			case waiting = <-deadlines:
			case got := <-result:
				t.Fatalf("下一首未就绪时跳过了切点等待：%v", got.err)
			case <-time.After(testWait):
				t.Fatal("未进入切点等待")
			}
			if waiting.duration != 5*time.Second {
				t.Fatalf("切点等待期限 = %v，期望五秒", waiting.duration)
			}
			select {
			case got := <-result:
				t.Fatalf("未就绪且未触发期限时读取提前返回：%v", got.err)
			default:
			}
			switch outcome {
			case "ready":
				incoming.ring.put(raw)
				incoming.ring.finish(io.EOF)
			case "timeout":
				waiting.ticker.ticks <- testNow.Add(5 * time.Second)
			case "cancel":
				cancel()
			}
			var got readResult
			select {
			case got = <-result:
			case <-time.After(testWait):
				t.Fatal("就绪、超时或取消后读取没有结束")
			}
			if p.boundary || len(s.immediateEvents) != 0 || incomingClosed {
				t.Fatalf("切点等待不应宣布边界或关闭下一首：boundary=%v，events=%d，closed=%v", p.boundary, len(s.immediateEvents), incomingClosed)
			}
			if outcome == "cancel" {
				if !errors.Is(got.err, context.Canceled) || got.block != nil {
					t.Fatalf("取消没有原样返回：块 %+v，错误 %v", got.block, got.err)
				}
				if p.current != outgoing || p.next != incoming || p.mix != nil || len(s.segments) != 1 || outgoing.position() != cut {
					t.Fatal("取消等待不应推进样本或换轨")
				}
				return
			}
			if got.err != nil || got.block == nil || got.block.Frames != rate/5 {
				t.Fatalf("切点后的量子错误：块 %+v，错误 %v", got.block, got.err)
			}
			checkBlock(got.block)
			if outcome == "timeout" {
				if p.current != outgoing || p.next != incoming || p.mix != nil || p.plan.kind != "gapless" || len(s.segments) != 1 || incoming.consumed != 0 {
					t.Fatal("切点超时应保留旧曲与预取来源，仅降级为普通换轨")
				}
				// 超时后才让 B 就绪；A 必须保留包括有效结尾之后在内的全部真实尾部。
				incoming.ring.put(raw)
				incoming.ring.finish(io.EOF)
			}
			wantFrames := transitionFrame + int64(math.Round(b.DurationSeconds*rate))
			for {
				block, _, err := p.read(rate / 5)
				if err != nil {
					t.Fatalf("继续读取第 %d 帧：%v", total, err)
				}
				if block.Frames == 0 {
					break
				}
				checkBlock(block)
				if total > wantFrames {
					t.Fatalf("流长度超过期望 %d", wantFrames)
				}
			}
			wantOldEnd := int64(cut + window)
			if outcome == "timeout" {
				wantOldEnd = oldEnd
			}
			if total != wantFrames || outgoing.position() != wantOldEnd {
				t.Fatalf("流长度或旧曲消费位置错误：total=%d，want=%d，A=%d，wantA=%d", total, wantFrames, outgoing.position(), wantOldEnd)
			}
			if len(s.segments) != 2 || s.segments[1].frame != transitionFrame || s.segments[1].value.EntryID != entryB.EntryID || s.segments[1].value.TrackStartSeconds != 0 {
				t.Fatalf("换轨分段错误：%+v", s.segments)
			}
			started := 0
			for _, pending := range s.pending {
				if pending.event.Kind == "track_started" {
					started++
					if pending.frame != transitionFrame || pending.event.EntryID != entryB.EntryID || pending.event.Transition != transition {
						t.Fatalf("换轨事件错误：%+v", pending)
					}
				}
			}
			if started != 1 || p.boundary || len(s.immediateEvents) != 0 {
				t.Fatalf("应在同一条流中恰好换轨一次：started=%d，boundary=%v，events=%d", started, p.boundary, len(s.immediateEvents))
			}
		})
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
