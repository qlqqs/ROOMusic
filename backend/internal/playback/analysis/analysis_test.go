package analysis

import (
	"math"
	"strings"
	"testing"
)

func TestAnalysisReadsPCMWindowMaximum(t *testing.T) {
	pcm := make([]byte, windowSamples*2*2)
	// Two 10 ms windows. Each is little-endian signed PCM; the output is the
	// absolute maximum sample from each window.
	pcm[0], pcm[1] = 0x10, 0x00
	pcm[2], pcm[3] = 0x20, 0x00
	pcm[windowSamples*2], pcm[windowSamples*2+1] = 0x00, 0x80 // -32768
	peaks, err := readWindowPeaks(strings.NewReader(string(pcm)))
	if err != nil {
		t.Fatalf("readWindowPeaks: %v", err)
	}
	if len(peaks) != 2 || peaks[0] != 32 || peaks[1] != 32768 {
		t.Fatalf("window peaks = %v, want [32 32768]", peaks)
	}
}

func TestWaveformDownsamplesByMaximumAndFindsSilenceEdges(t *testing.T) {
	windows := make([]uint16, 1205)
	windows[22] = 34 // first window above -60 dBFS
	windows[1202] = 32768
	got := measure(windows, nil, nil)
	if len(got.Waveform) != maxWaveformPoints {
		t.Fatalf("waveform has %d points, want at most %d", len(got.Waveform), maxWaveformPoints)
	}
	if got.Waveform[0] != byte(math.Round(34.0/32768*255)) {
		t.Fatalf("first downsampled peak = %d, want max from first bucket", got.Waveform[0])
	}
	if got.Waveform[len(got.Waveform)-2] != 255 || got.Waveform[len(got.Waveform)-1] != 0 {
		t.Fatalf("last downsampled peaks = %v, expected the penultimate bucket to contain the isolated maximum", got.Waveform[len(got.Waveform)-2:])
	}
	if got.LeadingSilenceMS == nil || *got.LeadingSilenceMS != 220 {
		t.Fatalf("leading silence = %v, want 220ms", got.LeadingSilenceMS)
	}
	if got.TrailingSilenceMS == nil || *got.TrailingSilenceMS != 12030 {
		t.Fatalf("trailing silence = %v, want 12030ms", got.TrailingSilenceMS)
	}
	if got.DurationMS == nil || *got.DurationMS != 12050 {
		t.Fatalf("duration = %v, want 12050ms", got.DurationMS)
	}
}

func TestWaveformDoesNotExceedLimitAndSilenceIsNull(t *testing.T) {
	got := measure(make([]uint16, 1001), nil, nil)
	if len(got.Waveform) != 1000 {
		t.Fatalf("waveform points = %d, want 1000", len(got.Waveform))
	}
	if got.LeadingSilenceMS != nil || got.TrailingSilenceMS != nil {
		t.Fatalf("silence markers = (%v, %v), want null for all-silent input", got.LeadingSilenceMS, got.TrailingSilenceMS)
	}
}

func TestEbur128SummaryParser(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		text                           string
		wantI, wantTP, wantLRA, wantSP *float64
		wantErr                        bool
	}{
		{
			name:  "完整摘要且两种峰值不同",
			text:  "[Parsed_ebur128] Summary:\nIntegrated loudness:\n  I: -22.4 LUFS\nLoudness range:\n  LRA: 6.3 LU\n  LRA low: -28.0 LUFS\n  LRA high: -21.7 LUFS\nSample peak:\n  Peak: -6.7 dBFS\nTrue peak:\n  Peak: -6.1 dBFS\n",
			wantI: floatPointer(-22.4), wantTP: floatPointer(-6.1), wantLRA: floatPointer(6.3), wantSP: floatPointer(-6.7),
		},
		{
			name:  "缺少两个可选段仍成功",
			text:  "Integrated loudness: I: -22.4 LUFS\nTrue peak: Peak: -6.1 dBFS\n",
			wantI: floatPointer(-22.4), wantTP: floatPointer(-6.1),
		},
		{
			name:  "采样峰值负无穷不影响其他值",
			text:  "Integrated loudness: I: -22.4 LUFS\nLoudness range: LRA: 0.0 LU\nSample peak: Peak: -inf dBFS\nTrue peak: Peak: -6.1 dBFS\n",
			wantI: floatPointer(-22.4), wantTP: floatPointer(-6.1), wantLRA: floatPointer(0),
		},
		{
			name: "全静音负无穷清空响度范围",
			text: "Integrated loudness: I: -inf LUFS\nLoudness range: LRA: 0.0 LU\nSample peak: Peak: -inf dBFS\nTrue peak: Peak: -inf dBFS\n",
		},
		{
			name:   "响度等于静音阈值清空范围但保留峰值",
			text:   "Integrated loudness: I: -70.0 LUFS\nLoudness range: LRA: 1.2 LU\nSample peak: Peak: -68.0 dBFS\nTrue peak: Peak: -67.9 dBFS\n",
			wantTP: floatPointer(-67.9), wantSP: floatPointer(-68),
		},
		{
			name:   "响度低于静音阈值清空范围",
			text:   "Integrated loudness: I: -71.0 LUFS\nLoudness range: LRA: 1.2 LU\nTrue peak: Peak: -68.9 dBFS\n",
			wantTP: floatPointer(-68.9),
		},
		{name: "缺少必需真峰值摘要", text: "Integrated loudness: I: -22.4 LUFS\n", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotI, gotTP, gotLRA, gotSP, err := parseSummary(tc.text)
			if (err != nil) != tc.wantErr {
				t.Fatalf("parseSummary error = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			for _, measurement := range []struct {
				name      string
				got, want *float64
			}{
				{"积分响度", gotI, tc.wantI},
				{"真峰值", gotTP, tc.wantTP},
				{"响度范围", gotLRA, tc.wantLRA},
				{"采样峰值", gotSP, tc.wantSP},
			} {
				if (measurement.got == nil) != (measurement.want == nil) {
					t.Errorf("%s 的空值状态有误：实际 %v，期望 %v", measurement.name, measurement.got, measurement.want)
				} else if measurement.got != nil && *measurement.got != *measurement.want {
					t.Errorf("%s = %v，期望 %v", measurement.name, *measurement.got, *measurement.want)
				}
			}
		})
	}
}

func floatPointer(v float64) *float64 { return &v }

func TestEbur128SummaryMissingMeasurementsIsParseError(t *testing.T) {
	_, _, _, _, err := parseSummary(strings.TrimSpace("summary omitted"))
	if err == nil {
		t.Fatal("parseSummary accepted missing ebur128 summary")
	}
}
