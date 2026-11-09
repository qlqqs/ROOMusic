package src

import "errors"

// Filter 是播放区可选择的四种滤波器；空值等同 LinearPrecise。
type Filter string

const (
	LinearPrecise  Filter = "linear_precise"
	MinimumPrecise Filter = "minimum_precise"
	LinearSmooth   Filter = "linear_smooth"
	MinimumSmooth  Filter = "minimum_smooth"
)

var (
	ErrStrategyUnavailable = errors.New("src: strategy unavailable")
	ErrInvalidRequest      = errors.New("src: invalid request")
	ErrNoSupportedRate     = errors.New("src: no supported sample rate")
	ErrUnsupportedRatio    = errors.New("src: ratio cannot be composed from the kernel catalog")
)

// Request 的 Supported 是输出端/编码器已经协商好的能力判定；不得为 nil。
// MaxRate 为零表示采用 768 kHz 上限。源采样率可高于输出上限。
type Request struct {
	SourceRate int
	Strategy   string
	CustomRate int
	Filter     Filter
	Supported  func(rate int) bool
	MaxRate    int
}

// Plan 不持有处理状态，也不生成核；From==To 时 Stages 为空。
type Plan struct {
	From, To      int
	Stages        []Stage
	Compatibility bool
}

var standardRates = [...]int{44100, 48000, 88200, 96000, 176400, 192000, 352800, 384000, 705600, 768000}

// Choose 选择目标率并按目录分级。DSD 输出策略尚不可用，返回 ErrStrategyUnavailable，
// 不在纯包内偷偷改成其他策略；装配方可明确按 none 重新请求。
func Choose(req Request) (Plan, error) {
	if req.SourceRate <= 0 || req.MaxRate < 0 || req.Supported == nil {
		return Plan{}, ErrInvalidRequest
	}
	phase, quality, ok := filterSpec(req.Filter)
	if !ok {
		return Plan{}, ErrInvalidRequest
	}
	limit := req.MaxRate
	if limit == 0 || limit > 768000 {
		limit = 768000
	}
	supported := func(rate int) bool { return rate > 0 && rate <= limit && req.Supported(rate) }
	target := req.SourceRate
	switch req.Strategy {
	case "", "none":
	case "dsd":
		return Plan{}, ErrStrategyUnavailable
	case "custom":
		if req.CustomRate != 0 {
			if !standardRate(req.CustomRate) {
				return Plan{}, ErrInvalidRequest
			}
			target = req.CustomRate
		}
	case "max_sample_rate":
		target = 0
		for _, rate := range standardRates {
			if supported(rate) {
				target = rate
			}
		}
		if target == 0 {
			return Plan{}, ErrNoSupportedRate
		}
	case "power_of_2":
		for rate := req.SourceRate; rate <= limit; {
			if standardRate(rate) && supported(rate) {
				target = rate
			}
			if rate > limit/2 {
				break
			}
			rate *= 2
		}
	default:
		return Plan{}, ErrInvalidRequest
	}
	chosen, compatibility := compatibleRate(target, limit, supported)
	if chosen == 0 {
		return Plan{}, ErrNoSupportedRate
	}
	stages, err := conversionStages(req.SourceRate, chosen, phase, quality)
	if err != nil {
		return Plan{}, err
	}
	return Plan{From: req.SourceRate, To: chosen, Stages: stages, Compatibility: compatibility}, nil
}

func filterSpec(f Filter) (Phase, Quality, bool) {
	switch f {
	case "", LinearPrecise:
		return Linear, Precise, true
	case MinimumPrecise:
		return Minimum, Precise, true
	case LinearSmooth:
		return Linear, Smooth, true
	case MinimumSmooth:
		return Minimum, Smooth, true
	default:
		return 0, 0, false
	}
}

func standardRate(rate int) bool {
	for _, r := range standardRates {
		if rate == r {
			return true
		}
	}
	return false
}

func compatibleRate(rate, limit int, supported func(int) bool) (int, bool) {
	// none 允许设备原生支持的非标准源率直通；转换目标仍只从标准集合取。
	if supported(rate) {
		return rate, false
	}
	other := 0
	if rate%441 == 0 {
		other = rate / 441 * 480
	} else if rate%480 == 0 {
		other = rate / 480 * 441
	}
	if standardRate(other) && supported(other) {
		return other, true
	}
	for r := rate; r%2 == 0 && r/2 >= 44100; {
		r /= 2
		if standardRate(r) && supported(r) {
			return r, true
		}
	}
	for r := rate; r <= limit/2; {
		r *= 2
		if standardRate(r) && supported(r) {
			return r, true
		}
	}
	return 0, false
}

func conversionStages(from, to int, phase Phase, q Quality) ([]Stage, error) {
	// 高率 DSD 来源降至 DXD 时，2s 级的输出奈奎斯特至少为 352.8 kHz。
	// 不能仅按“源率≥8×基础率”放宽到低目标率，否则会损伤普通 PCM 的有用带宽。
	if (to == 352800 || to == 384000) && from%to == 0 {
		factor := from / to
		if factor >= 4 && catalogRatio(1, factor/2, Stage2) && powerOfTwo(factor) {
			return []Stage{{phase, Stage2, 1, factor / 2}, {phase, Precise, 1, 2}}, nil
		}
	}
	// 为以后 PCM→DSD 的库调用保留窄带分级；Choose 的本批 PCM 上限不会到达这里。
	for _, base := range []int{44100, 48000} {
		if to%base == 0 && to/base >= 64 && powerOfTwo(to/base) && from <= 8*base && (8*base)%from == 0 && powerOfTwo((8*base)/from) {
			factor := to / (8 * base)
			if catalogRatio(factor, 1, Stage2) {
				first, err := catalogStages(from, 8*base, phase, q)
				if err != nil {
					return nil, err
				}
				return append(first, Stage{phase, Stage2, factor, 1}), nil
			}
		}
	}
	return catalogStages(from, to, phase, q)
}

func powerOfTwo(n int) bool { return n > 0 && n&(n-1) == 0 }
