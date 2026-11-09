package render

import (
	"slices"

	"github.com/qlqq/roomusic/backend/internal/dsp/chain"
	"github.com/qlqq/roomusic/backend/internal/dsp/peq"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

// buildPEQ 把当前流生效的段按声道展开，全部交给一个 IIR；不改变文档快照。
func buildPEQ(settings zones.PEQSettings, format chain.Format) (chain.Stage, error) {
	var sections []peq.Section
	bands := 0
	for _, band := range settings.Bands {
		if !band.Enabled {
			continue
		}
		var channels []int
		for channel := range format.Channels {
			if band.Channels.All || slices.Contains(band.Channels.Indices, channel) {
				channels = append(channels, channel)
			}
		}
		if len(channels) == 0 {
			continue
		}
		// 系数频率下限为 10 Hz，过低的流采样率无法满足设计器的物理范围。
		if float64(format.SampleRate)*0.49 < 10 {
			return chain.Stage{}, ErrNoStreamFormat
		}
		filter := peq.Band{Type: band.Type, FreqHz: band.FreqHz}
		if band.GainDB != nil {
			filter.GainDB = *band.GainDB
		}
		if band.Q != nil {
			filter.Q = *band.Q
		}
		if band.SlopeDB != nil {
			filter.SlopeDB = *band.SlopeDB
		}
		designed := peq.Design(filter, float64(format.SampleRate))
		for _, channel := range channels {
			for _, section := range designed {
				section.Channel = channel
				sections = append(sections, section)
			}
		}
		bands++
	}
	label := "参数均衡"
	if source := settings.Source; source != nil {
		switch source.Kind {
		case "opra":
			label = source.Product + " · " + source.Author
		case "preset":
			label = source.Name
		}
	}
	step := chain.SignalStep{Kind: "peq", Quality: "enhanced", Params: map[string]any{"label": label, "bands": bands}}
	return chain.Stage{In: format, Out: format, Modifies: true,
		Node: chain.Wrap(peq.NewIIR(format.Channels, sections, settings.PreampDB), format, format, step, true)}, nil
}
