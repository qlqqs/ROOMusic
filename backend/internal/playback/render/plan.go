package render

import (
	"errors"

	"github.com/qlqq/roomusic/backend/internal/dsp/chain"
	"github.com/qlqq/roomusic/backend/internal/dsp/leveling"
	"github.com/qlqq/roomusic/backend/internal/dsp/src"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

// renderPlan 只保存轻量格式与处理事实，不做资源读取或大核生成。
// 节点按实际前级输出格式构建，归本会话独占。
type renderPlan struct {
	sourcePCM, outputPCM chain.Format
	facts                processingFacts
	sampleRate           src.Plan
	sampleRateFilter     string
	stages, user, tail   []chain.Stage
}

type processingFacts struct {
	nonExact, sourceConversion, leveling, headroom, stages bool
}

func hasProcessing(f processingFacts) bool {
	return f.nonExact || f.sourceConversion || f.leveling || f.headroom || f.stages
}

func trackLayout(t Track) uint32 {
	if t.Layout != 0 {
		return t.Layout
	}
	return chain.DefaultLayout(t.Channels)
}

func sourceProcessing(track Track, entry zones.Entry, settings zones.ZoneSettings, pcm chain.Format) processingFacts {
	gainDB, _, enabled := leveling.Choose(leveling.Mode(settings.Leveling), track.TrackGainDB, track.AlbumGainDB, entry.AlbumContext)
	return processingFacts{
		nonExact:         !integerSource(track),
		sourceConversion: track.SampleRate != pcm.SampleRate || track.Channels != pcm.Channels,
		leveling:         enabled && leveling.Linear(gainDB) != 1,
		headroom:         settings.Headroom.Enabled,
	}
}

// 后继所需精度不能超过已发布的出口；设备只支持 16 位时仍可量化到 16 位。
func (s *session) acceptsSource(track Track, entry zones.Entry) bool {
	if s.format.BitDepth != 16 || s.format.MaxBitDepth > 0 && s.format.MaxBitDepth < 24 {
		return true
	}
	facts := sourceProcessing(track, entry, s.spec.Settings, s.sourcePCM)
	facts.stages = s.chain.ModifiesSamples()
	return !hasProcessing(facts) && track.BitDepth > 0 && track.BitDepth <= 16
}

// buildChain 在调用方的会话启动协程内生成 SRC 核，不占用 zones 控制锁。
func (p renderPlan) buildChain(settings zones.ZoneSettings, bits int) (*chain.Chain, error) {
	stages := p.stages
	if len(p.sampleRate.Stages) > 0 {
		out := p.sourcePCM
		out.SampleRate = p.sampleRate.To
		step := chain.SignalStep{Kind: "sample_rate", Quality: "enhanced", Params: map[string]any{
			"from_rate": p.sampleRate.From, "to_rate": p.sampleRate.To, "filter": p.sampleRateFilter,
		}}
		if p.sampleRate.Compatibility {
			step.Quality, step.Compat = "high", true
		}
		stage := chain.Stage{In: p.sourcePCM, Out: out, Modifies: true,
			Node: chain.Wrap(src.New(p.sampleRate.Stages, p.sourcePCM.Channels), p.sourcePCM, out, step, true)}
		stages = append([]chain.Stage{stage}, stages...)
	}
	c, err := chain.Build(chain.Spec{Format: p.sourcePCM, Stages: stages, User: p.user, Tail: p.tail,
		Headroom: chain.HeadroomSettings{Enabled: settings.Headroom.Enabled, DB: settings.Headroom.DB}, OutputBits: bits, Seed: 1})
	if err != nil {
		return nil, err
	}
	if c.OutputFormat() != p.outputPCM {
		return nil, errors.New("render: 处理链输出与计划不符")
	}
	return c, nil
}
