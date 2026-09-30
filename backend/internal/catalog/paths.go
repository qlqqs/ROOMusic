package catalog

import (
	pathpkg "path"
	"path/filepath"
	"strings"
)

// NormalizedPath 把来源相对路径规范为不含首尾空白、统一使用 / 且不带 ./ 前缀的形式；空路径返回空串。
func NormalizedPath(v string) string {
	v = pathpkg.Clean(strings.ReplaceAll(strings.TrimSpace(v), "\\", "/"))
	if v == "." {
		return ""
	}
	return strings.TrimPrefix(v, "./")
}

// DirectoryOf 返回来源相对路径所在的目录（统一使用 /）；位于根目录时返回空串。
func DirectoryOf(path string) string {
	d := filepath.ToSlash(filepath.Dir(path))
	if d == "." {
		return ""
	}
	return d
}
