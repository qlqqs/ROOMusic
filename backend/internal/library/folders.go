package library

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// FolderItem 是可浏览和选择的目录；Registered 由传输层补齐。
type FolderItem struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Registered bool   `json:"registered"`
}

// FolderListing 表示当前目录或允许目录列表；列表层的 Path 和 Parent 均为空。
type FolderListing struct {
	Path       *string      `json:"path"`
	Parent     *string      `json:"parent"`
	CanGoUp    bool         `json:"can_go_up"`
	Registered bool         `json:"registered"`
	Truncated  bool         `json:"truncated"`
	Items      []FolderItem `json:"items"`
}

// ListFolders 只读列举允许范围内的直接子目录，最多返回 limit 项，不访问数据库。
// input 为空时从允许目录起步；非空时必须是规范绝对路径。
func ListFolders(input string, allowedRoots []string, limit int) (FolderListing, error) {
	var current string
	if input != "" {
		if !filepath.IsAbs(input) || input != filepath.Clean(input) {
			return FolderListing{}, errors.New("打不开这个文件夹")
		}
		var err error
		current, err = ValidatePath(input, allowedRoots)
		if err != nil {
			return FolderListing{}, err
		}
	}

	roots := folderRoots(allowedRoots)
	if len(roots) == 0 {
		return FolderListing{}, errors.New("打不开这个文件夹")
	}
	listing := FolderListing{Items: []FolderItem{}}
	if input == "" {
		if len(roots) > 1 {
			for _, root := range roots {
				if len(listing.Items) >= limit {
					listing.Truncated = true
					break
				}
				listing.Items = append(listing.Items, FolderItem{Name: root, Path: root})
			}
			return listing, nil
		}
		current = roots[0]
	}

	listing.Path = &current
	parent := filepath.Dir(current)
	listing.Parent = &parent
	listing.CanGoUp = true
	for _, root := range roots {
		if current == root {
			listing.Parent = nil
			listing.CanGoUp = len(roots) > 1
			break
		}
	}

	entries, err := os.ReadDir(current)
	if err != nil {
		return FolderListing{}, err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.Type().IsDir() || strings.HasPrefix(name, ".") || !utf8.ValidString(name) {
			continue
		}
		if len(listing.Items) >= limit {
			listing.Truncated = true
			break
		}
		listing.Items = append(listing.Items, FolderItem{Name: name, Path: filepath.Join(current, name)})
	}
	return listing, nil
}

func folderRoots(allowedRoots []string) []string {
	roots := []string{}
	seen := make(map[string]bool, len(allowedRoots))
	for _, allowed := range allowedRoots {
		real, err := filepath.EvalSymlinks(allowed)
		if err != nil {
			continue
		}
		info, err := os.Stat(real)
		if err != nil || !info.IsDir() || seen[real] {
			continue
		}
		seen[real] = true
		roots = append(roots, real)
	}
	return roots
}
