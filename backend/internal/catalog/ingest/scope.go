package ingest

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

// OrganizationScope 返回观察所属的整理范围：暂存表按它分组，同一范围内的观察一起交给 OrganizeObservations。
func OrganizationScope(observation SourceObservation) string {
	location := locateObservation(observation)
	if location.isDiscDir {
		return "disc-parent:" + location.parent
	}
	if location.dir == "" {
		if album := albumEvidence(observation); album != "" {
			digest := sha256.Sum256([]byte(album))
			return fmt.Sprintf("root-album:%x", digest[:12])
		}
		return "root-source:" + NormalizedPath(observation.RelativePath)
	}
	return "directory:" + location.dir
}

// artworkScope 按目录、专辑与格式保留内嵌封面。可能独立成曲的文件按路径保留。
// JSON 字符串编码保留键的边界，同时避免把 NUL 写入 PostgreSQL jsonb。
func artworkScope(observation SourceObservation) string {
	location := locateObservation(observation)
	album := albumEvidence(observation)
	key := "dir:" + location.dir + "\x00" + album + "\x00" + observationFormat(observation)
	if album == "" && (location.dir == "" || hasTaggedTitleAndArtist(observation)) {
		key = "path:" + NormalizedPath(observation.RelativePath)
	}
	encoded, _ := json.Marshal(key)
	return string(encoded)
}

// IsDiscDirectory 供局部扫描和归组共用碟目录名规则。
func IsDiscDirectory(name string) bool {
	_, ok := parseDiscMarker(name)
	return ok
}

// 虚拟轨由 sheet 负责枚举，单轨 CUE 的物理轨仍由其音频目录负责。
const trackDirectorySQL = `regexp_replace(CASE WHEN t.source_kind='cue_virtual' THEN t.cue_sheet_path ELSE t.relative_path END, '(^|/)[^/]*$', '')`
