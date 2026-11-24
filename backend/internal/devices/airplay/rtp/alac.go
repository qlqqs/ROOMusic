// Package rtp 负责实时音频的 ALAC 帧、RTP 序列、同步和重传。
package rtp

import "errors"

const FramesPerPacket = 352

type bitWriter struct {
	data []byte
	bits uint32
	n    int
}

func (w *bitWriter) put(value uint32, n int) {
	w.bits = w.bits<<n | value
	w.n += n
	for w.n >= 8 {
		w.n -= 8
		w.data = append(w.data, byte(w.bits>>w.n))
	}
}

// ALAC 将小端交错 PCM 写入一个立体声未压缩帧。
// 行为参考 airplay2-sender-cpp（Apache-2.0）的 CPE、采样端序与结束标记。
func ALAC(pcm []byte, depth int) ([]byte, error) {
	if depth != 16 && depth != 24 {
		return nil, errors.New("ALAC 需要 16 或 24 位 PCM")
	}
	size := depth / 8
	frames := len(pcm) / (2 * size)
	if frames < 1 || frames > FramesPerPacket || len(pcm) != frames*2*size {
		return nil, errors.New("ALAC 立体声帧数无效")
	}
	w := bitWriter{data: make([]byte, 0, len(pcm)+8)}
	w.put(1, 3)
	w.put(0, 16)
	if frames == FramesPerPacket {
		w.put(1, 4)
	} else {
		w.put(9, 4)
		w.put(0, 16)
		w.put(uint32(frames), 16)
	}
	for len(pcm) > 0 {
		sample := uint32(pcm[0]) | uint32(pcm[1])<<8
		if size == 3 {
			sample |= uint32(pcm[2]) << 16
		}
		w.put(sample, depth)
		pcm = pcm[size:]
	}
	w.put(7, 3)
	if w.n > 0 {
		w.put(0, 8-w.n)
	}
	return w.data, nil
}
