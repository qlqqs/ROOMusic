package render

import (
	"context"
	"encoding/binary"
	"io"
	"strconv"

	"github.com/qlqq/roomusic/backend/internal/dsp/chain"
	"github.com/qlqq/roomusic/backend/internal/playback/render/ffmpeg"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

type encoder struct {
	process *ffmpeg.Process
	output  *io.PipeWriter
	format  zones.StreamFormat
	header  bool
	done    chan struct{}
}

func newEncoder(ctx context.Context, reservation *ffmpeg.Reservation, tool *ffmpeg.Tool, f zones.StreamFormat, pcm chain.Format, output *io.PipeWriter, fail func(error)) (*encoder, error) {
	e := &encoder{format: f, output: output, done: make(chan struct{})}
	if f.Codec == "wav" {
		close(e.done)
		return e, nil
	}
	input := "f32le"
	if f.Codec == "flac" {
		input = "s" + strconv.Itoa(f.BitDepth) + "le"
	}
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-f", input, "-ar", strconv.Itoa(pcm.SampleRate), "-ac", strconv.Itoa(pcm.Channels), "-i", "pipe:0", "-map_metadata", "-1", "-threads", "1"}
	if pcm.SampleRate != f.SampleRate {
		args = append(args, "-af", resampleFilter(f.SampleRate, tool.Soxr()))
	}
	switch f.Codec {
	case "flac":
		args = append(args, "-c:a", "flac", "-compression_level", "5", "-f", "flac")
	case "opus":
		args = append(args, "-c:a", "libopus", "-b:a", "128k", "-application", "audio", "-ar", "48000", "-f", "ogg", "-page_duration", "100000")
		if f.Channels > 2 {
			args = append(args, "-mapping_family", "1")
		}
	case "aac":
		args = append(args, "-c:a", "aac", "-profile:a", "aac_low", "-b:a", "256k", "-ar", strconv.Itoa(f.SampleRate), "-f", "adts")
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
		_, copyErr := io.Copy(output, p.Stdout())
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
		_ = output.CloseWithError(copyErr)
	}()
	return e, nil
}
func (e *encoder) write(data []byte) error {
	if e.process != nil {
		return writeAll(e.process, data)
	}
	if !e.header {
		if err := writeAll(e.output, wavHeader(e.format)); err != nil {
			return err
		}
		e.header = true
	}
	return writeAll(e.output, data)
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
