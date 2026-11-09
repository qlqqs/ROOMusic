package playback

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/library"
	"github.com/qlqq/roomusic/backend/internal/playback/analysis"
)

const (
	analysisBatchSize = 20
	analysisIdleDelay = time.Minute
	analysisScanDelay = 30 * time.Second
	analysisDBDelay   = time.Minute
)

var (
	errAnalysisSourceUnavailable = errors.New("analysis source unavailable")
	errAnalysisSourceChanged     = errors.New("analysis source changed")
	errAnalysisDatabase          = errors.New("analysis catalog unavailable")
)

type analysisRunner interface {
	Version() int16
	Analyze(context.Context, *os.File, *int32, *int32) (analysis.Result, error)
}

// AnalysisWorker 顺序分析 catalog 提供的曲目快照；一个 worker 最多运行一个 ffmpeg。
type AnalysisWorker struct {
	store      *catalog.Store
	database   catalog.Queryer
	roots      []string
	analyzer   analysisRunner
	activeScan func(context.Context) (bool, error)
	logger     *slog.Logger
	mu         sync.RWMutex
	state      string
}

// NewAnalysisWorker 装配后台单线程分析工作者。
func NewAnalysisWorker(database catalog.Queryer, allowedRoots []string, analyzer analysisRunner,
	activeScan func(context.Context) (bool, error), logger *slog.Logger) *AnalysisWorker {
	return &AnalysisWorker{store: catalog.NewStore(database), database: database, roots: allowedRoots, analyzer: analyzer,
		activeScan: activeScan, logger: logger, state: "idle"}
}

// State 返回工作者当前状态；未装配 worker 时由 HTTP 层报告 unavailable。
func (w *AnalysisWorker) State() string         { w.mu.RLock(); defer w.mu.RUnlock(); return w.state }
func (w *AnalysisWorker) setState(state string) { w.mu.Lock(); w.state = state; w.mu.Unlock() }

// Run 按发行分批处理，扫描期间让路、两首间隔低优先级暂停。
func (w *AnalysisWorker) Run(ctx context.Context) {
	processed := 0
	for ctx.Err() == nil {
		running, err := w.activeScan(ctx)
		if err != nil {
			w.logger.ErrorContext(ctx, "读取扫描状态失败", "event", "playback.analysis.scan_state_failed", "error", err)
			w.setState("waiting_for_scan")
			if !waitAnalysis(ctx, analysisDBDelay) {
				return
			}
			continue
		}
		if running {
			w.setState("waiting_for_scan")
			if !waitAnalysis(ctx, analysisScanDelay) {
				return
			}
			continue
		}
		candidates, err := w.store.ListAnalysisCandidates(ctx, w.analyzer.Version(), analysisBatchSize)
		if err != nil {
			w.logger.ErrorContext(ctx, "读取待分析曲目失败", "event", "playback.analysis.catalog_failed", "error", err)
			w.setState("idle")
			if !waitAnalysis(ctx, analysisDBDelay) {
				return
			}
			continue
		}
		if len(candidates) == 0 {
			w.setState("idle")
			if !waitAnalysis(ctx, analysisIdleDelay) {
				return
			}
			continue
		}
		w.setState("running")
		for _, candidate := range candidates {
			if ctx.Err() != nil {
				return
			}
			running, err = w.activeScan(ctx)
			if err != nil {
				w.logger.ErrorContext(ctx, "读取扫描状态失败", "event", "playback.analysis.scan_state_failed", "error", err)
				w.setState("waiting_for_scan")
				if !waitAnalysis(ctx, analysisDBDelay) {
					return
				}
				break
			}
			if running {
				w.setState("waiting_for_scan")
				break
			}
			started := time.Now()
			result, analyzeErr := w.analyze(ctx, candidate)
			if ctx.Err() != nil {
				return
			}
			if analyzeErr != nil {
				w.logger.ErrorContext(ctx, "准备音频分析来源失败", "event", "playback.analysis.catalog_failed", "track_id", candidate.TrackID, "error", analyzeErr)
				w.setState("idle")
				if !waitAnalysis(ctx, analysisDBDelay) {
					return
				}
				break
			}
			if err := w.store.SaveTrackAnalysis(ctx, candidate, w.analyzer.Version(), result); err != nil {
				w.logger.ErrorContext(ctx, "保存音频分析失败", "event", "playback.analysis.save_failed", "track_id", candidate.TrackID, "error", err)
				w.setState("idle")
				if !waitAnalysis(ctx, analysisDBDelay) {
					return
				}
				break
			}
			if result.Status == "failed" {
				w.logger.WarnContext(ctx, "曲目音频分析失败", "event", "playback.analysis.failed", "track_id", candidate.TrackID, "category", result.FailureCategory)
			}
			processed++
			if processed%100 == 0 {
				progress, progressErr := w.store.AnalysisProgress(ctx, w.analyzer.Version())
				if progressErr == nil {
					w.logger.InfoContext(ctx, "音频分析进度", "event", "playback.analysis.progress", "analyzed", progress.Analyzed, "total", progress.Total)
				}
			}
			delay := time.Since(started)
			if delay < 2*time.Second {
				delay = 2 * time.Second
			}
			if !waitAnalysis(ctx, delay) {
				return
			}
		}
	}
}

