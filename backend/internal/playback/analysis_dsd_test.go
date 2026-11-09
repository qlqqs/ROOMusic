package playback

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/playback/analysis"
	"github.com/qlqq/roomusic/backend/internal/playback/dsdpcm"
)

func TestAnalysisDSDStreamMatchesDefaultSourcePCM(t *testing.T) {
	for _, rate := range []int{2822400, 5644800} {
		t.Run(fmt.Sprintf("DSD%d", rate/44100), func(t *testing.T) {
			content := dsdSourceContent(rate)
			path := filepath.Join(t.TempDir(), "source.dsf")
			if err := os.WriteFile(path, content, 0o600); err != nil {
				t.Fatal(err)
			}
			startIndex, endIndex := int32(0), int32(1)
			for _, tc := range []struct {
				name, kind         string
				start, end         *int32
				firstFrame, frames int64
			}{
				{"普通", "dsf_id3", nil, nil, 0, 6000},
				{"CUE到共享边界", "cue_virtual", &startIndex, &endIndex, 0, 4704},
				{"CUE非零起点到EOF", "cue_virtual", &endIndex, nil, 4704, 1296},
			} {
				t.Run(tc.name, func(t *testing.T) {
					file, err := os.Open(path)
					if err != nil {
						t.Fatal(err)
					}
					defer file.Close()
					candidate := catalog.TrackAnalysisCandidate{
						SourceKind: tc.kind, SourceSize: int64(len(content)), CueIndexFrames: tc.start, CueEndFrames: tc.end,
					}
					layout, err := analysisDSDLayout(file, candidate)
					if err != nil || layout == nil {
						t.Fatalf("分析来源识别=%v，错误=%v", layout, err)
					}
					stream, input, err := newAnalysisDSDStream(context.Background(), file, *layout, candidate)
					if err != nil {
						t.Fatal(err)
					}
					defer stream.Close()
					if want := (analysis.PCMInput{Format: "f32le", SampleRate: 352800, Channels: 1}); input != want {
						t.Fatalf("分析 PCM 输入=%+v，期望 %+v", input, want)
					}
					// 独立默认转换器从父文件起点连续读取，分块丢弃前缀，不复制分析流的 seek 逻辑。
					pcm, err := dsdpcm.New(context.Background(), bytes.NewReader(content), *layout)
					if err != nil {
						t.Fatal(err)
					}
					const frameBytes = 4
					if _, err := io.CopyN(io.Discard, pcm, tc.firstFrame*frameBytes); err != nil {
						t.Fatal(err)
					}
					wantBytes := tc.frames * frameBytes
					expected := io.LimitReader(pcm, wantBytes)
					// 用非帧对齐的小缓冲分块比较完整 f32le 字节，不攒整轨 PCM。
					actualBlock, expectedBlock := make([]byte, 4093), make([]byte, 4093)
					var total int64
					for {
						n, actualErr := io.ReadFull(stream, actualBlock)
						wantN, expectedErr := io.ReadFull(expected, expectedBlock)
						if n != wantN || actualErr != expectedErr || !bytes.Equal(actualBlock[:n], expectedBlock[:wantN]) {
							t.Fatalf("偏移 %d：分析 %d 字节/%v，默认来源 %d 字节/%v，PCM 不一致", total, n, actualErr, wantN, expectedErr)
						}
						total += int64(n)
						if errors.Is(actualErr, io.EOF) || errors.Is(actualErr, io.ErrUnexpectedEOF) {
							break
						}
						if actualErr != nil {
							t.Fatal(actualErr)
						}
					}
					if total != wantBytes {
						t.Fatalf("分析长度=%d，期望 %d 字节", total, wantBytes)
					}
				})
			}
		})
	}
}
