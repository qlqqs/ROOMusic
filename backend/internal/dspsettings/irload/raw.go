package irload

import (
	"encoding/binary"
	"fmt"
	"math"
	"strings"
)

// ParseRaw 解析单声道的小端浮点 IR；extension 为 .pcm 或 .dbl。
// 采样率由调用方从 cfg 取得；不进行重采样、归一或声道复制。
func ParseRaw(data []byte, extension string) ([]float64, error) {
	width := 0
	switch strings.ToLower(extension) {
	case ".pcm":
		width = 4
	case ".dbl":
		width = 8
	default:
		return nil, fmt.Errorf("irload: unsupported raw IR format")
	}
	if len(data) == 0 || len(data)%width != 0 || len(data)/width > 1<<19 {
		return nil, fmt.Errorf("irload: invalid raw IR length")
	}
	samples := make([]float64, len(data)/width)
	for i := range samples {
		if width == 4 {
			samples[i] = float64(math.Float32frombits(binary.LittleEndian.Uint32(data[i*width:])))
		} else {
			samples[i] = math.Float64frombits(binary.LittleEndian.Uint64(data[i*width:]))
		}
		if !finite(samples[i]) {
			return nil, fmt.Errorf("irload: non-finite raw IR sample")
		}
	}
	return samples, nil
}
