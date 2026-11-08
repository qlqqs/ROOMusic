package parser

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/text/unicode/norm"
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

var cueAudioExtensions = []string{".flac", ".mp3", ".ogg", ".opus", ".wav", ".m4a"}

func cueNameEqual(a, b string) bool {
	return strings.EqualFold(norm.NFC.String(a), norm.NFC.String(b))
}

func matchingAudioFile(target string) (matched string, ambiguous bool, err error) {
	dir := filepath.Dir(target)
	base := filepath.Base(target)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	findMatch := func(matches func(string) bool) (string, bool, error) {
		var matched string
		for _, entry := range entries {
			if !matches(entry.Name()) {
				continue
			}
			candidate := filepath.Join(dir, entry.Name())
			info, statErr := os.Stat(candidate)
			if errors.Is(statErr, os.ErrNotExist) {
				continue
			}
			if statErr != nil {
				return "", false, statErr
			}
			if !info.Mode().IsRegular() {
				continue
			}
			if matched != "" {
				return "", true, nil
			}
			matched = candidate
		}
		return matched, false, nil
	}
	matched, ambiguous, err = findMatch(func(name string) bool {
		return cueNameEqual(name, base)
	})
	if matched != "" || ambiguous || err != nil {
		return matched, ambiguous, err
	}
	for _, extension := range cueAudioExtensions {
		matched, ambiguous, err = findMatch(func(name string) bool {
			return cueNameEqual(filepath.Ext(name), extension) && cueNameEqual(strings.TrimSuffix(name, filepath.Ext(name)), stem)
		})
		if matched != "" || ambiguous || err != nil {
			return matched, ambiguous, err
		}
	}
	return "", false, nil
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
	info, statErr := os.Stat(target)
	if errors.Is(statErr, os.ErrNotExist) {
		replacement, ambiguous, matchErr := matchingAudioFile(target)
		if matchErr != nil {
			return "unchecked", target, "unable to match FILE reference: " + normalized
		}
		if ambiguous {
			return "missing", target, "ambiguous FILE reference: " + normalized
		}
		if replacement != "" {
			target = replacement
			relativeToCue, relErr := filepath.Rel(cueDir, target)
			if relErr != nil {
				return "unchecked", "", "unable to resolve FILE reference"
			}
			normalized = filepath.ToSlash(filepath.Clean(relativeToCue))
			relative, err = filepath.Rel(boundary, target)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				return "unsafe", "", "unsafe FILE path traversal"
			}
			info, statErr = os.Stat(target)
		}
	}
	if statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return "missing", target, "missing FILE reference: " + normalized
		}
		return "unchecked", target, "unable to stat FILE reference: " + normalized
	}
	resolved = target
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
