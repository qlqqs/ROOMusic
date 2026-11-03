package ingest

import (
	"crypto/sha256"
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
