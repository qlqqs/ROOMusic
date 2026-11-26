package library

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog/ingest"
	"github.com/qlqq/roomusic/backend/internal/library/parser"
	"github.com/qlqq/roomusic/backend/internal/platform/workpool"
)

// scanSlot 是遍历顺序中的一个条目；worker 只填充 audio 的解析结果，其余效果由 apply 执行。
type scanSlot struct {
	observedSize                         int64
	relative                             string
	audio                                []scanAudio
	cue                                  *parser.CueDocument
	changedCueParents                    []string
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
	externalLyrics                    string
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
	changed                    map[string]bool
	heldCueParents             map[string]bool
	pending                    map[string]bool
	stageFailed                func(error)
	seen                       int64
	walkErrorSeen, excludedHit bool
}

func newScanPipeline(ctx context.Context, service *Service, executor scanExecutor, scanID string, root registeredRoot, cache *parseCache, feedback *scanFeedback, stageFailed func(error)) *scanPipeline {
	pipeline := &scanPipeline{ctx: ctx, service: service, executor: executor, scanID: scanID, root: root,
		cache: cache, feedback: feedback, changed: make(map[string]bool), pending: make(map[string]bool), stageFailed: stageFailed}
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
	if pipeline.changed[relative] {
		return scanAudio{path: path, relative: relative, info: info, err: ingest.ErrSourceChanged}, nil
	}
	audio := scanAudio{path: path, relative: relative, info: info, prepared: true,
		diagnosticKind: "parse_failure", diagnosticMessage: "CUE 引用音频解析失败"}
	audio.cached = pipeline.cache.lookupObservation(pipeline.ctx, &audio)
	return audio, nil
}

