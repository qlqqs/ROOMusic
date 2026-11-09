package playback

import (
	"context"
	"testing"
)

type cueTestDecoders struct{}

func (cueTestDecoders) HasDecoder(name string) bool { return name == "flac" }

// 假 TrackSource 的渲染测试接收样本帧；这里单独覆盖其上游真实的 75 Hz 索引换算。
func TestPostgreSQLRenderCUEFramesShareBoundary(t *testing.T) {
	fixture := newPlaybackFixture(t)
	service := NewService(fixture.database, []string{fixture.allowedRoot}, WithRenderDecoders(cueTestDecoders{}))
	const parent = "专辑/整轨.flac"
	content := flacContent(4096)
	writeSource(t, fixture.musicRoot, parent, content, scanTime)
	size := int64(len(content))
	endIndex, endFrame := int32(752), int64(320853)
	for _, tc := range []struct {
		name      string
		start     int32
		end       *int32
		wantStart int64
		wantEnd   *int64
	}{
		{"前轨", 751, &endIndex, 320426, &endFrame},
		{"后轨到 EOF", 752, nil, 320853, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := fixture.insertTrack(t, trackSpec{
				relativePath: "专辑/整轨.cue#" + tc.name, sourceKind: "cue_virtual", codec: textPointer("flac"),
				size: &size, modifiedAt: &scanTime, bitDepth: 16, sampleRate: 32000, channels: 2,
			})
			fixture.exec(t, `UPDATE tracks SET cue_parent_relative_path=$2, cue_index_frames=$3, cue_end_frames=$4 WHERE id=$1::uuid`, id, parent, tc.start, tc.end)
			source, err := service.OpenForRender(context.Background(), id)
			if err != nil {
				t.Fatalf("OpenForRender: %v", err)
			}
			defer source.File.Close()
			if source.CueStartFrame == nil || *source.CueStartFrame != tc.wantStart {
				t.Fatalf("CUE 起点 = %v，期望样本 %d", source.CueStartFrame, tc.wantStart)
			}
			if tc.wantEnd == nil {
				if source.CueEndFrame != nil {
					t.Fatalf("到 EOF 的轨不应设置结束帧：%d", *source.CueEndFrame)
				}
			} else if source.CueEndFrame == nil || *source.CueEndFrame != *tc.wantEnd {
				t.Fatalf("CUE 终点 = %v，期望共享样本 %d（前轨长 427 帧）", source.CueEndFrame, *tc.wantEnd)
			}
		})
	}
}
