package listening

import (
	"math"
	"testing"
)

func TestThreshold(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		duration float64
		want     float64
	}{
		{"unknown duration uses the cap", 0, 240},
		{"negative duration is unknown", -5, 240},
		{"NaN duration is unknown", math.NaN(), 240},
		{"infinite duration is unknown", math.Inf(1), 240},
		{"short track is half its length", 60, 30},
		{"very short track", 1, 0.5},
		{"exactly the crossover is 240", 480, 240},
		{"just below the crossover", 479, 239.5},
		{"long track is capped", 3600, 240},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := Threshold(testCase.duration); got != testCase.want {
				t.Fatalf("Threshold(%v) = %v, want %v", testCase.duration, got, testCase.want)
			}
		})
	}
}

func TestValidPlayedSeconds(t *testing.T) {
	for _, testCase := range []struct {
		value float64
		want  bool
	}{
		{0.001, true},
		{1, true},
		{86400, true},
		{0, false},
		{-1, false},
		{86400.001, false},
		{math.NaN(), false},
		{math.Inf(1), false},
		{math.Inf(-1), false},
	} {
		if got := ValidPlayedSeconds(testCase.value); got != testCase.want {
			t.Errorf("ValidPlayedSeconds(%v) = %v, want %v", testCase.value, got, testCase.want)
		}
	}
}

func TestMeetsThresholdAllowsOneSecondOfTolerance(t *testing.T) {
	for _, testCase := range []struct {
		name             string
		duration, played float64
		want             bool
	}{
		// 时长 100 秒：T = 50，服务端接受 ≥ 49。
		{"exactly T", 100, 50, true},
		{"T minus the tolerance", 100, 49, true},
		{"just below the tolerance", 100, 48.99, false},
		// 长曲目：T = 240，接受 ≥ 239。
		{"long track at T minus tolerance", 3600, 239, true},
		{"long track below T minus tolerance", 3600, 238.9, false},
		// 极短的曲目：T − 1 为负，任何正数都够。
		{"very short track", 1, 0.01, true},
		// 时长未知：没有门槛可依据，只靠数值范围约束（ValidPlayedSeconds）。
		{"unknown duration accepts a short play", 0, 0.5, true},
		{"NaN duration accepts a short play", math.NaN(), 0.5, true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := meetsThreshold(testCase.duration, testCase.played); got != testCase.want {
				t.Fatalf("meetsThreshold(%v, %v) = %v, want %v", testCase.duration, testCase.played, got, testCase.want)
			}
		})
	}
}
