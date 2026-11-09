package render

import (
	"context"
	"encoding/binary"
	"io"
	"strconv"
	"sync/atomic"

	"github.com/qlqq/roomusic/backend/internal/dsp/chain"
	"github.com/qlqq/roomusic/backend/internal/playback/render/ffmpeg"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

type encoder struct {
	process      *ffmpeg.Process
	output       *io.PipeWriter
	format       zones.StreamFormat
	header       bool
	done         chan struct{}
	encodedBytes atomic.Int64
}

func newEncoder(ctx context.Context, reservation *ffmpeg.Reservation, tool *ffmpeg.Tool, f zones.StreamFormat, pcm chain.Format, output *io.PipeWriter, hls *hlsMedia, fail func(error)) (*encoder, error) {
	e := &encoder{format: f, output: output, done: make(chan struct{})}
	if f.Codec == "wav" {
		close(e.done)
		return e, nil
	}
	input := "f32le"
	if f.BitDepth != 0 {
		input = "s" + strconv.Itoa(f.BitDepth) + "le"
	}
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-f", input, "-ar", strconv.Itoa(pcm.SampleRate), "-ac", strconv.Itoa(pcm.Channels), "-i", "pipe:0", "-map_metadata", "-1", "-threads", "1"}
	if pcm.SampleRate != f.SampleRate {
		args = append(args, "-af", resampleFilter(f.SampleRate, tool.Soxr()))
	}
	switch f.Codec {
	case "flac":
		args = append(args, "-c:a", "flac", "-compression_level", "5", "-f", "flac")
	case "alac":
		sampleFormat := "s16p"
		if f.BitDepth == 24 {
			sampleFormat = "s32p"
		}
		args = append(args, "-c:a", "alac", "-sample_fmt", sampleFormat, "-bits_per_raw_sample", strconv.Itoa(f.BitDepth))
	case "opus":
		args = append(args, "-c:a", "libopus", "-b:a", "128k", "-application", "audio", "-ar", "48000", "-f", "ogg", "-page_duration", "100000")
		if f.Channels > 2 {
			args = append(args, "-mapping_family", "1")
		}
	case "aac":
		args = append(args, "-c:a", "aac", "-profile:a", "aac_low", "-b:a", "256k", "-ar", strconv.Itoa(f.SampleRate))
		if f.Delivery == "stream" {
			args = append(args, "-f", "adts")
		}
	}
	if f.Delivery == "hls" {
		args = append(args, "-f", "mp4", "-movflags", "+empty_moov+default_base_moof+skip_trailer", "-frag_duration", "4000000")
	}
	args = append(args, "-flush_packets", "1", "pipe:1")
	p, err := reservation.Start(ctx, ffmpeg.StartOptions{Args: args, Stdin: true})
	if err != nil {
		return nil, err
	}
	e.process = p
	go func() {
		defer close(e.done)
		defer p.Close()
		var copyErr error
		if hls != nil {
			copyErr = hls.split(p.Stdout())
		} else {
			_, copyErr = io.Copy(e, p.Stdout())
		}
		if copyErr != nil {
			_ = p.Close()
		}
		waitErr := p.Wait()
		if copyErr == nil {
			copyErr = waitErr
		}
		if copyErr != nil && ctx.Err() == nil {
			fail(copyErr)
		}
		if hls != nil && copyErr == nil && ctx.Err() == nil {
			hls.finish()
		}
		_ = output.CloseWithError(copyErr)
	}()
	return e, nil
}

// Write 用于所有非 HLS 长流输出：先计入编码字节（含 WAV 头部与 PCM），再传给客户端。
// 管道可能阻塞，但这里不持会话锁；客户端读取不会扣减累计值。
func (e *encoder) Write(data []byte) (int, error) {
	e.encodedBytes.Add(int64(len(data)))
	return e.output.Write(data)
}

func (e *encoder) write(data []byte) error {
	if e.process != nil {
		return writeAll(e.process, data)
	}
	if !e.header {
		if err := writeAll(e, wavHeader(e.format)); err != nil {
			return err
		}
		e.header = true
	}
	return writeAll(e, data)
}
func (e *encoder) finish() error {
	if e.process != nil {
		return e.process.CloseInput()
	}
	return e.output.Close()
}
func (e *encoder) close() {
	if e.process != nil {
		_ = e.process.Close()
	}
	<-e.done
}
func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := writer.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
func wavHeader(f zones.StreamFormat) []byte {
	b := make([]byte, 44)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], 0xffffffff)
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], uint16(f.Channels))
	binary.LittleEndian.PutUint32(b[24:], uint32(f.SampleRate))
	block := f.Channels * f.BitDepth / 8
	binary.LittleEndian.PutUint32(b[28:], uint32(f.SampleRate*block))
	binary.LittleEndian.PutUint16(b[32:], uint16(block))
	binary.LittleEndian.PutUint16(b[34:], uint16(f.BitDepth))
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], 0xffffffff)
	return b
}
