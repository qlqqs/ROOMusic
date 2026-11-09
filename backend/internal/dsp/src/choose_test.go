package src

import (
	"errors"
	"reflect"
	"testing"
)

func TestChooseStrategiesAndCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name            string
		from            int
		strategy        string
		custom, maxRate int
		supported       []int
		to              int
		compatibility   bool
		err             error
	}{
		{name: "none_原率优先", from: 96000, strategy: "none", supported: []int{44100, 48000, 88200, 96000, 192000}, to: 96000},
		{name: "最高率封顶768k", from: 44100, strategy: "max_sample_rate", maxRate: 1536000, supported: []int{44100, 705600, 768000, 1536000}, to: 768000},
		{name: "2的幂跳过不支持档", from: 44100, strategy: "power_of_2", maxRate: 384000, supported: []int{44100, 176400, 384000}, to: 176400},
		{name: "dsd不可用", from: 44100, strategy: "dsd", err: ErrStrategyUnavailable},
		{name: "指定率", from: 44100, strategy: "custom", custom: 96000, supported: []int{44100, 96000}, to: 96000},
		{name: "custom缺值按none", from: 44100, strategy: "custom", supported: []int{44100, 96000}, to: 44100},
		{name: "换族先于减半与倍增", from: 96000, strategy: "none", supported: []int{48000, 88200, 192000}, to: 88200, compatibility: true},
		{name: "44族换48族", from: 88200, strategy: "none", supported: []int{44100, 96000, 176400}, to: 96000, compatibility: true},
		{name: "减半先于倍增", from: 96000, strategy: "none", supported: []int{48000, 192000}, to: 48000, compatibility: true},
		{name: "连续减半", from: 192000, strategy: "none", supported: []int{48000, 384000}, to: 48000, compatibility: true},
		{name: "从原率连续倍增", from: 44100, strategy: "none", supported: []int{176400, 352800}, to: 176400, compatibility: true},
		{name: "DXD到192k上限", from: 352800, strategy: "none", maxRate: 192000, supported: []int{44100, 48000, 88200, 96000, 176400, 192000, 352800, 384000}, to: 176400, compatibility: true},
		{name: "custom目标回退", from: 44100, strategy: "custom", custom: 192000, supported: []int{96000, 176400}, to: 176400, compatibility: true},
		{name: "低率源倍增", from: 22050, strategy: "none", supported: []int{44100, 88200}, to: 44100, compatibility: true},
		{name: "非标准原率直通", from: 32000, strategy: "none", supported: []int{32000, 48000}, to: 32000},
		{name: "无支持率", from: 44100, strategy: "none", err: ErrNoSupportedRate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := Choose(Request{SourceRate: tc.from, Strategy: tc.strategy, CustomRate: tc.custom, MaxRate: tc.maxRate, Supported: func(rate int) bool {
				for _, r := range tc.supported {
					if r == rate {
						return true
					}
				}
				return false
			}})
			if !errors.Is(err, tc.err) {
				t.Fatalf("Choose 错误 = %v，期望 %v", err, tc.err)
			}
			if tc.err != nil {
				return
			}
			if plan.From != tc.from || plan.To != tc.to || plan.Compatibility != tc.compatibility {
				t.Fatalf("Choose = %+v，期望 %d→%d compatibility=%t", plan, tc.from, tc.to, tc.compatibility)
			}
			if plan.From == plan.To && len(plan.Stages) != 0 {
				t.Error("无变率仍有处理级")
			}
			for _, stage := range plan.Stages {
				if stage.Phase != Linear || stage.Quality != Precise {
					t.Errorf("默认滤波器不符：%+v", stage)
				}
			}
		})
	}
}

func TestChooseStages(t *testing.T) {
	for _, tc := range []struct {
		name      string
		from, to  int
		ratios    [][2]int
		qualities []Quality
	}{
		{"目录跨族单级", 44100, 48000, [][2]int{{160, 147}}, []Quality{Precise}},
		{"反向目录单级", 48000, 44100, [][2]int{{147, 160}}, []Quality{Precise}},
		{"普通PCM64倍不分级", 11025, 705600, [][2]int{{64, 1}}, []Quality{Precise}},
		{"普通PCM8倍降频不用2s", 352800, 44100, [][2]int{{1, 8}}, []Quality{Precise}},
		{"高率源降至普通PCM不用2s", 1411200, 176400, [][2]int{{1, 8}}, []Quality{Precise}},
		{"32到44_1先升后降", 32000, 44100, [][2]int{{3, 2}, {147, 160}}, []Quality{Precise, Precise}},
		{"32到48最少单级", 32000, 48000, [][2]int{{3, 2}}, []Quality{Precise}},
		{"跨族两级选最小2的幂", 44100, 768000, [][2]int{{2, 1}, {1280, 147}}, []Quality{Precise, Precise}},
		{"DSD256到DXD", 1411200, 352800, [][2]int{{1, 2}, {1, 2}}, []Quality{Stage2, Precise}},
		{"DSD512到DXD", 2822400, 352800, [][2]int{{1, 4}, {1, 2}}, []Quality{Stage2, Precise}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := Choose(Request{SourceRate: tc.from, Strategy: "custom", CustomRate: tc.to, Supported: func(r int) bool { return r == tc.to }})
			if err != nil {
				t.Fatal(err)
			}
			want := make([]Stage, len(tc.ratios))
			for i, r := range tc.ratios {
				want[i] = Stage{Linear, tc.qualities[i], r[0], r[1]}
			}
			// 两级纯升频的先后不影响设计的最少级数/幂代价约束。
			if tc.name == "跨族两级选最小2的幂" && len(plan.Stages) == 2 && plan.Stages[0].L == 1280 {
				want[0], want[1] = want[1], want[0]
			}
			if !reflect.DeepEqual(plan.Stages, want) {
				t.Errorf("处理级 = %+v，期望 %+v", plan.Stages, want)
			}
			numerator, denominator := tc.from, 1
			down := false
			for _, s := range plan.Stages {
				if s.L < s.M {
					down = true
				} else if down {
					t.Error("降频后又升频")
				}
				numerator *= s.L
				denominator *= s.M
				if numerator < min(tc.from, tc.to)*denominator {
					t.Error("中间率低于源率和目标率的较小值")
				}
			}
			if numerator != tc.to*denominator {
				t.Error("级联比率不等于目标比率")
			}
		})
	}
}

func TestChoosePreservesFilter(t *testing.T) {
	for _, tc := range []struct {
		filter  Filter
		phase   Phase
		quality Quality
	}{
		{LinearPrecise, Linear, Precise}, {MinimumPrecise, Minimum, Precise}, {LinearSmooth, Linear, Smooth}, {MinimumSmooth, Minimum, Smooth},
	} {
		t.Run(string(tc.filter), func(t *testing.T) {
			plan, err := Choose(Request{SourceRate: 32000, Strategy: "custom", CustomRate: 44100, Filter: tc.filter, Supported: func(r int) bool { return r == 44100 }})
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Stages) != 2 {
				t.Fatalf("处理级数 = %d，期望 2", len(plan.Stages))
			}
			for _, s := range plan.Stages {
				if s.Phase != tc.phase || s.Quality != tc.quality {
					t.Errorf("用户滤波器未传递到每一级：%+v", s)
				}
			}
		})
	}
}
