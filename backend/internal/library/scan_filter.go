package library

import (
	"path"
	"strings"
)

// scanFilter 保存根内过滤规则，由预计数与正式遍历共用；注册根自身不参与判断。
type scanFilter struct {
	exclusions map[string]struct{}
}

var builtInIgnoredDirectories = []string{
	"@eaDir", "#recycle", "@Recycle", "@Recently-Snapshot", "#snapshot",
	"$RECYCLE.BIN", "System Volume Information", "lost+found", "__MACOSX", "@appmeta", "@team",
}

func newScanFilter(exclusions []string) scanFilter {
	filter := scanFilter{exclusions: make(map[string]struct{}, len(exclusions))}
	for _, exclusion := range exclusions {
		filter.exclusions[path.Clean(exclusion)] = struct{}{}
	}
	return filter
}

func (filter scanFilter) skipDir(relativeDir, name string) bool {
	if filter.skipFile(name) {
		return true
	}
	for _, ignored := range builtInIgnoredDirectories {
		if strings.EqualFold(name, ignored) {
			return true
		}
	}
	_, excluded := filter.exclusions[relativeDir]
	return excluded
}

func (filter scanFilter) skipFile(name string) bool { return strings.HasPrefix(name, ".") }

func isSupportedAudioExtension(extension string) bool {
	switch extension {
	case ".flac", ".mp3", ".ogg", ".opus", ".wav", ".m4a":
		return true
	default:
		return false
	}
}

func isAudioCandidateExtension(extension string) bool {
	switch extension {
	case ".aac", ".aif", ".aiff", ".ape", ".dff", ".dsf", ".m4a", ".mka", ".wma":
		return true
	default:
		return false
	}
}
