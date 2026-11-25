package render

import (
	"context"
	"errors"
	"io"
	"math"
	"time"

	"github.com/qlqq/roomusic/backend/internal/dsp/chain"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

type TrackRender = zones.TrackRender

type trackPipeline struct {
	chain   *chain.Chain
	encoder *encoder
	decoder *decoder
}

func (r *Renderer) trackFormat(input TrackRender) (zones.StreamFormat, error) {
	codec := input.Codec
	if codec == "mp3" && !r.options.FFmpeg.HasEncoder("libmp3lame") {
		codec = "opus"
	}
	caps := zones.Capabilities{MaxChannels: 32}
	for _, f := range deliveryFormats {
		if f.codec == codec && f.delivery == "stream" {
			caps.StreamFormats = []zones.StreamFormatCapability{{Codec: codec, MIMEType: f.mime, Lossless: f.lossless}}
			break
		}
	}
	f, err := r.Negotiate(caps, input.Settings.Quality)
	if err != nil {
		return f, err
	}
	if f.Quality == "lossless" {
		return f, nil
	}
	f.BitRate = input.BitRate
	if f.BitRate == 0 {
		f.BitRate = 256
		if codec == "opus" {
			f.BitRate = 192
		}
	}
	return f, nil
}

// prepareTrackPipeline 是播放区首曲和独立单曲共用的准备、解码、处理链与编码入口。
func (r *Renderer) prepareTrackPipeline(ctx context.Context, spec zones.RenderSpec, format zones.StreamFormat, plan renderPlan, track Track, writer *io.PipeWriter, hls *hlsMedia, fail func(error), immediate bool) (*trackPipeline, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	count := 0
	if track.DSDLayout == nil {
		count++
	}
	if format.Codec != "wav" && format.Delivery != "push" {
		count++
	}
	var starter processStarter
	if immediate && count > 0 {
		slots, err := r.options.FFmpeg.TryReserve(count)
		if err != nil {
			return nil, errors.Join(ErrBusy, err)
		}
		defer slots.Close()
		starter = slots
	}
	if err := r.loadConvolution(ctx, spec.UserID, &plan); err != nil {
		return nil, err
	}
	processing, err := plan.buildChain(spec.Settings, format.BitDepth)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !immediate && count > 0 {
		slots, err := r.options.FFmpeg.Reserve(ctx, count)
		if err != nil {
			return nil, errors.Join(ErrBusy, err)
		}
		defer slots.Close()
		starter = slots
	}
	enc, err := newEncoder(ctx, starter, r.options.FFmpeg, format, plan.outputPCM, writer, hls, fail)
	if err != nil {
		return nil, err
	}
	dec, err := openDecoder(ctx, starter, r.options.FFmpeg, track, spec.Start, plan.sourcePCM, spec.StartSeconds, spec.Settings.Leveling)
	return &trackPipeline{chain: processing, encoder: enc, decoder: dec}, err
}

type trackBody struct {
	reader *io.PipeReader
	cancel context.CancelFunc
	done   chan struct{}
}

func (body *trackBody) Read(p []byte) (int, error) { return body.reader.Read(p) }
func (body *trackBody) Close() error {
	body.cancel()
	_ = body.reader.Close()
	<-body.done
	return nil
}

// RenderTrack 尽快输出单曲，不读取播放头、不等待播放进度。
func (r *Renderer) RenderTrack(ctx context.Context, input zones.TrackRender) (io.ReadCloser, zones.StreamFormat, error) {
	r.mu.Lock()
	if r.closed || r.options.FFmpeg == nil || r.options.Sources == nil {
		r.mu.Unlock()
		return nil, zones.StreamFormat{}, ErrUnavailable
	}
	r.workers.Add(1)
	r.mu.Unlock()
	handedOff := false
	defer func() {
		if !handedOff {
			r.workers.Done()
		}
	}()
	if math.IsNaN(input.StartSeconds) || math.IsInf(input.StartSeconds, 0) || input.StartSeconds < 0 {
		return nil, zones.StreamFormat{}, &SourceError{Reason: "decode_failed"}
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(r.lifecycle, cancel)
	defer func() {
		if !handedOff {
			stop()
			cancel()
		}
	}()
	source, err := r.prepareSource(ctx, input.UserID, input.TrackID)
	if err != nil {
		return nil, zones.StreamFormat{}, err
	}
	defer func() {
		if !handedOff {
			_ = source.File.Close()
		}
	}()
	format, err := r.trackFormat(input)
	if err != nil {
		return nil, zones.StreamFormat{}, err
	}
	spec := zones.RenderSpec{UserID: input.UserID, Start: zones.Entry{TrackID: input.TrackID}, StartSeconds: input.StartSeconds, Settings: input.Settings, Format: format}
	format, plan, err := concreteFormat(spec, source)
	if err != nil {
		return nil, zones.StreamFormat{}, err
	}
	reader, writer := io.Pipe()
	pipeline, err := r.prepareTrackPipeline(ctx, spec, format, plan, source, writer, nil,
		func(renderErr error) { _ = writer.CloseWithError(renderErr) }, true)
	if err != nil {
		cancel()
		_ = reader.Close()
		if pipeline != nil {
			pipeline.encoder.close()
		}
		_ = writer.CloseWithError(err)
		return nil, zones.StreamFormat{}, err
	}
	unblock := context.AfterFunc(ctx, func() { _ = writer.CloseWithError(ctx.Err()) })
	body := &trackBody{reader: reader, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer r.workers.Done()
		defer close(body.done)
		defer cancel()
		defer unblock()
		defer stop()
		defer pipeline.decoder.close()
		defer pipeline.encoder.close()
		if err := renderTrackAudio(ctx, pipeline, input.Settings); err != nil {
			if ctx.Err() == nil {
				_ = writer.CloseWithError(err)
			}
			cancel()
			return
		}
	}()
	handedOff = true
	return body, pipeline.encoder.format, nil
}

func renderTrackAudio(ctx context.Context, pipeline *trackPipeline, settings zones.ZoneSettings) error {
	pcm := pcmOutput{chain: pipeline.chain, integer: pipeline.encoder.format.BitDepth != 0, clip: settings.Headroom.ClipIndicator}
	var chunk []byte
	var ended bool
	for {
		var input *chain.Buffer
		if !ended {
			buffer, _, err := pipeline.decoder.read(ctx, max(1, pipeline.decoder.format.SampleRate/5), false, time.Now)
			if err != nil && !errors.Is(err, io.EOF) {
				return err
			}
			ended = errors.Is(err, io.EOF)
			if buffer != nil && buffer.Frames > 0 {
				input = buffer
			} else if !ended {
				continue
			}
		}
		out, encoded, _ := pcm.block(chunk, input)
		if out == nil {
			break
		}
		chunk = encoded
		if len(chunk) > 0 {
			if err := pipeline.encoder.write(chunk); err != nil {
				return err
			}
		}
	}
	if err := pipeline.encoder.finish(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-pipeline.encoder.done:
		return nil
	}
}
