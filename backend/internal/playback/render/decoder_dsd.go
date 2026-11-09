package render

import (
	"context"
	"errors"
	"io"
	"os"

	"github.com/qlqq/roomusic/backend/internal/dsp/chain"
	"github.com/qlqq/roomusic/backend/internal/dsp/leveling"
	"github.com/qlqq/roomusic/backend/internal/playback/dsdpcm"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

// dsdProducer 拥有父来源、转换历史和字节环。引用计数仅由 player 协程维护；
// 分轨 EOF 不结束生产者，最后一段释放或会话取消才中止填充。
type dsdProducer struct {
	cancel context.CancelFunc
	done   chan struct{}
	ring   *byteRing
	info   os.FileInfo
	refs   int
}

func openDSDDecoder(ctx context.Context, track Track, entry zones.Entry, format chain.Format, startSeconds float64, mode string, span pcmRange, capacity int) (*decoder, error) {
	child, cancel := context.WithCancel(ctx)
	reader, err := dsdpcm.New(child, track.File, *track.DSDLayout)
	if err == nil {
		err = reader.SeekFrame(span.start)
	}
	if err != nil {
		cancel()
		return nil, &SourceError{Reason: "decode_failed", Err: err}
	}
	info, err := track.File.Stat()
	if err != nil {
		cancel()
		return nil, &SourceError{Reason: "decode_failed", Err: err}
	}
	producer := &dsdProducer{cancel: cancel, done: make(chan struct{}), ring: newByteRing(capacity), info: info, refs: 1}
	d := dsdSegment(producer, track, entry, format, startSeconds, mode, span)
	go producer.fill(child, track.File, reader, format.Channels*4)
	return d, nil
}

func dsdSegment(producer *dsdProducer, track Track, entry zones.Entry, format chain.Format, startSeconds float64, mode string, span pcmRange) *decoder {
	gainDB, _, enabled := leveling.Choose(leveling.Mode(mode), track.TrackGainDB, track.AlbumGainDB, entry.AlbumContext)
	d := &decoder{track: track, entry: entry, producer: producer, ring: producer.ring, format: format,
		gain: leveling.Linear(gainDB), leveling: enabled, startSeconds: startSeconds}
	if span.end != nil {
		frames := *span.end - span.start
		d.segmentFrames = &frames
	}
	return d
}

// continueDSD 只接续同父同版本的相邻绝对边界；预取不消费环，也不重置任何滤波器。
// 后继安全句柄只用于验证，音频仍由原生产者读取；已到父 EOF 也保留初始 Stat 快照。
func (d *decoder) continueDSD(track Track, entry zones.Entry, mode string) *decoder {
	if d.producer == nil || d.track.CueStartFrame == nil || d.track.CueEndFrame == nil || track.CueStartFrame == nil || track.DSDLayout == nil ||
		*d.track.CueEndFrame != *track.CueStartFrame || *d.track.DSDLayout != *track.DSDLayout {
		return nil
	}
	info, err := track.File.Stat()
	previous := d.producer.info
	if err != nil || !os.SameFile(previous, info) || previous.Size() != info.Size() || !previous.ModTime().Equal(info.ModTime()) {
		return nil
	}
	d.producer.refs++
	_ = track.File.Close()
	return dsdSegment(d.producer, track, entry, d.format, 0, mode, pcmRange{start: *track.CueStartFrame, end: track.CueEndFrame})
}

func (p *dsdProducer) release() {
	p.refs--
	if p.refs == 0 {
		p.cancel()
		<-p.done
	}
}

func (p *dsdProducer) fill(ctx context.Context, file *os.File, reader io.Reader, frameBytes int) {
	defer close(p.done)
	defer file.Close()
	stop := context.AfterFunc(ctx, func() {
		p.ring.finish(ctx.Err())
		// 解除正在进行的文件读取；dsdpcm 自身不拥有句柄。
		_ = file.Close()
	})
	defer stop()
	chunk := make([]byte, (32*1024/frameBytes)*frameBytes)
	var total int64
	for {
		available, err := p.ring.free(ctx)
		if err != nil {
			p.ring.finish(err)
			return
		}
		n, readErr := reader.Read(chunk[:min(len(chunk), available)])
		if n > 0 {
			p.ring.put(chunk[:n])
			total += int64(n)
		}
		if readErr != nil {
			if readErr == io.EOF && (total == 0 || total%int64(frameBytes) != 0) {
				readErr = errors.New("render: 解码结果为空或帧不完整")
			}
			if readErr != io.EOF {
				readErr = &SourceError{Reason: "decode_failed", Err: readErr}
			}
			p.ring.finish(readErr)
			return
		}
	}
}
