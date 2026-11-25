package render

import (
	"context"
	"errors"
	"io"
	"strconv"

	"github.com/qlqq/roomusic/backend/internal/dsp/chain"
	"github.com/qlqq/roomusic/backend/internal/playback/render/ffmpeg"
	"github.com/qlqq/roomusic/backend/internal/zones"
	"golang.org/x/sync/errgroup"
)

type memberStreamFilter struct {
	driftFrames
	tool   *ffmpeg.Tool
	format zones.StreamFormat
}

func NewMemberStreamFilter(tool *ffmpeg.Tool) func(zones.StreamFormat) zones.MemberStreamFilter {
	return func(format zones.StreamFormat) zones.MemberStreamFilter {
		return &memberStreamFilter{
			tool: tool, format: format,
			driftFrames: driftFrames{maxBlock: max(driftFrameInterval, driftLookaheadSeconds*format.SampleRate)},
		}
	}
}

func (f *memberStreamFilter) Filter(parent context.Context, source io.ReadCloser, output io.Writer) error {
	format := f.format
	if format.Codec != "flac" || (format.BitDepth != 16 && format.BitDepth != 24) || format.Channels <= 0 || format.SampleRate <= 0 {
		return errors.New("成员流的 FLAC 格式无效")
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	slots, err := f.tool.Reserve(ctx, 2)
	if err != nil {
		return err
	}
	defer slots.Close()
	raw := "s" + strconv.Itoa(format.BitDepth) + "le"
	// 输入由播放头节流，整组暂停时可以长期没有新字节。
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-xerror", "-f", "flac",
		"-probesize", "32", "-analyzeduration", "0", "-threads", "1", "-i", "pipe:0",
		"-map", "0:a:0", "-threads", "1", "-c:a", "pcm_" + raw, "-f", raw, "-flush_packets", "1", "pipe:1"}
	decoder, err := slots.Start(ctx, ffmpeg.StartOptions{Args: args, Stdin: true})
	if err != nil {
		return err
	}
	defer decoder.Close()
	pcm := chain.Format{SampleRate: format.SampleRate, Channels: format.Channels, Layout: format.Layout}
	encoder, err := slots.Start(ctx, ffmpeg.StartOptions{Args: encoderArgs(format, pcm), Stdin: true})
	if err != nil {
		return err
	}
	defer encoder.Close()

	var stages errgroup.Group
	run := func(stage func() error) {
		stages.Go(func() error {
			err := stage()
			if err != nil {
				cancel()
				// 唤醒等共享输入的读者和等成员取流的写者，再等待三个阶段退出。
				source.Close()
			}
			return err
		})
	}
	run(func() error {
		if _, err := io.Copy(decoder, source); err != nil {
			return err
		}
		return decoder.CloseInput()
	})
	run(func() error {
		if err := f.copyPCM(decoder, encoder, format.BitDepth/8, format.Channels); err != nil {
			return err
		}
		return encoder.CloseInput()
	})
	run(func() error {
		_, err := io.Copy(output, encoder)
		return err
	})
	return stages.Wait()
}
