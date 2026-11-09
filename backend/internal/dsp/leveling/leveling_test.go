package leveling

import (
	"math"
	"reflect"
	"testing"
)

func TestChoose(t *testing.T) {
	track, album := -2.0, 7.8
	tests := []struct {
		name         string
		mode         Mode
		track, album *float64
		context      bool
		gain         float64
		basis        Basis
		ok           bool
	}{
		{"关闭", Off, nil, nil, false, 0, "", false},
		{"曲目", Track, &track, nil, false, track, BasisTrack, true},
		{"专辑", Album, &track, &album, false, album, BasisAlbum, true},
		{"专辑退回曲目", Album, &track, nil, false, track, BasisTrack, true},
		{"自动专辑上下文", Auto, &track, &album, true, album, BasisAlbum, true},
		{"自动曲目上下文", Auto, &track, &album, false, track, BasisTrack, true},
		{"自动专辑退回曲目", Auto, &track, nil, true, track, BasisTrack, true},
		{"未知固定负五分贝", Track, nil, nil, false, -5, BasisUnknown, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gain, basis, ok := Choose(tt.mode, tt.track, tt.album, tt.context)
			if gain != tt.gain || basis != tt.basis || ok != tt.ok {
				t.Fatalf("Choose = (%g, %q, %v), want (%g, %q, %v)", gain, basis, ok, tt.gain, tt.basis, tt.ok)
			}
		})
	}
	if got, want := Linear(album), math.Pow(10, album/20); math.Abs(got-want) > 1e-12 || got <= 1 {
		t.Fatalf("正增益被截断：Linear(%g) = %g, want %g", album, got, want)
	}
}

func TestApply(t *testing.T) {
	for _, gain := range []float64{1, 0.5} {
		samples := [][]float64{{1, 2, 3}, {-1, -2, -3}}
		regions := []GainRegion{{Offset: 1, Frames: 1, Gain: gain}}
		exact := true
		Apply(samples, &regions, &exact)
		want := [][]float64{{1, 2 * gain, 3}, {-1, -2 * gain, -3}}
		if !reflect.DeepEqual(samples, want) || exact != (gain == 1) || len(regions) != 0 {
			t.Fatalf("gain=%g: samples=%v, Exact=%v, regions=%v", gain, samples, exact, regions)
		}
	}
}
