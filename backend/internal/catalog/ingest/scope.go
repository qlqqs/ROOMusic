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

// artworkScope 按目录与专辑保留内嵌封面；根内无专辑的散曲按路径区分。
// JSON 字符串编码保留键的边界，同时避免把 NUL 写入 PostgreSQL jsonb。
func artworkScope(observation SourceObservation) string {
	location := locateObservation(observation)
	album := albumEvidence(observation)
	key := "dir:" + location.dir + "\x00" + album
	if location.dir == "" && album == "" {
		key = "path:" + NormalizedPath(observation.RelativePath)
	}
	encoded, _ := json.Marshal(key)
	return string(encoded)
}
