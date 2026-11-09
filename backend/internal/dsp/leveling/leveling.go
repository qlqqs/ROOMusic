// Package leveling 选择分析阶段已算好的增益，并在混音前施加增益区域。
package leveling

import "math"

type Mode string

const (
	Off   Mode = "off"
	Auto  Mode = "auto"
	Track Mode = "track"
	Album Mode = "album"
)

type Basis string

const (
	BasisTrack    Basis = "track"
	BasisAlbum    Basis = "album"
	BasisUnknown  Basis = "unknown"
	UnknownGainDB       = -5.0
)

// Choose 原样选择增益，不重新计算响度或真峰值限制。
// 未完成、失败或不可用的分析结果由调用方传 nil；模式必须已在设置边界校验。
func Choose(mode Mode, trackGainDB, albumGainDB *float64, albumContext bool) (gainDB float64, basis Basis, ok bool) {
	switch mode {
	case Off:
		return 0, "", false
	case Auto, Track, Album:
		useAlbum := mode == Album || mode == Auto && albumContext
		if useAlbum && albumGainDB != nil {
			return *albumGainDB, BasisAlbum, true
		}
		if trackGainDB != nil {
			return *trackGainDB, BasisTrack, true
		}
		return UnknownGainDB, BasisUnknown, true
	default:
		panic("leveling: 未知模式")
	}
}

func Linear(db float64) float64 { return math.Pow(10, db/20) }

// GainRegion 按 Offset 升序、互不重叠，且位于有效样本范围内。
// 未覆盖的帧增益为 1，Gain 为线性值。
type GainRegion struct {
	Offset, Frames int
	Gain           float64
}

// Apply 就地施加区域后清空区域切片，保留容量供下一块复用。
// 使用窄参数而非 chain.Buffer，避免叶子包反向依赖处理链或接口装箱。
func Apply(samples [][]float64, regions *[]GainRegion, exact *bool) {
	for _, region := range *regions {
		if region.Gain == 1 {
			continue
		}
		for _, channel := range samples {
			for i := region.Offset; i < region.Offset+region.Frames; i++ {
				channel[i] *= region.Gain
			}
		}
		*exact = false
	}
	*regions = (*regions)[:0]
}
