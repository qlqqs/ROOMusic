package fakerenderer

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"time"
)

func (r *Renderer) pull(ctx context.Context, uri string) {
	defer r.pulls.Done()
	defer func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if ctx.Err() == nil {
			r.elapsed = r.elapsedLocked()
			r.started = time.Time{}
			r.state.TransportState = "STOPPED"
			r.cancel()
			r.cancel = nil
			r.signalLocked()
		}
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}

	reader := bufio.NewReader(resp.Body)
	format := readFormat(reader)
	r.mu.Lock()
	if ctx.Err() != nil {
		r.mu.Unlock()
		return
	}
	r.state.SampleRate, r.state.BitDepth = format.sampleRate, format.bitDepth
	r.mu.Unlock()

	var target time.Duration
	account := func(n int, duration time.Duration) bool {
		r.mu.Lock()
		if ctx.Err() != nil {
			r.mu.Unlock()
			return false
		}
		r.state.BytesRead += int64(n)
		r.mu.Unlock()
		target += duration
		return r.waitPlayed(ctx, target)
	}
	if format.flac {
		playFLAC(reader, format.sampleRate, account)
		return
	}
	var block [8192]byte
	for {
		n, err := reader.Read(block[:])
		if n > 0 && !account(n, time.Duration(n)*time.Second/time.Duration(format.bytesPerSecond)) {
			return
		}
		if err != nil {
			return
		}
	}
}

func (r *Renderer) waitPlayed(ctx context.Context, target time.Duration) bool {
	for {
		r.mu.Lock()
		remaining := target - r.elapsedLocked()
		playing := r.state.TransportState == "PLAYING"
		changed := r.changed
		r.mu.Unlock()
		if ctx.Err() != nil {
			return false
		}
		if playing && remaining <= 0 {
			return true
		}
		var tick <-chan time.Time
		var timer *time.Timer
		if playing {
			timer = time.NewTimer(remaining)
			tick = timer.C
		}
		select {
		case <-ctx.Done():
		case <-changed:
		case <-tick:
		}
		if timer != nil {
			timer.Stop()
		}
	}
}

type audioFormat struct {
	sampleRate, bitDepth, bytesPerSecond int
	flac                                 bool
}

func readFormat(r *bufio.Reader) audioFormat {
	f := audioFormat{bytesPerSecond: 48000 * 2 * 2}
	header, _ := r.Peek(12)
	if len(header) < 12 {
		return f
	}
	switch {
	case string(header[:4]) == "fLaC":
		_, _ = r.Discard(4)
		for {
			var h [4]byte
			if _, err := io.ReadFull(r, h[:]); err != nil {
				return f
			}
			size := int(h[1])<<16 | int(h[2])<<8 | int(h[3])
			if h[0]&0x7f == 0 && size == 34 {
				var b [34]byte
				if _, err := io.ReadFull(r, b[:]); err != nil {
					return f
				}
				f.sampleRate = int(b[10])<<12 | int(b[11])<<4 | int(b[12]>>4)
				f.bitDepth = (int(b[12]&1)<<4 | int(b[13]>>4)) + 1
				f.flac = f.sampleRate > 0
			} else if _, err := r.Discard(size); err != nil {
				return f
			}
			if h[0]&0x80 != 0 {
				return f
			}
		}
	case string(header[:4]) == "RIFF" && string(header[8:12]) == "WAVE":
		_, _ = r.Discard(12)
		for {
			var h [8]byte
			if _, err := io.ReadFull(r, h[:]); err != nil {
				return f
			}
			size := int64(binary.LittleEndian.Uint32(h[4:]))
			switch string(h[:4]) {
			case "data":
				return f
			case "fmt ":
				if size < 16 {
					return f
				}
				var b [16]byte
				if _, err := io.ReadFull(r, b[:]); err != nil {
					return f
				}
				f.sampleRate = int(binary.LittleEndian.Uint32(b[4:8]))
				f.bitDepth = int(binary.LittleEndian.Uint16(b[14:]))
				if rate := binary.LittleEndian.Uint32(b[8:12]); rate > 0 {
					f.bytesPerSecond = int(rate)
				}
				size -= 16
			}
			if _, err := io.CopyN(io.Discard, r, size+size%2); err != nil {
				return f
			}
		}
	}
	return f
}

// 连续 FLAC 没有总时长，压缩的静音帧也可能只有几十字节。按每帧的样本数节流。
func playFLAC(r *bufio.Reader, rate int, account func(int, time.Duration) bool) {
	consumed := 0
	for {
		b, err := r.ReadSlice(0xff)
		consumed += len(b)
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			account(consumed, 0)
			return
		}
		h, _ := r.Peek(15)
		samples := frameSamples(h)
		if samples > 0 {
			if !account(consumed, time.Duration(samples)*time.Second/time.Duration(rate)) {
				return
			}
			consumed = 0
		}
	}
}

func frameSamples(h []byte) int {
	// h 从同步码 0xff 后开始；块大小编码、UTF-8 帧号和 CRC-8 均来自帧头。
	if len(h) < 5 || h[0]&0xfe != 0xf8 || h[2]&1 != 0 {
		return 0
	}
	block, rate := h[1]>>4, h[1]&15
	if block == 0 || rate == 15 || h[2]>>4 > 10 {
		return 0
	}
	i := 4
	if h[3]&0x80 != 0 {
		count := 0
		for mask := byte(0x80); mask != 0 && h[3]&mask != 0; mask >>= 1 {
			count++
		}
		if count < 2 || count > 7 || 3+count >= len(h) {
			return 0
		}
		for _, b := range h[4 : 3+count] {
			if b&0xc0 != 0x80 {
				return 0
			}
		}
		i = 3 + count
	}
	var samples int
	switch block {
	case 1:
		samples = 192
	case 2, 3, 4, 5:
		samples = 576 << int(block-2)
	case 6:
		if i >= len(h) {
			return 0
		}
		samples = int(h[i]) + 1
		i++
	case 7:
		if i+2 > len(h) {
			return 0
		}
		samples = int(binary.BigEndian.Uint16(h[i:i+2])) + 1
		i += 2
	default:
		samples = 256 << int(block-8)
	}
	switch rate {
	case 12:
		i++
	case 13, 14:
		i += 2
	}
	if i >= len(h) {
		return 0
	}
	crc := byte(0xf3) // 多项式 0x07，首字节 0xff 的余数。
	for _, b := range h[:i] {
		crc ^= b
		for range 8 {
			if crc&0x80 != 0 {
				crc = (crc << 1) ^ 7
			} else {
				crc <<= 1
			}
		}
	}
	if crc != h[i] {
		return 0
	}
	return samples
}
