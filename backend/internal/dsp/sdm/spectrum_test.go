package sdm

import (
	"math"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/dsp/dsd"
	"github.com/qlqq/roomusic/backend/internal/dsp/internal/fft"
)

func TestRoundTrip(t *testing.T) {
	const samples = 1 << 18
	amplitude := math.Pow(10, -6.0/20)
	input := make([][]float64, 2)
	for ch := range input {
		input[ch] = make([]float64, samples)
		for n := range input[ch] {
			input[ch][n] = amplitude * math.Sin(2*math.Pi*1000*float64(n)/testDSD64Rate+float64(ch)*math.Pi/3)
		}
	}
	for _, algorithm := range testAlgorithms {
		t.Run(algorithm.name, func(t *testing.T) {
			m := newTestModulator(t, algorithm.alg, testDSD64Rate, len(input))
			packed := m.Process(input)
			planar := make([][]byte, len(input))
			for ch := range planar {
				planar[ch] = make([]byte, samples/8)
				for i := range planar[ch] {
					planar[ch][i] = packed[i*len(input)+ch]
				}
			}
			converter, err := dsd.New(len(input), 6)
			if err != nil {
				t.Fatal(err)
			}
			for ch, pcm := range converter.Process(planar) {
				// 保留后半段的 2^14 点，避开调制器和 FIR 的启动瞬态。
				pcm = pcm[len(pcm)/2:]
				const pcmRate = testDSD64Rate / 8
				spectrum, _ := hannSpectrum(pcm)
				peak := bandPeak(spectrum, pcmRate, len(pcm), 20, 20000)
				peakHz := float64(peak*pcmRate) / float64(len(pcm))
				if !(math.Abs(peakHz-1000) <= float64(pcmRate)/float64(len(pcm))) {
					t.Fatalf("声道 %d：峰位 %.6f Hz，不在 1 kHz ±1 频点内", ch, peakHz)
				}
				level, residual := fitTone(pcm, pcmRate, 1000)
				levelError := 20 * math.Log10(level/amplitude)
				if !(math.Abs(levelError) <= 0.1) {
					t.Fatalf("声道 %d：电平差 %.6f dB，期望 ≤0.1 dB", ch, levelError)
				}
				// 1 kHz 不与 2^14 点相干；先拟合并去掉基波，避免窗泄漏被误算为噪声。
				noiseSpectrum, windowPower := hannSpectrum(residual)
				noise := bandPower(noiseSpectrum, pcmRate, len(pcm), 20, 20000, -1)
				signal := level * level * float64(len(pcm)) * windowPower / 4
				snr := 10 * math.Log10(signal/noise)
				t.Logf("声道 %d：峰位 %.3f Hz，电平差 %.6f dB，带内 SNR %.3f dB", ch, peakHz, levelError, snr)
				if !(snr >= 100) {
					t.Fatalf("声道 %d：带内 SNR %.3f dB，期望 ≥100 dB", ch, snr)
				}
			}
		})
	}
}

