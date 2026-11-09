// Package dsdpcm 把已打开的 DSD 来源转换为定长、已补偿延迟的交错 f32le PCM 流。
package dsdpcm

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/qlqq/roomusic/backend/internal/playback/dsdio"
)

// Reader 同步按需转换，不创建协程，不拥有或关闭来源句柄。
// Read 与 SeekFrame 不可并发调用。取消在每次底层读取前后及处理量子之间检查；
// 若底层 ReaderAt 本身阻塞，调用方须通过其句柄生命周期解除阻塞。
// 相邻分轨应持续消费同一 Reader，按各段帧数限读，不在边界定位或重置。
type Reader struct {
	ctx      context.Context
	source   *dsdio.Reader
	pipeline *pipeline

	byteFrames  int64
	inputLeft   int64
	inputFrames int64
	skip        int64
	remaining   int64
	input       [][]byte
	buffer      []byte
	pending     []byte
	terminal    error
}

// New 使用播放和分析共用的 +6 dB、filter=1 默认值。
// layout 应来自同一已打开来源的 dsdio.Probe；包内不按路径重新打开文件。
func New(ctx context.Context, source io.ReaderAt, layout dsdio.Layout) (*Reader, error) {
	return NewWithOptions(ctx, source, layout, Options{GainDB: 6, Filter: 1})
}

