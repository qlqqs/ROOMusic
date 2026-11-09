package library

import (
	"context"
	"errors"
	"io/fs"
	"os"

	"github.com/qlqq/roomusic/backend/internal/catalog/ingest"
	"github.com/qlqq/roomusic/backend/internal/library/parser"
	"github.com/qlqq/roomusic/backend/internal/platform/workpool"
)

// scanSlot 是遍历顺序中的一个条目；worker 只填充 audio 的解析结果，其余效果由 apply 执行。
type scanSlot struct {
	relative                             string
	audio                                []scanAudio
	cue                                  *parser.CueDocument
	diagnostics                          []scanDiagnostic
	heldKind                             string
	processed, seen, walkError, excluded bool
	terminalError                        error
}

type scanDiagnostic struct {
	kind, message string
}

// scanAudio 的缓存判定与安全检查由遍历者完成，解析结果不与其他任务共享。
type scanAudio struct {
	path, relative                    string
	info                              fs.FileInfo
	observation                       parser.AudioObservation
	prepared, cached                  bool
	err                               error
	diagnosticKind, diagnosticMessage string
}

func (audio scanAudio) deferred() bool {
	return audio.cached && audio.observation.ArtworkMIME != ""
}

type scanPipeline struct {
	ctx                        context.Context
	service                    *Service
	executor                   scanExecutor
	scanID                     string
	root                       registeredRoot
	cache                      *parseCache
	feedback                   *scanFeedback
	pool                       *workpool.Ordered[scanSlot]
	pending                    map[string]bool
	stageFailed                func(error)
	seen                       int64
	walkErrorSeen, excludedHit bool
}

func newScanPipeline(ctx context.Context, service *Service, executor scanExecutor, scanID string, root registeredRoot, cache *parseCache, feedback *scanFeedback, stageFailed func(error)) *scanPipeline {
	pipeline := &scanPipeline{ctx: ctx, service: service, executor: executor, scanID: scanID, root: root,
		cache: cache, feedback: feedback, pending: make(map[string]bool), stageFailed: stageFailed}
	workers := service.scanWorkerCount()
	pipeline.pool = workpool.NewOrdered(ctx, workers, 2*workers, pipeline.apply)
	return pipeline
}

func (pipeline *scanPipeline) prepareAudio(path, relative string, info fs.FileInfo) (scanAudio, error) {
	relative = ingest.NormalizedPath(relative)
	// CUE 与物理条目可能读同一父音频。先应用早先的写缓存，不能让命中与 worker 完成时机有关。
	if pipeline.pending[relative] {
		if err := pipeline.pool.Flush(); err != nil {
			return scanAudio{}, err
		}
	}
	observation, cached := pipeline.cache.lookupObservation(pipeline.ctx, path, relative, info)
	return scanAudio{path: path, relative: relative, info: info, observation: observation, prepared: true, cached: cached,
		diagnosticKind: "parse_failure", diagnosticMessage: "CUE 引用音频解析失败"}, nil
}

func (pipeline *scanPipeline) prepareCueParent(path string) (scanAudio, error) {
	if path == "" || !isWithin(pipeline.root.Path, path) {
		return scanAudio{err: errors.New("unsafe CUE parent"), diagnosticKind: "cue_reference", diagnosticMessage: "CUE 引用无法安全读取"}, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return scanAudio{err: err, diagnosticKind: "permission_error", diagnosticMessage: "无法读取 CUE 引用音频"}, nil
	}
	if !info.Mode().IsRegular() {
		return scanAudio{err: errors.New("non-regular CUE parent"), diagnosticKind: "cue_reference", diagnosticMessage: "CUE 引用不是普通文件"}, nil
	}
	return pipeline.prepareAudio(path, safeRelativePath(pipeline.root.Path, path), info)
}

func (pipeline *scanPipeline) prepareCue(slot scanSlot, path string) (scanSlot, error) {
	document, err := parser.ParseCueSheet(path, pipeline.root.Path)
	if err != nil {
		slot.diagnostics = []scanDiagnostic{{"unsupported_cue", "CUE 文件无法解析"}}
		slot.heldKind = ingest.HeldCueSheet
		return slot, nil
	}
	slot.cue = &document
	parents := make(map[string]bool)
	for _, reference := range document.Files {
		if reference.Status != "present" || parents[reference.ResolvedPath] {
			continue
		}
		parents[reference.ResolvedPath] = true
		audio, err := pipeline.prepareCueParent(reference.ResolvedPath)
		if err != nil {
			return scanSlot{}, err
		}
		// 安全检查失败也保留引用键，应用时才能按原 FILE 次序报告。
		audio.path = reference.ResolvedPath
		slot.audio = append(slot.audio, audio)
	}
	return slot, nil
}

func (pipeline *scanPipeline) submit(slot scanSlot) error {
	needsParse := false
	for _, audio := range slot.audio {
		if audio.prepared && !audio.cached {
			needsParse = true
			pipeline.pending[audio.relative] = true
		}
	}
	if !needsParse {
		return pipeline.pool.Ready(slot)
	}
	return pipeline.pool.Submit(func(ctx context.Context) scanSlot {
		for index := range slot.audio {
			if ctx.Err() != nil {
				break
			}
			audio := &slot.audio[index]
			if audio.prepared && !audio.cached {
				audio.observation, audio.err = parser.ParseAudioFile(audio.path)
			}
		}
		return slot
	})
}

