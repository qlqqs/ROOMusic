package render

import (
	"github.com/qlqq/roomusic/backend/internal/dsp/chain"
	"github.com/qlqq/roomusic/backend/internal/dsp/leveling"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

// renderPlan 只保存轻量格式与处理事实，不做资源读取或大核生成。
// 节点按实际前级输出格式构建，归本会话独占。
type renderPlan struct {
	sourcePCM, outputPCM chain.Format
	facts                processingFacts
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
