package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/ingest"
	"github.com/qlqq/roomusic/backend/internal/library/parser"
)

const (
	parseCacheDirectoryLimit = 2000
	maxParseCacheBytes       = 1 << 20
)

type incrementalStats struct {
	filesParsed, parseCacheHits, parseCacheWrites              int64
	candidatesWritten, candidatesSkipped, artworkDeferredLoads int64
}

func (service *Service) logIncrementalSummary(scanID, rootID, mode string, stats *incrementalStats) {
	if service.logger != nil {
		service.logger.Info("扫描增量统计", "event", "library.scan.incremental_summary", "module", "library",
			"scan_run_id", scanID, "library_root_id", rootID, "mode", mode,
			"files_parsed", stats.filesParsed, "parse_cache_hits", stats.parseCacheHits,
			"parse_cache_writes", stats.parseCacheWrites, "candidates_written", stats.candidatesWritten,
			"candidates_skipped", stats.candidatesSkipped, "artwork_deferred_loads", stats.artworkDeferredLoads)
	}
}

type parseCacheEntry struct {
	size        int64
	modifiedAt  time.Time
	version     string
	observation []byte
}

// parseCache 只保留当前目录至多 2000 项；跨目录的 CUE 引用与超出预取的文件逐项查询。
// 缓存错误仅记安全类别，不改变扫描完整性，也不输出来源路径或标签。
type parseCache struct {
	service              *Service
	executor             scanExecutor
	scanID, rootID, mode string
	stats                *incrementalStats
	directory            string
	loaded, overflow     bool
	entries              map[string]parseCacheEntry
	seen                 []string
}

func (cache *parseCache) warn(operation string) {
	if cache.service.logger != nil {
		cache.service.logger.Warn("解析缓存不可用", "event", "library.scan.parse_cache_failed", "module", "library",
			"scan_run_id", cache.scanID, "library_root_id", cache.rootID, "operation", operation)
	}
}

func (cache *parseCache) enterDirectory(ctx context.Context, directory string) {
	directory = ingest.NormalizedPath(directory)
	if cache.mode == ScanModeFull || (cache.loaded && cache.directory == directory) {
		return
	}
	cache.flushSeen(ctx)
	cache.directory, cache.loaded, cache.overflow = directory, true, false
	cache.entries = make(map[string]parseCacheEntry)
	rows, err := cache.executor.QueryContext(ctx, `SELECT relative_path,source_size,source_modified_at,parser_version,observation
        FROM library_parse_cache WHERE root_id=$1::uuid AND directory=$2 ORDER BY relative_path LIMIT $3`, cache.rootID, directory, parseCacheDirectoryLimit+1)
	if err != nil {
		cache.warn("prefetch")
		return
	}
	defer rows.Close()
	for rows.Next() {
		if len(cache.entries) == parseCacheDirectoryLimit {
			cache.overflow = true
			break
		}
		var relative string
		var entry parseCacheEntry
		if err := rows.Scan(&relative, &entry.size, &entry.modifiedAt, &entry.version, &entry.observation); err != nil {
			cache.entries = make(map[string]parseCacheEntry)
			cache.warn("prefetch")
			return
		}
		cache.entries[relative] = entry
	}
	if err := rows.Err(); err != nil {
		cache.entries = make(map[string]parseCacheEntry)
		cache.warn("prefetch")
	}
}

func (cache *parseCache) lookup(ctx context.Context, relative string) (parseCacheEntry, bool) {
	if cache.loaded && ingest.DirectoryOf(relative) == cache.directory {
		if entry, ok := cache.entries[relative]; ok {
			return entry, true
		}
		if !cache.overflow {
			return parseCacheEntry{}, false
		}
	}
	var entry parseCacheEntry
	err := cache.executor.QueryRowContext(ctx, `SELECT source_size,source_modified_at,parser_version,observation
        FROM library_parse_cache WHERE root_id=$1::uuid AND relative_path=$2`, cache.rootID, relative).
		Scan(&entry.size, &entry.modifiedAt, &entry.version, &entry.observation)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		cache.warn("read")
	}
	return entry, err == nil
}

