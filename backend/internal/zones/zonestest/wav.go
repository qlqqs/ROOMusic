package zonestest

import (
	"encoding/binary"
	"io"
	"sync"
	"sync/atomic"

	"github.com/qlqq/roomusic/backend/internal/zones"
)

// silenceWAV 不缓存整首音频；未知长度 WAV 的 PCM 样本按读取需求生成。
// 测试用 Body 不是播放时钟，曲目推进由 Advance/Observe 驱动。
type silenceWAV struct {
	mu     sync.Mutex
	header []byte
	done   <-chan struct{}
	ended  atomic.Bool
}

func newSilenceWAV(done <-chan struct{}, format zones.StreamFormat) *silenceWAV {
	header := make([]byte, 44)
	copy(header, "RIFF")
	binary.LittleEndian.PutUint32(header[4:], 0xffffffff)
	copy(header[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(header[16:], 16)
	binary.LittleEndian.PutUint16(header[20:], 1)
	binary.LittleEndian.PutUint16(header[22:], uint16(format.Channels))
	binary.LittleEndian.PutUint32(header[24:], uint32(format.SampleRate))
	frameBytes := format.Channels * format.BitDepth / 8
	binary.LittleEndian.PutUint32(header[28:], uint32(format.SampleRate*frameBytes))
	binary.LittleEndian.PutUint16(header[32:], uint16(frameBytes))
	binary.LittleEndian.PutUint16(header[34:], uint16(format.BitDepth))
	copy(header[36:], "data")
	binary.LittleEndian.PutUint32(header[40:], 0xffffffff)
	return &silenceWAV{header: header, done: done}
}

func (r *silenceWAV) end() { r.ended.Store(true) }
func (r *silenceWAV) Read(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	select {
	case <-r.done:
		return 0, io.EOF
	default:
	}
	if len(p) == 0 {
		return 0, nil
	}
	if len(r.header) > 0 {
		n := copy(p, r.header)
		r.header = r.header[n:]
		return n, nil
	}
	if r.ended.Load() {
		return 0, io.EOF
	}
	clear(p)
	return len(p), nil
}
