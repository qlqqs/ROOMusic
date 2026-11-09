package chain

import (
	"bytes"
	"fmt"
	"reflect"
	"testing"
)

// 假处理器按每 ratio 个输入帧保留最后一帧，先加固定延迟，再按 batch 帧吐块。
type delayedProcessor struct {
	delay, ratio, batch, phase, ready int
	queue                             [][]float64
}

func newDelayedProcessor(delay, ratio, batch, channels int) *delayedProcessor {
	p := &delayedProcessor{delay: delay, ratio: ratio, batch: batch, queue: make([][]float64, channels)}
	p.Reset()
	return p
}
func (p *delayedProcessor) Latency() int { return p.delay }
func (p *delayedProcessor) Reset() {
	p.phase, p.ready = 0, 0
	for ch := range p.queue {
		p.queue[ch] = make([]float64, p.delay)
	}
}
func (p *delayedProcessor) Process(in [][]float64) [][]float64 {
	for i := range in[0] {
		p.phase++
		if p.phase%p.ratio != 0 {
			continue
		}
		for ch := range in {
			p.queue[ch] = append(p.queue[ch], in[ch][i])
		}
		p.ready++
	}
	n := p.ready / p.batch * p.batch
	out := make([][]float64, len(in))
	for ch := range out {
		out[ch] = append([]float64(nil), p.queue[ch][:n]...)
		p.queue[ch] = p.queue[ch][n:]
	}
	p.ready -= n
	return out
}

func TestWrapLatencyAndDrain(t *testing.T) {
	for _, tt := range []struct {
		name         string
		ratio, batch int
	}{
		{"固定延迟", 1, 1}, {"攒块", 1, 4}, {"二比一变率并攒块", 2, 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			in := Format{SampleRate: 10, Channels: 2}
			out := in
			out.SampleRate /= tt.ratio
			c := New(Wrap(newDelayedProcessor(3, tt.ratio, tt.batch, 2), in, out, SignalStep{}, tt.ratio != 1))
			samples := [][]float64{{1, 2, 3, 4, 5, 6, 7, 8}, {-1, -2, -3, -4, -5, -6, -7, -8}}
			got := make([][]float64, 2)
			collect := func(b *Buffer) {
				for ch := range got {
					got[ch] = append(got[ch], b.Samples[ch][:b.Frames]...)
				}
			}
			start := 0
			for _, end := range []int{3, 5, 8} {
				collect(c.Process(&Buffer{Samples: [][]float64{samples[0][start:end], samples[1][start:end]}, Frames: end - start, Exact: true, SourceBits: 16}))
				start = end
			}
			for b := c.Drain(); b != nil; b = c.Drain() {
				collect(b)
			}
			for ch := range got {
				if len(got[ch]) != 8/tt.ratio {
					t.Fatalf("输出帧数=%d, want %d", len(got[ch]), 8/tt.ratio)
				}
				for i, value := range got[ch] {
					if want := samples[ch][(i+1)*tt.ratio-1]; value != want {
						t.Fatalf("声道 %d 帧 %d=%g, want %g", ch, i, value, want)
					}
				}
			}
		})
	}
}

func TestWrapRegionsAcrossBlocks(t *testing.T) {
	a := Wrap(newDelayedProcessor(1, 2, 2, 1), Format{SampleRate: 10, Channels: 1}, Format{SampleRate: 5, Channels: 1}, SignalStep{}, true)
	var gains []float64
	collect := func(b *Buffer) {
		start := len(gains)
		for i := 0; i < b.Frames; i++ {
			gains = append(gains, 1)
		}
		for _, r := range b.Regions {
			if r.Offset < 0 || r.Frames <= 0 || r.Offset+r.Frames > b.Frames {
				t.Fatalf("输出区域越界：%+v，帧数 %d", r, b.Frames)
			}
			for i := r.Offset; i < r.Offset+r.Frames; i++ {
				gains[start+i] = r.Gain
			}
		}
	}
	// 输入绝对区间 [1,6) 跨越两块，输出应覆盖 [0,3)，而非分别按块长取整。
	for _, b := range []Buffer{
		{Samples: [][]float64{{1, 2, 3}}, Frames: 3, Regions: []GainRegion{{Offset: 1, Frames: 2, Gain: 0.5}}},
		{Samples: [][]float64{{4, 5, 6}}, Frames: 3, Regions: []GainRegion{{Frames: 3, Gain: 0.5}}},
		{Samples: [][]float64{{7, 8}}, Frames: 2},
	} {
		collect(a.Process(&b))
	}
	for b := a.Drain(); b != nil; b = a.Drain() {
		collect(b)
	}
	if want := []float64{0.5, 0.5, 0.5, 1}; !reflect.DeepEqual(gains, want) {
		t.Fatalf("逐帧增益=%v, want %v", gains, want)
	}
}

func TestWrapExact(t *testing.T) {
	for _, modifies := range []bool{false, true} {
		t.Run(fmt.Sprintf("modifies=%v", modifies), func(t *testing.T) {
			format := Format{SampleRate: 10, Channels: 1}
			a := Wrap(newDelayedProcessor(2, 1, 1, 1), format, format, SignalStep{}, modifies)
			var got []bool
			collect := func(b *Buffer) {
				for i := 0; i < b.Frames; i++ {
					got = append(got, b.Exact)
				}
			}
			for _, exact := range []bool{true, false} {
				collect(a.Process(&Buffer{Samples: [][]float64{{1, 2}}, Frames: 2, Exact: exact, SourceBits: 24}))
			}
			for b := a.Drain(); b != nil; b = a.Drain() {
				collect(b)
			}
			want := []bool{!modifies, !modifies, false, false}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Exact=%v, want %v", got, want)
			}
		})
	}
}

func TestChainRejectsUnappliedRegions(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("链头未拒绝尚未施加的增益区域")
		}
	}()
	New().Process(&Buffer{Samples: [][]float64{{1}}, Frames: 1, Regions: []GainRegion{{Frames: 1, Gain: 1}}})
}

func TestQuantizeExactIntegers(t *testing.T) {
	for _, bits := range []int{16, 24} {
		t.Run(fmt.Sprint(bits), func(t *testing.T) {
			c, err := Build(Spec{Format: Format{SampleRate: 48000, Channels: 2}, OutputBits: bits, Seed: 1})
			if err != nil {
				t.Fatal(err)
			}
			values := []int32{-1 << (bits - 1), -257, -1, 0, 1, 257, 1<<(bits-1) - 1}
			samples := [][]float64{make([]float64, len(values)), make([]float64, len(values))}
			var want []byte
			for i := range values {
				for ch := range samples {
					v := values[i]
					if ch == 1 {
						v = values[len(values)-1-i]
					}
					samples[ch][i] = float64(v<<(32-bits)) / (1 << 31)
					for j := 0; j < bits/8; j++ {
						want = append(want, byte(v>>(8*j)))
					}
				}
			}
			b := c.Process(&Buffer{Samples: samples, Frames: len(values), Exact: true, SourceBits: bits})
			got, clipped := c.Quantize(nil, b)
			if !bytes.Equal(got, want) || clipped != 0 {
				t.Fatalf("量化=%x, 削波=%d; want %x", got, clipped, want)
			}
		})
	}
}
