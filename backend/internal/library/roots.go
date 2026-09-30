package library

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ValidatePath 把请求的目录规范化并要求它是位于某个允许根内的真实目录：绝对路径、不含目录符号链接，
// 解析后仍在允许根之内。返回规范化路径；任何不满足的情况都返回错误，且不暴露允许根的位置。
func ValidatePath(input string, allowedRoots []string) (string, error) {
	clean, err := filepath.Abs(filepath.Clean(input))
	if err != nil {
		return "", err
	}
	// Registration must not silently follow a directory symlink. The lexical
	// path and its real path differ whenever any component is a symlink.
	real, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return "", err
	}
	if filepath.Clean(real) != clean {
		return "", fmt.Errorf("directory symlink is not allowed")
	}
	info, err := os.Stat(real)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("not directory")
	}
	for _, allowed := range allowedRoots {
		allowedReal, evalErr := filepath.EvalSymlinks(allowed)
		if evalErr == nil && (real == allowedReal || strings.HasPrefix(real, allowedReal+string(filepath.Separator))) {
			return real, nil
		}
	}
	return "", fmt.Errorf("outside allowlist")
}
