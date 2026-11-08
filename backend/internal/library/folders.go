package library

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// FolderItem 是可浏览的目录；Selectable 表示是否位于允许根内，Registered 由传输层补齐。
type FolderItem struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Registered bool   `json:"registered"`
	Selectable bool   `json:"selectable"`
}

// FolderListing 表示当前目录及其直接子目录。
type FolderListing struct {
	Path       string       `json:"path"`
	Parent     *string      `json:"parent"`
	CanGoUp    bool         `json:"can_go_up"`
	Registered bool         `json:"registered"`
	Selectable bool         `json:"selectable"`
	Truncated  bool         `json:"truncated"`
	Items      []FolderItem `json:"items"`
}

// ListFolders 只读列举允许根内的直接子目录，或通往允许根的上层路径，不访问数据库。
// input 为空时从文件系统根目录起步；非空时必须是规范绝对路径。
func ListFolders(input string, allowedRoots []string, limit int) (FolderListing, error) {
	if input == "" {
		input = "/"
	}
	if !filepath.IsAbs(input) || input != filepath.Clean(input) {
		return FolderListing{}, errors.New("打不开这个文件夹")
	}

	roots := folderRoots(allowedRoots)
	if len(roots) == 0 {
		return FolderListing{}, errors.New("打不开这个文件夹")
	}
	listing := FolderListing{Path: input, Items: []FolderItem{}}
	if current, err := ValidatePath(input, allowedRoots); err == nil {
		listing.Path = current
		listing.Selectable = true
		if current != "/" {
			parent := filepath.Dir(current)
			listing.Parent = &parent
			listing.CanGoUp = true
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
			listing.Items = append(listing.Items, FolderItem{
				Name: name, Path: filepath.Join(current, name), Selectable: true,
			})
		}
		return listing, nil
	}

	prefix := strings.TrimSuffix(input, "/") + "/"
	rootSet := make(map[string]bool, len(roots))
	itemByPath := make(map[string]FolderItem, len(roots))
	for _, root := range roots {
		rootSet[root] = true
		if root == input || !strings.HasPrefix(root, prefix) {
			continue
		}
		name := strings.SplitN(strings.TrimPrefix(root, prefix), "/", 2)[0]
		path := prefix + name
		itemByPath[path] = FolderItem{Name: name, Path: path}
	}
	if len(itemByPath) == 0 {
		return FolderListing{}, errors.New("打不开这个文件夹")
	}
	parent := filepath.Dir(input)
	if input != "/" {
		listing.Parent = &parent
		listing.CanGoUp = true
	}
	paths := make([]string, 0, len(itemByPath))
	for path := range itemByPath {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		item := itemByPath[path]
		item.Selectable = rootSet[path]
		listing.Items = append(listing.Items, item)
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
