package ingest

import (
	"fmt"
	"strings"
)

// SourceIdentityForObservation 返回曲目来源的当前身份：物理文件为根 ID 加规范相对路径，
// CUE 虚拟轨为根 ID 加 sheet、父来源、轨号与 INDEX 01 帧。它只标识来源“此刻在哪里”；曲目 ID 能否
// 在改名或移动后延续，由 PlanStagedRoot 按内容指纹决定。
func SourceIdentityForObservation(rootID string, observation SourceObservation) string {
	if strings.EqualFold(observation.SourceKind, "cue_virtual") || strings.Contains(strings.ToLower(observation.SourceKind), "cue") {
		sheet := NormalizedPath(observation.CueSheetPath)
		parent := NormalizedPath(observation.CueParentRelativePath)
		if parent == "" {
			parent = NormalizedPath(observation.CueReferencedFile)
		}
		return fmt.Sprintf("%s:cue:v1:%s:%s:%d:%d", rootID, sheet, parent, observation.TrackNumber, observation.CueIndexFrames)
	}
	return rootID + ":physical:v1:" + NormalizedPath(observation.RelativePath)
}
