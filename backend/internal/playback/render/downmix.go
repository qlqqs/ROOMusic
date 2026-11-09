package render

import (
	"strconv"

	"github.com/qlqq/roomusic/backend/internal/dsp/chain"
	"github.com/qlqq/roomusic/backend/internal/dsp/downmix"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

// chooseChannels 接续 SRC 的输出格式；矩阵在 Stages，纯声道选择在量化前的 Tail。
func (p *renderPlan) chooseChannels(document zones.SettingsDocument, maximum int) error {
	settings, err := zones.DecodeSettingsDocument[zones.DownmixSettings](document)
	if err != nil {
		return err
	}
	in := p.outputPCM
	if target, ok := downmix.Target(downmix.Mask(in.Layout), maximum, settings.Enabled); ok {
		matrix, err := downmix.Build(downmix.Mask(in.Layout), target)
		if err != nil {
			return err
		}
		out := in
		out.Channels, out.Layout = target.Channels(), uint32(target)
		step := downmixStep(in, out, "matrix")
		p.stages = append(p.stages, chain.Stage{In: in, Out: out, Modifies: true,
			Node: chain.Wrap(downmix.New(matrix), in, out, step, true)})
		p.outputPCM = out
		return nil
	}
	if in.Channels > maximum {
		out := in
		out.Channels, out.Layout = maximum, selectedLayout(in.Layout, maximum)
		p.tail = append(p.tail, chain.Stage{In: in, Out: out,
			Node: &channelSelect{channels: maximum, step: downmixStep(in, out, "select")}})
		p.outputPCM = out
	}
	return nil
}

func downmixStep(in, out chain.Format, mode string) chain.SignalStep {
	layoutName := func(format chain.Format) string {
		if format.Layout == 0 {
			return strconv.Itoa(format.Channels) + " 声道"
		}
		return downmix.Mask(format.Layout).String()
	}
	return chain.SignalStep{Kind: "downmix", Quality: "high", Compat: true, Params: map[string]any{
		"from_channels": in.Channels, "to_channels": out.Channels,
		"from_layout": layoutName(in), "to_layout": layoutName(out), "by": "roomusic", "mode": mode,
	}}
}

// selectedLayout 保留流顺序中的前 N 个扬声器；单声道出口使用标准 mono 标识。
func selectedLayout(layout uint32, channels int) uint32 {
	if channels == 1 {
		return chain.DefaultLayout(1)
	}
	var selected uint32
	for count := 0; count < channels && layout != 0; count++ {
		bit := layout & -layout
		selected |= bit
		layout &^= bit
	}
	return selected
}

// channelSelect 不混合、不改样本值，保留所选声道的 Exact 与来源位深。
type channelSelect struct {
	channels int
	step     chain.SignalStep
	result   chain.Buffer
}

func (n *channelSelect) Process(b *chain.Buffer) *chain.Buffer {
	n.result = *b
	n.result.Samples = b.Samples[:n.channels]
	return &n.result
}

func (n *channelSelect) Reset()                 { n.result = chain.Buffer{} }
func (n *channelSelect) Step() chain.SignalStep { return n.step }
