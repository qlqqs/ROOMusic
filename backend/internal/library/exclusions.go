package library

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
)

// ValidateExclusionPath 校验真实子目录，并返回适合保存的斜杠分隔相对路径。
func ValidateExclusionPath(input, rootPath string, allowedRoots []string) (string, error) {
	if !filepath.IsAbs(input) || input != filepath.Clean(input) {
		return "", errors.New("打不开这个文件夹")
	}
	validated, err := ValidatePath(input, allowedRoots)
	if err != nil {
		return "", err
	}
	relative, err := relativePathWithinRoot(rootPath, validated)
	if err != nil {
		return "", err
	}
	if err := ValidateExclusionRelativePath(relative); err != nil {
		return "", err
	}
	return relative, nil
}

// ValidateExclusionRelativePath 只校验存储路径的格式，删除排除项不要求目录仍然存在。
func ValidateExclusionRelativePath(input string) error {
	if input == "." || !fs.ValidPath(input) || strings.ContainsRune(input, '\x00') || len(input) > 4096 {
		return errors.New("排除路径无效")
	}
	return nil
}

func relativePathWithinRoot(rootPath, current string) (string, error) {
	relative, err := filepath.Rel(rootPath, current)
	if err != nil || !filepath.IsLocal(relative) {
		return "", errors.New("打不开这个文件夹")
	}
	return filepath.ToSlash(relative), nil
}
