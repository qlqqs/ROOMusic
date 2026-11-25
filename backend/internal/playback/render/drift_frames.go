package render

import (
	"encoding/binary"
	"io"
	"math"
	"slices"
	"sort"
	"sync"
)

const (
	driftFrameInterval    = 2000
	driftPPMUnit          = 1e6
	driftMaxPPM           = driftPPMUnit / driftFrameInterval
	driftLookaheadSeconds = 3
)

type driftCheckpoint struct {
	start, output, adjustment int64
}

type driftFrames struct {
	mu                          sync.Mutex
	requested, written, applied int64
	ppm, credit                 float64 // credit 的单位是百万分之一帧，保留跨块的小数余量。
	history                     []driftCheckpoint
	maxBlock                    int
}

func (d *driftFrames) Adjust(frames int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.requested += frames
}

func (d *driftFrames) SetRate(ppm float64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.ppm = min(driftMaxPPM, max(-driftMaxPPM, ppm))
}

// RenderState 同时取得渲染位置和在途调整，供协调器将残差外推到同一个输出位置。
func (d *driftFrames) RenderState(output int64) (head, pending int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.written, d.requested - d.adjustmentAt(output)
}

func (d *driftFrames) adjustmentAt(output int64) int64 {
	i := sort.Search(len(d.history), func(i int) bool { return d.history[i].output > output })
	var prior int64
	if i > 0 {
		prior = d.history[i-1].adjustment
	}
	if i < len(d.history) {
		// 块的整数净调整记在右端点。块内源位置随进度线性变化，
		// 过半时最近整数已是右端的调整量，恰好半帧时取右端。
		b := d.history[i]
		if output >= b.start+(b.output-b.start+1)/2 {
			return b.adjustment
		}
	}
	return prior
}

func (d *driftFrames) Source(output int64) int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return output + d.adjustmentAt(output)
}

func (d *driftFrames) Pending(output int64) int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.requested - d.adjustmentAt(output)
}

// nextBlock 在完整输入和右侧上下文到齐后才记账，读写管道和插值均在锁外。
// 未就绪时 need 是当前所需的源帧总数，下次读取后可按最新速率与欠量重新排块。
func (d *driftFrames) nextBlock(available int, eof bool) (length, step, need int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	length = driftFrameInterval
	if d.requested == d.applied && d.ppm != 0 {
		until := math.Ceil((driftPPMUnit - math.Copysign(1, d.ppm)*d.credit) / math.Abs(d.ppm))
		limit := d.maxBlock
		if limit == 0 {
			limit = 96000
		}
		// 极慢漂移可能要几分钟才满一帧。先直通前段，避免等整段输入时耗尽播放器缓冲。
		if until > float64(limit) {
			length = int(min(float64(length), until-float64(limit)))
		} else {
			length = max(length, int(until))
		}
	}
	credit := d.credit + float64(length)*d.ppm
	whole := int64(credit / driftPPMUnit)
	debt := d.requested + whole - d.applied
	switch {
	case debt > 0:
		step = 1
	case debt < 0:
		step = -1
	}
	input := length + step
	if !eof && available < input+driftSincRadius {
		return 0, 0, input + driftSincRadius
	}
	if available < input {
		// 不足一次拉伸的尾段逐帧直通。
		length, step = available, 0
		credit = d.credit + float64(length)*d.ppm
		whole = int64(credit / driftPPMUnit)
	}
	d.credit = credit - float64(whole)*driftPPMUnit
	d.requested += whole
	start := d.written
	d.written += int64(length)
	if step != 0 {
		d.applied += int64(step)
		d.history = append(d.history, driftCheckpoint{start: start, output: d.written, adjustment: d.applied})
	}
	return length, step, 0
}

func (d *driftFrames) copyPCM(src io.Reader, dst io.Writer, width, channels int) error {
	frameSize := channels * width
	prefix := driftSincRadius * frameSize
	window := make([]byte, prefix, prefix+(driftFrameInterval+driftSincRadius+1)*frameSize)
	var output []byte
	offset := 0
	first, eof := true, false
	for {
		available := (len(window) - offset - prefix) / frameSize
		if eof && available == 0 {
			return nil
		}
		length, step, need := d.nextBlock(available, eof)
		if need > 0 {
			if offset > 0 {
				window = window[:copy(window, window[offset:])]
				offset = 0
			}
			size := min(need-available, driftFrameInterval) * frameSize
			start := len(window)
			window = slices.Grow(window, size)[:start+size]
			n, err := io.ReadFull(src, window[start:])
			window = window[:start+n]
			if n%frameSize != 0 {
				return io.ErrUnexpectedEOF
			}
			if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
				return err
			}
			eof = err != nil
			if first && n > 0 {
				for off := 0; off < prefix; off += frameSize {
					copy(window[off:], window[prefix:prefix+frameSize])
				}
				first = false
			}
			continue
		}
		inputBytes := (length + step) * frameSize
		context := window[offset:]
		block := context[prefix : prefix+inputBytes]
		if step != 0 {
			// 只有真实流尾缺右侧抽头，按最后一帧延拓；相邻调整块共用真实上下文。
			if missing := prefix + inputBytes + prefix - len(context); missing > 0 {
				context = slices.Grow(context, missing)
				last := window[len(window)-frameSize:]
				for missing > 0 {
					context = append(context, last...)
					missing -= frameSize
				}
			}
			output = slices.Grow(output[:0], length*frameSize)[:length*frameSize]
			stretchPCM(output, context, width, frameSize, length, step)
			block = output
		}
		if err := writeAll(dst, block); err != nil {
			return err
		}
		offset += inputBytes
	}
}

func pcmSample(b []byte, width int) int32 {
	if width == 2 {
		return int32(int16(binary.LittleEndian.Uint16(b)))
	}
	return int32(binary.LittleEndian.Uint16(b)) | int32(int8(b[2]))<<16
}