// NewWithOptions 接受显式增益及低通档位；零值表示 0 dB、仅 FIR，绝不补成默认值。
// 构造只准备处理器，不读取音频负载；初始位置是父文件的第零个输出 PCM 帧。
func NewWithOptions(ctx context.Context, source io.ReaderAt, layout dsdio.Layout, options Options) (*Reader, error) {
	if ctx == nil || source == nil {
		return nil, fmt.Errorf("dsdpcm: 上下文和来源不能为空")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	input, err := dsdio.NewReader(contextReaderAt{ctx, source}, layout)
	if err != nil {
		return nil, fmt.Errorf("dsdpcm: 来源布局无效: %w", err)
	}
	pipeline, err := newPipeline(layout, options)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &Reader{
		ctx: ctx, source: input, pipeline: pipeline,
		byteFrames: layout.ByteFrames(), inputLeft: layout.ByteFrames(),
		skip: int64(pipeline.latency), remaining: pipeline.frames,
		input: make([][]byte, layout.Channels),
	}, nil
}

// Format 返回固定的来源 PCM 格式；编码始终为交错 f32le，不含文件头。
func (r *Reader) Format() Format { return r.pipeline.format }

// Frames 是整个父来源的输出帧数；有效 DSD 比特数换算后向下取整，不含尾部填充。
func (r *Reader) Frames() int64 { return r.pipeline.frames }

// Latency 是内部已一次性补偿的输出帧数，调用方不能再次丢弃这段长度。
// 后置 IIR 不申报额外缓冲延迟，但其相位响应保留。
func (r *Reader) Latency() int { return r.pipeline.latency }

// SeekFrame 定位到父来源的绝对输出 PCM 帧，允许 EOF；不是 DSD 比特或字节帧。
// 非零起点从至少一秒前重新进料（靠近文件头则从零开始），按完整抽取周期对齐。
// 这里只重置坐标和历史，前滚由后续 Read 按需执行；非法定位不改变原状态。
func (r *Reader) SeekFrame(frame int64) error {
	if frame < 0 || frame > r.Frames() {
		return fmt.Errorf("dsdpcm: 定位超出来源帧范围")
	}
	if err := r.ctx.Err(); err != nil {
		return err
	}
	start := max(int64(0), frame-r.pipeline.preroll)
	if frame == r.Frames() {
		start = frame
	}
	byteFrame := start * int64(r.pipeline.factor)
	if err := r.source.SeekByteFrame(byteFrame); err != nil {
		return err
	}
	r.pipeline.reset()
	r.inputLeft = r.byteFrames - byteFrame
	r.inputFrames = 0
	r.skip = frame - start + int64(r.pipeline.latency)
	r.remaining = r.Frames() - frame
	r.pending = nil
	r.terminal = nil
	return nil
}

// Read 实现 io.Reader，接受任意字节长度（包括不足一个 PCM 帧的缓冲）。
// 只在整个父来源结束时冲刷静音；读取错误不当作正常 EOF 补造剩余样本。
// 已转换的有效前缀可先返回，随后传播原始错误；取消后不再交付缓冲里的音频。
func (r *Reader) Read(dst []byte) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	for len(r.pending) == 0 {
		if r.terminal != nil {
			return 0, r.terminal
		}
		if r.remaining == 0 {
			return 0, io.EOF
		}
		if err := r.produce(); err != nil {
			r.terminal = err
		}
		if err := r.ctx.Err(); err != nil {
			return 0, err
		}
	}
	n := copy(dst, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

// produce 最多进料一个 200 ms 量子；跳过前滚与延迟时零输出是合法的。
func (r *Reader) produce() error {
	var readErr error
	count := r.pipeline.quantum
	if r.inputLeft > 0 {
		count = int(min(int64(count), r.inputLeft))
		r.prepareInput(count)
		n, err := r.source.Read(r.input)
		if contextErr := r.ctx.Err(); contextErr != nil {
			return contextErr
		}
		r.inputLeft -= int64(n)
		// 请求已经按剩余有效样本限长，短读只能是损坏或底层读取失败。
		if n != count && (err == nil || errors.Is(err, io.EOF)) {
			err = io.ErrUnexpectedEOF
		} else if n == count && errors.Is(err, io.EOF) {
			err = nil
		}
		count, readErr = n, err
		for ch := range r.input {
			r.input[ch] = r.input[ch][:count]
		}
	} else {
		// 仅真实音频 EOF 才填 0x69，并让静音穿过 Converter/IIR/SRC 的全部历史。
		// 考虑 SRC 当前余相，只进料达到目标长度所需的样本，不暴露额外滤波尾巴。
		factor := int64(r.pipeline.factor)
		outputs := min(int64(count)/factor, r.skip+r.remaining)
		count = int(outputs*factor - r.inputFrames%factor)
		r.prepareInput(count)
		for _, channel := range r.input {
			for i := range channel {
				channel[i] = 0x69
			}
		}
	}
	if count == 0 {
		return readErr
	}
	pcm := r.pipeline.process(r.input)
	r.inputFrames += int64(count)
	drop := min(r.skip, int64(len(pcm[0])))
	r.skip -= drop
	keep := int(min(r.remaining, int64(len(pcm[0]))-drop))
	r.pack(pcm, int(drop), keep)
	r.remaining -= int64(keep)
	return readErr
}

func (r *Reader) prepareInput(count int) {
	for ch := range r.input {
		if cap(r.input[ch]) < count {
			r.input[ch] = make([]byte, count)
		} else {
			r.input[ch] = r.input[ch][:count]
		}
	}
}

func (r *Reader) pack(pcm [][]float64, start, frames int) {
	length := frames * len(pcm) * 4
	if cap(r.buffer) < length {
		r.buffer = make([]byte, length)
	} else {
		r.buffer = r.buffer[:length]
	}
	position := 0
	for i := start; i < start+frames; i++ {
		for _, channel := range pcm {
			binary.LittleEndian.PutUint32(r.buffer[position:], math.Float32bits(float32(channel[i])))
			position += 4
		}
	}
	r.pending = r.buffer
}

type contextReaderAt struct {
	ctx    context.Context
	source io.ReaderAt
}

func (r contextReaderAt) ReadAt(dst []byte, offset int64) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.source.ReadAt(dst, offset)
	if contextErr := r.ctx.Err(); contextErr != nil {
		return 0, contextErr
	}
	return n, err
}