func (pipeline *scanPipeline) prepareCueParent(path string) (scanAudio, error) {
	if path == "" || !isWithin(pipeline.root.Path, path) {
		return scanAudio{err: errors.New("unsafe CUE parent"), diagnosticKind: "cue_reference", diagnosticMessage: "CUE 引用无法安全读取"}, nil
	}
	if !isScannableAudio(strings.ToLower(filepath.Ext(path))) {
		return scanAudio{err: parser.ErrUnknownFormat, diagnosticKind: "unsupported_format", diagnosticMessage: "不支持的 CUE 引用音频格式"}, nil
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
	before, err := os.Stat(path)
	if err != nil {
		slot.diagnostics = []scanDiagnostic{{"permission_error", "无法读取 CUE 文件"}}
		slot.heldKind = ingest.HeldCueSheet
		return slot, nil
	}
	document, err := parser.ParseCueSheet(path, pipeline.root.Path)
	if checkErr := checkReadTime(path, before); checkErr != nil {
		slot.heldKind = ingest.HeldCueSheet
		if errors.Is(checkErr, ingest.ErrSourceChanged) {
			slot.changedCueParents = make([]string, 0, len(document.Files))
			for _, reference := range document.Files {
				if reference.Status == "present" {
					slot.changedCueParents = append(slot.changedCueParents, safeRelativePath(pipeline.root.Path, reference.ResolvedPath))
				}
			}
		} else {
			slot.diagnostics = []scanDiagnostic{{"permission_error", "无法读取 CUE 文件"}}
		}
		return slot, nil
	}
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
				audio.info, audio.err = os.Stat(audio.path)
				if audio.err == nil {
					audio.observation, audio.err = parser.ParseAudioFile(audio.path)
					if err := checkReadTime(audio.path, audio.info); err != nil {
						audio.err = err
					}
				}
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
	for i := range slot.audio {
		audio := &slot.audio[i]
		if pipeline.changed[audio.relative] {
			audio.err = ingest.ErrSourceChanged
			audio.cached = false
		}
		if errors.Is(audio.err, ingest.ErrSourceChanged) && !pipeline.changed[audio.relative] {
			_, count, err := ingest.DiscardChangedFile(pipeline.ctx, pipeline.executor, pipeline.scanID, pipeline.root.ID, audio.relative)
			if err != nil {
				return err
			}
			pipeline.changed[audio.relative] = true
			for range count {
				if pipeline.feedback != nil {
					pipeline.feedback.skip(pipeline.ctx)
				}
			}
		}
		pipeline.cache.applyAudio(pipeline.ctx, *audio)
		delete(pipeline.pending, audio.relative)
	}
	if slot.cue != nil {
		return pipeline.applyCue(slot)
	}
	if len(slot.audio) > 0 {
		audio := slot.audio[0]
		if errors.Is(audio.err, ingest.ErrSourceChanged) {
			return nil
		}
		if audio.err != nil {
			slot.diagnostics = []scanDiagnostic{{"parse_failure", "音频文件解析失败"}}
			if errors.Is(audio.err, parser.ErrUnknownFormat) || errors.Is(audio.err, parser.ErrUnsupportedEncoding) {
				slot.diagnostics = []scanDiagnostic{{"unsupported_format", "不支持的音频格式"}}
			}
			slot.heldKind = ingest.HeldFile
		} else {
			if pipeline.heldCueParents[audio.relative] {
				return nil
			}
			if audio.externalLyrics != "" {
				parser.ApplyExternalLyrics(&audio.observation, audio.externalLyrics)
			}
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
		if slot.changedCueParents != nil {
			if err := pipeline.holdCueParents(slot.relative, slot.changedCueParents); err != nil {
				return err
			}
		}
		pipeline.feedback.observeHeld(pipeline.root.ID, slot.relative, slot.observedSize, slot.heldKind)
		return pipeline.hold(slot.heldKind, slot.relative)
	}
	return slot.terminalError
}

// 变化的 sheet 可能尚未读全 FILE 声明，合并上次入库与本次安全解析出的父路径。
// 撤回先遍历到的物理观察，同时记住后续物理条目；其他稳定 sheet 仍可使用父文件的缓存。
func (pipeline *scanPipeline) holdCueParents(sheet string, parsed []string) error {
	rows, err := pipeline.executor.QueryContext(pipeline.ctx, `WITH parents(path) AS (
 SELECT unnest($4::text[]) UNION
 SELECT cue_parent_relative_path FROM tracks WHERE source_root_id=$2 AND source_status='present'
 AND cue_sheet_path=$3 AND cue_parent_relative_path IS NOT NULL
 ), held AS (
 INSERT INTO scan_held_sources(scan_run_id,root_id,kind,relative_path)
 SELECT $1::uuid,$2::uuid,'file',path FROM parents ON CONFLICT DO NOTHING
 ), discarded AS (
 DELETE FROM scan_observations WHERE scan_run_id=$1 AND root_id=$2 AND relative_path=fingerprint_path
 AND relative_path IN (SELECT path FROM parents)
 ) SELECT path FROM parents`, pipeline.scanID, pipeline.root.ID, sheet, parsed)
	if err != nil {
		return err
	}
	defer rows.Close()
	if pipeline.heldCueParents == nil {
		pipeline.heldCueParents = make(map[string]bool)
	}
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return err
		}
		pipeline.heldCueParents[path] = true
	}
	return rows.Err()
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
		pipeline.feedback.observeHeld(pipeline.root.ID, slot.relative, slot.observedSize, ingest.HeldCueSheet)
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
			if errors.Is(audio.err, ingest.ErrSourceChanged) {
				continue
			}
			kind, message := audio.diagnosticKind, audio.diagnosticMessage
			if errors.Is(audio.err, parser.ErrUnknownFormat) || errors.Is(audio.err, parser.ErrUnsupportedEncoding) {
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
		pipeline.feedback.observeHeld(pipeline.root.ID, slot.relative, slot.observedSize, ingest.HeldCueSheet)
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
	inserted, err := ingest.HoldSourceOnce(pipeline.ctx, pipeline.executor, pipeline.scanID, pipeline.root.ID, kind, relative)
	if err != nil {
		return errors.Join(err, pipeline.diagnose(relative, "staging_write_failure", "无法登记保住的来源"))
	}
	if inserted && pipeline.feedback != nil {
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

func checkReadTime(path string, before fs.FileInfo) error {
	after, err := os.Stat(path)
	if err != nil {
		return err
	}
	if after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return ingest.ErrSourceChanged
	}
	return nil
}