func (w *AnalysisWorker) analyze(ctx context.Context, candidate catalog.TrackAnalysisCandidate) (catalog.TrackAnalysisResult, error) {
	file, err := w.openSource(ctx, candidate)
	if err != nil {
		if errors.Is(err, errAnalysisDatabase) {
			return catalog.TrackAnalysisResult{}, err
		}
		category := "source_unavailable"
		if errors.Is(err, errAnalysisSourceChanged) {
			category = "source_changed"
		}
		return catalog.TrackAnalysisResult{Status: "failed", FailureCategory: category}, nil
	}
	defer file.Close()
	var cueIndex, cueEnd *int32
	if candidate.SourceKind == "cue_virtual" {
		cueIndex = candidate.CueIndexFrames
		cueEnd = candidate.CueEndFrames
	}
	result, err := w.analyzer.Analyze(ctx, file, cueIndex, cueEnd)
	if err != nil {
		if ctx.Err() != nil {
			return catalog.TrackAnalysisResult{}, ctx.Err()
		}
		var failure *analysis.Failure
		category := "ffmpeg_exit"
		if errors.As(err, &failure) {
			category = failure.Category
		}
		return catalog.TrackAnalysisResult{Status: "failed", FailureCategory: category}, nil
	}
	return catalog.TrackAnalysisResult{Status: "done", IntegratedLUFS: result.IntegratedLUFS, TruePeakDBTP: result.TruePeakDBTP,
		LoudnessRangeLU: result.LoudnessRangeLU, SamplePeakDBFS: result.SamplePeakDBFS,
		DurationMS: result.DurationMS, LeadingSilenceMS: result.LeadingSilenceMS, TrailingSilenceMS: result.TrailingSilenceMS, Waveform: result.Waveform}, nil
}

func (w *AnalysisWorker) openSource(ctx context.Context, catalogCandidate catalog.TrackAnalysisCandidate) (*os.File, error) {
	location, err := library.LoadRootLocation(ctx, w.database, catalogCandidate.RootID)
	if errors.Is(err, library.ErrRootNotFound) || (err == nil && !location.Active) {
		return nil, fmt.Errorf("%w: root unavailable", errAnalysisSourceUnavailable)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: load root: %w", errAnalysisDatabase, err)
	}
	rootPath, err := library.ValidatePath(location.Path, w.roots)
	if err != nil || rootPath != location.Path {
		return nil, fmt.Errorf("%w: root is outside allowed roots", errAnalysisSourceUnavailable)
	}
	relative, err := sourceAudioPath(catalogCandidate.SourceKind, catalogCandidate.RelativePath, catalogCandidate.CueParentRelativePath)
	var opened *openedSource
	if err == nil {
		opened, err = openScannedSource(rootPath, relative, expectedSource{
			Size: &catalogCandidate.SourceSize, ModifiedAt: &catalogCandidate.SourceModifiedAt,
		}, catalogCandidate.TrackID)
	}
	if errors.Is(err, ErrSourceChanged) {
		return nil, fmt.Errorf("%w: %w", errAnalysisSourceChanged, err)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errAnalysisSourceUnavailable, err)
	}
	return opened.File, nil
}

func waitAnalysis(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// AnalysisProgress 是 progress REST 的 wire DTO。
type AnalysisProgress struct {
	Total    int64  `json:"total"`
	Analyzed int64  `json:"analyzed"`
	Failed   int64  `json:"failed"`
	State    string `json:"state"`
}

type AnalysisProgressProvider interface {
	LoadProgress(context.Context) (AnalysisProgress, error)
}

type analysisProgressProvider struct {
	store   *catalog.Store
	version int16
	worker  *AnalysisWorker
}

// NewAnalysisProgressProvider 提供进度计数；分析未运行时仍返回真实计数与 unavailable 状态。
func NewAnalysisProgressProvider(database catalog.Queryer, version int16, worker *AnalysisWorker) AnalysisProgressProvider {
	return &analysisProgressProvider{store: catalog.NewStore(database), version: version, worker: worker}
}

func (p *analysisProgressProvider) LoadProgress(ctx context.Context) (AnalysisProgress, error) {
	counts, err := p.store.AnalysisProgress(ctx, p.version)
	if err != nil {
		return AnalysisProgress{}, err
	}
	state := "unavailable"
	if p.worker != nil {
		state = p.worker.State()
	}
	return AnalysisProgress{Total: counts.Total, Analyzed: counts.Analyzed, Failed: counts.Failed, State: state}, nil
}