// lookupObservation 只查询缓存；命中计数与 seen 在槽位按序应用时推进。
func (cache *parseCache) lookupObservation(ctx context.Context, path, relative string, info fs.FileInfo) (parser.AudioObservation, bool) {
	relative = ingest.NormalizedPath(relative)
	version := parser.CacheVersion(filepath.Ext(path))
	modifiedAt := info.ModTime().UTC().Truncate(time.Microsecond)
	if cache.mode != ScanModeFull && version != "" {
		if entry, ok := cache.lookup(ctx, relative); ok && entry.size == info.Size() && entry.modifiedAt.Equal(modifiedAt) && entry.version == version {
			var observation parser.AudioObservation
			if len(entry.observation) <= maxParseCacheBytes && json.Unmarshal(entry.observation, &observation) == nil {
				observation.Artwork = nil
				return observation, true
			}
			cache.warn("decode")
		}
	}
	return parser.AudioObservation{}, false
}

func (cache *parseCache) applyAudio(ctx context.Context, audio scanAudio) {
	if !audio.prepared {
		return
	}
	if audio.cached {
		cache.stats.parseCacheHits++
		cache.seen = append(cache.seen, audio.relative)
		if len(cache.seen) == parseCacheDirectoryLimit {
			cache.flushSeen(ctx)
		}
		return
	}
	cache.stats.filesParsed++
	if version := parser.CacheVersion(filepath.Ext(audio.path)); audio.err == nil && version != "" {
		cache.write(ctx, audio.relative, audio.info.Size(), audio.info.ModTime().UTC().Truncate(time.Microsecond), version, audio.observation)
	}
}

func (cache *parseCache) write(ctx context.Context, relative string, size int64, modifiedAt time.Time, version string, observation parser.AudioObservation) {
	observation.Artwork = nil
	payload, err := json.Marshal(observation)
	if err != nil {
		cache.warn("encode")
		return
	}
	if len(payload) > maxParseCacheBytes {
		return
	}
	_, err = cache.executor.ExecContext(ctx, `INSERT INTO library_parse_cache
        (root_id,relative_path,directory,source_size,source_modified_at,parser_version,observation,seen_scan_id)
        VALUES($1::uuid,$2,$3,$4,$5,$6,$7::jsonb,$8::uuid)
        ON CONFLICT(root_id,relative_path) DO UPDATE SET directory=EXCLUDED.directory,source_size=EXCLUDED.source_size,
        source_modified_at=EXCLUDED.source_modified_at,parser_version=EXCLUDED.parser_version,
        observation=EXCLUDED.observation,seen_scan_id=EXCLUDED.seen_scan_id,updated_at=NOW()`,
		cache.rootID, relative, ingest.DirectoryOf(relative), size, modifiedAt, version, payload, cache.scanID)
	if err != nil {
		cache.warn("write")
		return
	}
	cache.stats.parseCacheWrites++
	if cache.mode != ScanModeFull && cache.loaded && ingest.DirectoryOf(relative) == cache.directory {
		if _, exists := cache.entries[relative]; exists || len(cache.entries) < parseCacheDirectoryLimit {
			cache.entries[relative] = parseCacheEntry{size: size, modifiedAt: modifiedAt, version: version, observation: payload}
		} else {
			cache.overflow = true
		}
	}
}

func (cache *parseCache) flushSeen(ctx context.Context) {
	if len(cache.seen) == 0 {
		return
	}
	if _, err := cache.executor.ExecContext(ctx, `UPDATE library_parse_cache SET seen_scan_id=$1::uuid
        WHERE root_id=$2::uuid AND relative_path=ANY($3::text[])`, cache.scanID, cache.rootID, cache.seen); err != nil {
		cache.warn("seen")
	}
	cache.seen = cache.seen[:0]
}

func (cache *parseCache) cleanup(ctx context.Context) {
	if _, err := cache.executor.ExecContext(ctx, `DELETE FROM library_parse_cache WHERE root_id=$1::uuid
        AND seen_scan_id IS DISTINCT FROM $2::uuid`, cache.rootID, cache.scanID); err != nil {
		cache.warn("cleanup")
	}
}
