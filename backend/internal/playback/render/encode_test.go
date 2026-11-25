package render

import (
	"context"
	"encoding/binary"
	"errors"
	"slices"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/dsp/chain"
	"github.com/qlqq/roomusic/backend/internal/playback/render/ffmpeg"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

func TestFLACEncoderFrameSizeArgument(t *testing.T) {
	starter := &recordingProcessStarter{}
	format := zones.StreamFormat{Codec: "flac", Delivery: "stream", BitDepth: 24}
	pcm := chain.Format{SampleRate: 352800, Channels: 2}
	_, err := newEncoder(context.Background(), starter, nil, format, pcm, nil, nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("未到达编码启动边界：%v", err)
	}
	index := slices.Index(starter.args, "-frame_size")
	if index < 0 || index+1 >= len(starter.args) || starter.args[index+1] != "4096" {
		t.Fatalf("FLAC 编码参数缺少 -frame_size 4096：%v", starter.args)
	}
}

func TestFFmpegFLACStreamBlockSize(t *testing.T) {
	const rate = 352800
	ff := pinnedFFmpeg(t)
	tool := openTestTool(t, ff, ffmpeg.Options{})
	fixture := makeFLAC(t, ff, 24, rate, 997, 0.2)
	r := newTestRenderer(t, tool, fixtureSource(map[string]audioFixture{"a": fixture}))
	s := openTestSession(t, r, renderSpec("flac"))
	data := readBody(t, s)

	// FLAC 标记后是 4 字节元数据块头和 34 字节 STREAMINFO。
	if len(data) < 42 || string(data[:4]) != "fLaC" || binary.BigEndian.Uint32(data[4:8])&0x7fffffff != 34 {
		t.Fatal("FLAC 输出缺少完整的 STREAMINFO")
	}
	streamInfo := data[8:42]
	if got := binary.BigEndian.Uint32(streamInfo[10:14]) >> 12; got != rate {
		t.Fatalf("FLAC STREAMINFO 采样率 = %d，期望 %d Hz", got, rate)
	}
	if got := binary.BigEndian.Uint16(streamInfo[2:4]); got != 4096 {
		t.Fatalf("FLAC STREAMINFO 最大块大小 = %d，期望 4096 帧", got)
	}
}