func TestNoiseShaping(t *testing.T) {
	const samples = 1 << 18
	const warmup = 1 << 15
	const toneBin = 93 // DSD64 下约 1 kHz，整窗周期数为整数。
	const frequency = float64(toneBin*testDSD64Rate) / samples
	input := make([]float64, warmup+samples)
	amplitude := math.Pow(10, -6.0/20)
	for n := range input {
		input[n] = amplitude * math.Sin(2*math.Pi*frequency*float64(n)/testDSD64Rate)
	}
	var snrs [4]float64
	for _, algorithm := range testAlgorithms {
		t.Run(algorithm.name, func(t *testing.T) {
			m := newTestModulator(t, algorithm.alg, testDSD64Rate, 1)
			packed := m.Process([][]float64{input})[warmup/8:]
			bits := make([]float64, samples)
			for n := range bits {
				bits[n] = -1
				if packed[n/8]&(1<<(7-n%8)) != 0 {
					bits[n] = 1
				}
			}
			spectrum, _ := hannSpectrum(bits)
			var signal float64
			for bin := toneBin - 1; bin <= toneBin+1; bin++ {
				signal += spectralPower(spectrum[bin])
			}
			noise := bandPower(spectrum, testDSD64Rate, samples, 20, 20000, toneBin)
			ultrasonic := bandPower(spectrum, testDSD64Rate, samples, 50000, 150000, -1)
			snr := 10 * math.Log10(signal/noise)
			shaping := 10 * math.Log10(ultrasonic/noise)
			snrs[algorithm.alg] = snr
			t.Logf("带内 SNR %.3f dB，50–150 kHz 相对带内噪声功率 %.3f dB", snr, shaping)
			if !(snr >= 110 && shaping >= 60) {
				t.Fatalf("带内 SNR %.3f dB（需 ≥110），带外/带内噪声功率差 %.3f dB（需 ≥60）", snr, shaping)
			}
		})
	}
	for _, family := range []struct {
		name        string
		fifth, last Algorithm
	}{{"标准阶", Order5, Order7}, {"Clans", Clans5, Clans7}} {
		if improvement := snrs[family.last] - snrs[family.fifth]; !(improvement >= 10) {
			t.Errorf("%s 七阶相对五阶 SNR 仅高 %.3f dB，期望 ≥10 dB", family.name, improvement)
		}
	}
}

// 周期 Hann 窗使相干正弦只占峰值及其相邻频点；返回未归一化的正频谱。
func hannSpectrum(samples []float64) ([]complex128, float64) {
	windowed := make([]float64, len(samples))
	var windowPower float64
	for n, sample := range samples {
		w := 0.5 - 0.5*math.Cos(2*math.Pi*float64(n)/float64(len(samples)))
		windowed[n] = sample * w
		windowPower += w * w
	}
	spectrum := make([]complex128, len(samples)/2+1)
	fft.New(len(samples)).RealForward(spectrum, windowed)
	return spectrum, windowPower
}

func spectralPower(v complex128) float64 {
	return real(v)*real(v) + imag(v)*imag(v)
}

func bandPeak(spectrum []complex128, rate, samples int, low, high float64) int {
	first := int(math.Ceil(low * float64(samples) / float64(rate)))
	last := int(math.Floor(high * float64(samples) / float64(rate)))
	peak := first
	for bin := first + 1; bin <= last; bin++ {
		if spectralPower(spectrum[bin]) > spectralPower(spectrum[peak]) {
			peak = bin
		}
	}
	return peak
}

func bandPower(spectrum []complex128, rate, samples int, low, high float64, tone int) float64 {
	first := int(math.Ceil(low * float64(samples) / float64(rate)))
	last := int(math.Floor(high * float64(samples) / float64(rate)))
	var power float64
	for bin := first; bin <= last; bin++ {
		if tone >= 0 && bin >= tone-2 && bin <= tone+2 {
			continue
		}
		power += spectralPower(spectrum[bin])
	}
	return power
}

// Hann 加权二参数最小二乘同时估计正余弦分量，不假设 FIR 的相位或整数群延迟。
func fitTone(samples []float64, rate int, frequency float64) (float64, []float64) {
	var ss, sc, cc, ys, yc float64
	for n, sample := range samples {
		s, c := math.Sincos(2 * math.Pi * frequency * float64(n) / float64(rate))
		w := 0.5 - 0.5*math.Cos(2*math.Pi*float64(n)/float64(len(samples)))
		ss += w * s * s
		sc += w * s * c
		cc += w * c * c
		ys += w * sample * s
		yc += w * sample * c
	}
	determinant := ss*cc - sc*sc
	a, b := (ys*cc-yc*sc)/determinant, (yc*ss-ys*sc)/determinant
	residual := make([]float64, len(samples))
	for n, sample := range samples {
		s, c := math.Sincos(2 * math.Pi * frequency * float64(n) / float64(rate))
		residual[n] = sample - a*s - b*c
	}
	return math.Hypot(a, b), residual
}
