package fade

import (
	"math"
	"testing"
)

func TestCurves(t *testing.T) {
	for _, x := range []float64{0, 0.25, 0.5, 0.75, 1} {
		wantIn := math.Sqrt(math.Sin(math.Pi * x / 2))
		wantOut := math.Pow(math.Cos(math.Pi*x/2), 4)
		if math.Abs(FadeIn(x)-wantIn) > 1e-12 || math.Abs(FadeOut(x)-wantOut) > 1e-12 {
			t.Fatalf("x=%g: FadeIn=%g, FadeOut=%g; want %g, %g", x, FadeIn(x), FadeOut(x), wantIn, wantOut)
		}
	}
}

func TestAlignDown8(t *testing.T) {
	for _, tt := range []struct{ frames, want int }{{0, 0}, {7, 0}, {8, 8}, {9, 8}, {23, 16}} {
		if got := AlignDown8(tt.frames); got != tt.want {
			t.Fatalf("AlignDown8(%d) = %d, want %d", tt.frames, got, tt.want)
		}
	}
	const end, duration = 37, 14
	if cut := AlignDown8(end - duration); cut%8 != 0 || cut+duration > end {
		t.Fatalf("截断窗口越界或未对齐：cut=%d", cut)
	}
}

func TestMixAcrossBlocks(t *testing.T) {
	const frames = 8
	a, b := make([]float64, frames), make([]float64, frames)
	for i := range a {
		a[i], b[i] = 1, 0.5
	}
	whole, split := make([]float64, frames), make([]float64, frames)
	Mix([][]float64{whole}, [][]float64{a}, [][]float64{b}, 0, frames)
	start := 0
	for _, end := range []int{2, 5, frames} {
		Mix([][]float64{split[start:end]}, [][]float64{a[start:end]}, [][]float64{b[start:end]}, start, frames)
		start = end
	}
	for i := range whole {
		x := float64(i) / frames
		want := math.Pow(math.Cos(math.Pi*x/2), 4) + 0.5*math.Sqrt(math.Sin(math.Pi*x/2))
		if whole[i] != split[i] || math.Abs(split[i]-want) > 1e-12 {
			t.Fatalf("帧 %d: whole=%g, split=%g, want=%g", i, whole[i], split[i], want)
		}
	}
}