func (pipeline *scanPipeline) apply(slot scanSlot) error {
	if slot.processed {
		defer pipeline.feedback.processed(pipeline.ctx)
	}
	if slot.seen {
		pipeline.seen++
	}
	pipeline.walkErrorSeen = pipeline.walkErrorSeen || slot.walkError
	pipeline.excludedHit = pipeline.excludedHit || slot.excluded
	for _, audio := range slot.audio {
		pipeline.cache.applyAudio(pipeline.ctx, audio)
		delete(pipeline.pending, audio.relative)
	}
	if slot.cue != nil {
		return pipeline.applyCue(slot)
	}
	if len(slot.audio) > 0 {
		audio := slot.audio[0]
		if audio.err != nil {
			slot.diagnostics = []scanDiagnostic{{"parse_failure", "音频文件解析失败"}}
			if errors.Is(audio.err, parser.ErrUnsupportedEncoding) {
				slot.diagnostics = []scanDiagnostic{{"unsupported_format", "不支持的音频格式"}}
			}
			slot.heldKind = ingest.HeldFile
		} else {
			observation := buildPhysicalSourceObservation(audio.observation, slot.relative)
			observation.ArtworkDeferred = audio.deferred()
			observation.SourceSize, observation.SourceModifiedAt = audio.info.Size(), audio.info.ModTime()
			if observation.Directory == "" && observation.InferredFields["album"] {
				observation.Album = ""
			}
			return pipeline.stage(slot.relative, observation, "无法暂存音频观察")
		}
	}
	for _, diagnostic := range slot.diagnostics {
		if err := pipeline.diagnose(slot.relative, diagnostic.kind, diagnostic.message); err != nil {
			return err
		}
	}
	if slot.heldKind != "" {
		return pipeline.hold(slot.heldKind, slot.relative)
	}
	return slot.terminalError
}

func (pipeline *scanPipeline) applyCue(slot scanSlot) error {
	parents := make(map[string]scanAudio, len(slot.audio))
	for _, audio := range slot.audio {
		parents[audio.path] = audio
	}
	document, err := parser.ResolveCueDurations(*slot.cue, func(path string) (float64, error) {
		audio := parents[path]
		return audio.observation.DurationSeconds, audio.err
	})
	if err != nil {
		if err := pipeline.diagnose(slot.relative, "unsupported_cue", "CUE 文件无法解析"); err != nil {
			return err
		}
		return pipeline.hold(ingest.HeldCueSheet, slot.relative)
	}
	for _, diagnostic := range document.Diagnostics {
		if err := pipeline.diagnose(slot.relative, "cue_reference", diagnostic); err != nil {
			return err
		}
	}
	parentFacts := make(map[string]scanAudio, len(document.Files))
	cueFailed := false
	for _, reference := range document.Files {
		if cueReferenceNeedsHold(reference.Status) {
			cueFailed = true
		}
		if reference.Status != "present" {
			continue
		}
		audio := parents[reference.ResolvedPath]
		if audio.err != nil {
			cueFailed = true
			kind, message := audio.diagnosticKind, audio.diagnosticMessage
			if errors.Is(audio.err, parser.ErrUnsupportedEncoding) {
				kind, message = "unsupported_format", "不支持的 CUE 引用音频格式"
			}
			if err := pipeline.diagnose(slot.relative, kind, message); err != nil {
				return err
			}
			continue
		}
		parentFacts[reference.Path] = audio
	}
	if cueFailed {
		return pipeline.hold(ingest.HeldCueSheet, slot.relative)
	}
	for _, cue := range document.Tracks {
		if err := pipeline.ctx.Err(); err != nil {
			return err
		}
		if !cueTrackCanBeStaged(cue) {
			continue
		}
		audio, ok := parentFacts[cue.ReferencedFile]
		if !ok {
			continue
		}
		observation := buildCueVirtualObservation(audio.observation, document, cue, slot.relative)
		observation.ArtworkDeferred = audio.deferred()
		observation.SourceSize, observation.SourceModifiedAt = audio.info.Size(), audio.info.ModTime()
		if err := pipeline.stage(slot.relative, observation, "无法暂存 CUE 观察"); err != nil {
			return err
		}
	}
	return nil
}

func (pipeline *scanPipeline) diagnose(relative, kind, message string) error {
	return pipeline.service.recordDiagnosticWith(pipeline.ctx, pipeline.executor, pipeline.scanID, relative, kind, message)
}

func (pipeline *scanPipeline) hold(kind, relative string) error {
	if kind == ingest.HeldFile {
		relative = ingest.NormalizedPath(relative)
	}
	if err := ingest.HoldSource(pipeline.ctx, pipeline.executor, pipeline.scanID, pipeline.root.ID, kind, relative); err != nil {
		return errors.Join(err, pipeline.diagnose(relative, "staging_write_failure", "无法登记保住的来源"))
	}
	if pipeline.feedback != nil {
		pipeline.feedback.skip(pipeline.ctx)
	}
	return nil
}

func (pipeline *scanPipeline) stage(relative string, observation ingest.SourceObservation, message string) error {
	if err := stageObservation(pipeline.ctx, pipeline.executor, pipeline.scanID, pipeline.root.ID, observation); err != nil {
		pipeline.stageFailed(err)
		return pipeline.diagnose(relative, "staging_write_failure", message)
	}
	return pipeline.ctx.Err()
}
