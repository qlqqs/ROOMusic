package irload

import (
	"encoding/binary"
	"math"
	"reflect"
	"testing"
)

func TestParseRawLittleEndian(t *testing.T) {
	want := []float64{0, 1, -0.5, 0.125, -2}
	for _, extension := range []string{".pcm", ".dbl", ".PCM", ".DBL"} {
		t.Run(extension, func(t *testing.T) {
			width := 8
			if extension == ".pcm" || extension == ".PCM" {
				width = 4
			}
			data := rawSamples(want, width)
			got, err := ParseRaw(data, extension)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("ParseRaw() = %v, %v; want %v", got, err, want)
			}
		})
	}
}

func TestParseRawRejectsInvalidData(t *testing.T) {
	for _, tt := range []struct {
		name, extension string
		data            []byte
	}{
		{"unsupported", ".wav", make([]byte, 8)},
		{"empty", ".pcm", nil},
		{"partial_float32", ".pcm", make([]byte, 5)},
		{"partial_float64", ".dbl", make([]byte, 9)},
		{"nan_float32", ".pcm", rawSamples([]float64{math.NaN()}, 4)},
		{"inf_float64", ".dbl", rawSamples([]float64{math.Inf(-1)}, 8)},
		{"too_many_taps", ".pcm", make([]byte, ((1<<19)+1)*4)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseRaw(tt.data, tt.extension); err == nil {
				t.Fatal("ParseRaw 接受了无效数据")
			}
		})
	}
}

func rawSamples(samples []float64, width int) []byte {
	data := make([]byte, width*len(samples))
	for i, v := range samples {
		if width == 4 {
			binary.LittleEndian.PutUint32(data[i*width:], math.Float32bits(float32(v)))
		} else {
			binary.LittleEndian.PutUint64(data[i*width:], math.Float64bits(v))
		}
	}
	return data
}
