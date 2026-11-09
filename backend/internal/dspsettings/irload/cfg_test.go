package irload

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseCFGLayoutsAndWeights(t *testing.T) {
	const paths = "Room filter.pcm\n0\n0.0 1.5\n1.0\nRoom filter.pcm\n0\n1.0\n-0.99999\n"
	for _, tt := range []struct {
		name, header string
		pathLine     int
	}{
		{"split_header", "48000\n2\n2\n3\n1 0.25\n0 0.5\n", 7},
		{"single_header", "48000 2 2 3\n1 0.25\n0 0.5\n", 4},
		{"combined_delays", "48000 2 2 3\n1 0.25 0 0.5\n", 3},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseCFG([]byte(tt.header + paths))
			if err != nil {
				t.Fatal(err)
			}
			want := &CFG{SampleRate: 48000, InputChannels: 2, OutputChannels: 2, OutputMask: 3,
				InDelayMS: []float64{1, 0.25}, OutDelayMS: []float64{0, 0.5}, Paths: []CFGPath{
					{File: "Room filter.pcm", Channel: 0, Inputs: []Weighted{{0, 1}, {1, 0.5}}, Outputs: []Weighted{{1, 1}}, Line: tt.pathLine},
					{File: "Room filter.pcm", Channel: 0, Inputs: []Weighted{{1, 1}}, Outputs: []Weighted{{0, -0.99999}}, Line: tt.pathLine + 4},
				}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("ParseCFG() = %+v, want %+v", got, want)
			}
		})
	}
}

func TestParseCFGErrorLine(t *testing.T) {
	for _, tt := range []struct {
		name, data string
		line       int
	}{
		{"header", "\n48000\nno\n2\n3\n", 3},
		{"mask", "48000 2 2 not_hex\n", 1},
		{"delay", "48000 2 2 3\n0 -1\n0 0\n", 2},
		{"file_channel", "48000 2 2 3\n0 0\n0 0\n\nroom.pcm\nno\n0.0\n1.0\n", 6},
		{"weight", "48000 2 2 3\n0 0\n0 0\nroom.pcm\n0\n1.bad\n0.0\n", 6},
		{"routing_channel", "48000 2 2 3\n0 0\n0 0\nroom.pcm\n0\n0.0\n2.0\n", 7},
		{"missing_output", "48000 2 2 3\n0 0\n0 0\nroom.pcm\n0\n0.0\n", 7},
		{"file_list", "first.cfg\nsecond.wav\n", 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseCFG([]byte(tt.data))
			assertCFGErrorLine(t, err, tt.line)
		})
	}
}

func TestDelaySamplesFloorsAtTargetRate(t *testing.T) {
	for _, tt := range []struct {
		ms   float64
		rate int
		want int
	}{{1, 48000, 48}, {1, 44100, 44}, {0.25, 44100, 11}, {0, 48000, 0}} {
		got, err := DelaySamples(tt.ms, tt.rate)
		if err != nil || got != tt.want {
			t.Fatalf("DelaySamples(%g, %d) = %d, %v; want %d", tt.ms, tt.rate, got, err, tt.want)
		}
	}
}

func assertCFGErrorLine(t *testing.T, err error, line int) {
	t.Helper()
	var cfgErr *CFGError
	if !errors.As(err, &cfgErr) || cfgErr.Line != line || !strings.Contains(err.Error(), "ir_cfg_invalid") {
		t.Fatalf("错误 = %v, want CFGError（ir_cfg_invalid）, line=%d", err, line)
	}
}
