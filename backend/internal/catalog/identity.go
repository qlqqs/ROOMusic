package catalog

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// SourceIdentityForObservation 返回曲目来源的版本化身份：物理文件为根 ID 加规范相对路径，
// CUE 虚拟轨为根 ID 加 sheet、父来源、轨号与 INDEX 01 帧。新行始终使用它。
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

// legacySourceIdentity 只用于从 0010 原地升级；新行始终使用 SourceIdentityForObservation。
func legacySourceIdentity(rootID string, observation SourceObservation) string {
	return rootID + ":" + NormalizedPath(observation.RelativePath)
}

func legacyCueSourceIdentity(rootID string, observation SourceObservation) string {
	if !isCueObservation(observation) {
		return ""
	}
	sheet := NormalizedPath(observation.CueSheetPath)
	parent := cueParentPath(observation)
	if sheet == "" || parent == "" || observation.TrackNumber < 1 {
		return ""
	}
	sheetDirectory := filepath.Dir(filepath.FromSlash(sheet))
	parentFromSheet, err := filepath.Rel(sheetDirectory, filepath.FromSlash(parent))
	if err != nil {
		return ""
	}
	parentFromSheet = NormalizedPath(filepath.ToSlash(parentFromSheet))
	if parentFromSheet == "" {
		return ""
	}
	legacyPath := sheet + "#track-" + strconv.Itoa(observation.TrackNumber) + "@" + parentFromSheet
	return rootID + ":" + legacyPath
}
