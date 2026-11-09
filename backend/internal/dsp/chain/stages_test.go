package chain

import "testing"

// 只模拟时钟转换与固有延迟，不验证任何重采样算法。
type stageClockProcessor struct {
	inRate, outRate, channels, delay int
	input, output                    int64
}

func (p *stageClockProcessor) Latency() int { return p.delay }
func (p *stageClockProcessor) Reset()       { p.input, p.output = 0, 0 }
func (p *stageClockProcessor) Process(in [][]float64) [][]float64 {
	p.input += int64(len(in[0]))
	end := p.input * int64(p.outRate) / int64(p.inRate)
	out := make([][]float64, p.channels)
	for ch := range out {
		out[ch] = make([]float64, end-p.output)
	}
	p.output = end
	return out
}

func TestStagesFormatFramesAndDrain(t *testing.T) {
	in := Format{SampleRate: 44100, Channels: 2, Layout: 3}
	mid := Format{SampleRate: 48000, Channels: 2, Layout: 3}
	out := Format{SampleRate: 48000, Channels: 1, Layout: 4}
	for _, chunks := range [][]int{{1, 146, 295, 17}, {459}} {
		c, err := Build(Spec{Format: in, Stages: []Stage{
			{Node: Wrap(&stageClockProcessor{44100, 48000, 2, 3, 0, 0}, in, mid, SignalStep{}, true), In: in, Out: mid, Modifies: true},
			{Node: Wrap(&stageClockProcessor{48000, 48000, 1, 7, 0, 0}, mid, out, SignalStep{}, true), In: mid, Out: out, Modifies: true},
		}})
		if err != nil {
			t.Fatal(err)
		}
		if c.InputFormat() != in || c.OutputFormat() != out || c.Latency() != 10 || !c.ModifiesSamples() {
			t.Fatalf("链格式或延迟不符：%+v → %+v，延迟 %d", c.InputFormat(), c.OutputFormat(), c.Latency())
		}
		for _, frame := range []int64{0, 1, 146, 147, 442, 459} {
			if got, want := c.OutputFrame(frame), frame*160/147; got != want {
				t.Fatalf("OutputFrame(%d)=%d，期望 %d", frame, got, want)
			}
		}
		total, zero := 0, false
		collect := func(b *Buffer) {
			if len(b.Samples) != 1 || len(b.Samples[0]) != b.Frames {
				t.Fatal("输出声道或长度不符")
			}
			total += b.Frames
			zero = zero || b.Frames == 0
		}
		for _, n := range chunks {
			collect(c.Process(&Buffer{Samples: [][]float64{make([]float64, n), make([]float64, n)}, Frames: n, Exact: true, SourceBits: 16}))
		}
		before := total
		for b := c.Drain(); b != nil; b = c.Drain() {
			collect(b)
		}
		if total != 499 || before >= total || len(chunks) > 1 && !zero {
			t.Fatalf("总帧=%d，冲刷前=%d，零输出=%v", total, before, zero)
		}
	}
}

func TestDefaultLayout(t *testing.T) {
	for _, tc := range []struct {
		channels int
		want     uint32
	}{{4, 0x107}, {7, 0x70f}, {9, 0}, {16, 0}, {32, 0}} {
		if got := DefaultLayout(tc.channels); got != tc.want {
			t.Errorf("DefaultLayout(%d)=%#x，期望 %#x", tc.channels, got, tc.want)
		}
	}
}
