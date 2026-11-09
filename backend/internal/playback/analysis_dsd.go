package playback

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/playback/analysis"
	"github.com/qlqq/roomusic/backend/internal/playback/dsdio"
	"github.com/qlqq/roomusic/backend/internal/playback/dsdpcm"
)

// analyzeSource 只嗅探已经安全打开且核对版本的句柄，不按扩展名重开父文件。
func (w *AnalysisWorker) analyzeSource(ctx context.Context, file *os.File, candidate catalog.TrackAnalysisCandidate) (analysis.Result, error) {
	layout, err := analysisDSDLayout(file, candidate)
	if err != nil {
		return analysis.Result{}, err
	}
	if layout == nil {
		var cueIndex, cueEnd *int32
		if candidate.SourceKind == "cue_virtual" {
			cueIndex, cueEnd = candidate.CueIndexFrames, candidate.CueEndFrames
		}
		return w.analyzer.Analyze(ctx, file, cueIndex, cueEnd)
	}
	stream, input, err := newAnalysisDSDStream(ctx, file, *layout, candidate)
	if err != nil {
		return analysis.Result{}, err
	}
	defer stream.Close()
	return w.analyzer.AnalyzePCM(ctx, stream, input)
}

func analysisDSDLayout(file *os.File, candidate catalog.TrackAnalysisCandidate) (*dsdio.Layout, error) {
	switch candidate.SourceKind {
	case "dsf_id3", "dff_diin":
	case "cue_virtual":
		// CUE 的候选没有 Codec；非 DSD 父音频仍交原有 ffmpeg 路径。
		var signature [4]byte
		if _, err := file.ReadAt(signature[:], 0); err != nil && !errors.Is(err, io.EOF) {
			return nil, &analysis.Failure{Category: "source_unavailable", Err: err}
		}
		if string(signature[:]) != "DSD " && string(signature[:]) != "FRM8" {
			return nil, nil
		}
	default:
		return nil, nil
	}
	layout, err := dsdio.Probe(file, candidate.SourceSize)
	if err != nil {
		// 已识别的 DSD（包括 DST）不能退回 ffmpeg，避免另一套转换电平。
		return nil, &analysis.Failure{Category: "parse", Err: err}
	}
	if candidate.SourceKind == "dsf_id3" && layout.Container != dsdio.DSF || candidate.SourceKind == "dff_diin" && layout.Container != dsdio.DFF {
		return nil, &analysis.Failure{Category: "source_changed", Err: errors.New("DSD container differs from scanned source")}
	}
	return &layout, nil
}

// newAnalysisDSDStream 直接交付有界、按需转换的读取器，不创建额外生产协程。
// 关闭流会取消转换并关闭源句柄；exec 的 stdin 复制协程由分析器等待回收。
func newAnalysisDSDStream(ctx context.Context, file *os.File, layout dsdio.Layout, candidate catalog.TrackAnalysisCandidate) (*analysisPCMStream, analysis.PCMInput, error) {
	producerCtx, cancel := context.WithCancel(ctx)
	pcm, err := dsdpcm.New(producerCtx, file, layout)
	if err != nil {
		cancel()
		return nil, analysis.PCMInput{}, &analysis.Failure{Category: "parse", Err: err}
	}
	format := pcm.Format()
	var source io.Reader = pcm
	if candidate.SourceKind == "cue_virtual" {
		start, end := candidate.CueIndexFrames, candidate.CueEndFrames
		if start == nil || *start < 0 || end != nil && *end < *start {
			cancel()
			return nil, analysis.PCMInput{}, &analysis.Failure{Category: "source_changed", Err: errors.New("invalid CUE scan range")}
		}
		// 两端分别换算到 DXD 帧域，不用 DSD 比特率，也不对持续时间单独取整。
		first := int64(*start) * int64(format.SampleRate) / 75
		if err := pcm.SeekFrame(first); err != nil {
			cancel()
			return nil, analysis.PCMInput{}, &analysis.Failure{Category: "source_changed", Err: fmt.Errorf("seek DSD CUE: %w", err)}
		}
		if end != nil {
			last := int64(*end) * int64(format.SampleRate) / 75
			source = io.LimitReader(pcm, (last-first)*int64(format.Channels)*4)
		}
	}
	stream := &analysisPCMStream{Reader: source}
	stream.close = sync.OnceFunc(func() {
		cancel()
		_ = file.Close()
	})
	return stream, analysis.PCMInput{Format: "f32le", SampleRate: format.SampleRate, Channels: format.Channels}, nil
}

type analysisPCMStream struct {
	io.Reader
	close func()
}

func (s *analysisPCMStream) Close() error {
	s.close()
	return nil
}
