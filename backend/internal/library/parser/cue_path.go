package parser

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// CueFileReference 保留每个 FILE 声明。兼容入口 parseCue 会把引用平铺到
// CueTrack.ReferencedFile，需要逐文件诊断的扫描流程则直接使用 ParseCueDocument。
type CueFileReference struct {
	Path         string
	Type         string
	ResolvedPath string
	Status       string
	TrackNumbers []int
}

func normalizeCueReference(reference string) string {
	reference = strings.TrimSpace(strings.ReplaceAll(reference, "\\", "/"))
	return filepath.ToSlash(filepath.Clean(filepath.FromSlash(reference)))
}

func validateCueReference(cuePath, containmentRoot, reference string) (status, resolved, diagnostic string) {
	if reference == "" || strings.IndexByte(reference, 0) >= 0 {
		return "unsafe", "", "empty or NUL FILE reference"
	}
	if filepath.IsAbs(reference) || strings.HasPrefix(reference, `\`) || (len(reference) >= 2 && reference[1] == ':') {
		return "unsafe", "", "unsafe absolute FILE reference"
	}
	normalized := normalizeCueReference(reference)
	if normalized == "." {
		return "unsafe", "", "empty FILE reference"
	}
	cueDir, err := filepath.Abs(filepath.Dir(cuePath))
	if err != nil {
		return "unchecked", "", "unable to resolve CUE directory"
	}
	boundary, err := filepath.Abs(containmentRoot)
	if err != nil {
		return "unchecked", "", "unable to resolve containment root"
	}
	if relative, relErr := filepath.Rel(boundary, cueDir); relErr != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "unsafe", "", "CUE file is outside containment root"
	}
	target := filepath.Clean(filepath.Join(cueDir, filepath.FromSlash(normalized)))
	relative, err := filepath.Rel(boundary, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "unsafe", "", "unsafe FILE path traversal"
	}
	resolved = target
	info, statErr := os.Stat(target)
	if statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return "missing", resolved, "missing FILE reference: " + normalized
		}
		return "missing", resolved, "unable to stat FILE reference: " + normalized
	}
	if !info.Mode().IsRegular() {
		return "unsafe", resolved, "FILE reference is not a regular file: " + normalized
	}
	realBoundary, boundaryErr := filepath.EvalSymlinks(boundary)
	realTarget, targetErr := filepath.EvalSymlinks(target)
	if boundaryErr != nil || targetErr != nil {
		return "unchecked", resolved, "unable to resolve FILE symlink: " + normalized
	}
	if relative, relErr := filepath.Rel(realBoundary, realTarget); relErr != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "unsafe", resolved, "FILE symlink escapes containment root: " + normalized
	}
	return "present", resolved, ""
}
