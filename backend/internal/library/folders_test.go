package library

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestListFoldersFiltersAndSorts(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"Jazz", "Disc 2", "Disc 10", "classical", ".hidden", string([]byte{0xff})} {
		if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "album.flac"), []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "Jazz"), filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "Jazz", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}

	listing, err := ListFolders(root, []string{root}, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := FolderListing{
		Path: &root,
		Items: []FolderItem{
			{Name: "Disc 10", Path: filepath.Join(root, "Disc 10")},
			{Name: "Disc 2", Path: filepath.Join(root, "Disc 2")},
			{Name: "Jazz", Path: filepath.Join(root, "Jazz")},
			{Name: "classical", Path: filepath.Join(root, "classical")},
		},
	}
	if !reflect.DeepEqual(listing, want) {
		t.Fatalf("目录列表 = %#v，预期 %#v", listing, want)
	}

	child := filepath.Join(root, "classical")
	listing, err = ListFolders(child, []string{root}, 10)
	if err != nil {
		t.Fatal(err)
	}
	want = FolderListing{Path: &child, Parent: &root, CanGoUp: true, Items: []FolderItem{}}
	if !reflect.DeepEqual(listing, want) {
		t.Fatalf("空子目录列表 = %#v，预期 %#v", listing, want)
	}
}

func TestListFoldersTruncatesAfterFiltering(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"c", "a", "b", ".hidden", string([]byte{0xff})} {
		if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "0-file"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "a"), filepath.Join(root, "0-link")); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name      string
		limit     int
		names     []string
		truncated bool
	}{
		{name: "超过上限", limit: 2, names: []string{"a", "b"}, truncated: true},
		{name: "恰好上限", limit: 3, names: []string{"a", "b", "c"}},
		{name: "少于上限", limit: 4, names: []string{"a", "b", "c"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			listing, err := ListFolders(root, []string{root}, test.limit)
			if err != nil {
				t.Fatal(err)
			}
			want := FolderListing{Path: &root, Truncated: test.truncated, Items: []FolderItem{}}
			for _, name := range test.names {
				want.Items = append(want.Items, FolderItem{Name: name, Path: filepath.Join(root, name)})
			}
			if !reflect.DeepEqual(listing, want) {
				t.Fatalf("截断目录列表 = %#v，预期 %#v", listing, want)
			}
		})
	}
}

func TestListFoldersRejectsInvalidPaths(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked")
	if err := os.Symlink(child, link); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(child, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		path string
	}{
		{name: "允许范围之外", path: "/etc"},
		{name: "相对路径", path: "child"},
		{name: "上级片段", path: root + "/../x"},
		{name: "当前片段", path: root + "/./child"},
		{name: "重复分隔符", path: root + "//child"},
		{name: "尾部分隔符", path: root + "/"},
		{name: "不存在", path: filepath.Join(root, "missing")},
		{name: "普通文件", path: file},
		{name: "目录符号链接", path: link},
		{name: "祖先符号链接", path: filepath.Join(link, "nested")},
	} {
		t.Run(test.name, func(t *testing.T) {
			if listing, err := ListFolders(test.path, []string{root}, 2); err == nil {
				t.Fatalf("非法路径应返回错误，实际列表 %#v", listing)
			}
		})
	}
}

func TestListFoldersStartsAtAllowedRoots(t *testing.T) {
	base := t.TempDir()
	first := filepath.Join(base, "z-root")
	second := filepath.Join(base, "a-root")
	for _, root := range []string{first, second} {
		if err := os.MkdirAll(filepath.Join(root, "child"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(base, "linked-root")
	if err := os.Symlink(first, link); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(base, "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		allowed []string
		want    FolderListing
	}{
		{
			name:    "单根直接进入",
			allowed: []string{first},
			want:    FolderListing{Path: &first, Items: []FolderItem{{Name: "child", Path: filepath.Join(first, "child")}}},
		},
		{
			name:    "多根保持配置顺序并解析链接去重",
			allowed: []string{link, filepath.Join(base, "missing"), file, second, first},
			want:    FolderListing{Items: []FolderItem{{Name: first, Path: first}, {Name: second, Path: second}}},
		},
		{
			name:    "符号链接单根直接进入真实路径",
			allowed: []string{link},
			want:    FolderListing{Path: &first, Items: []FolderItem{{Name: "child", Path: filepath.Join(first, "child")}}},
		},
		{
			name:    "去重和跳过无效目录后只剩单根",
			allowed: []string{link, first, file, filepath.Join(base, "missing")},
			want:    FolderListing{Path: &first, Items: []FolderItem{{Name: "child", Path: filepath.Join(first, "child")}}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			listing, err := ListFolders("", test.allowed, 3)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(listing, test.want) {
				t.Fatalf("起点列表 = %#v，预期 %#v", listing, test.want)
			}
		})
	}

	listing, err := ListFolders(first, []string{link, second}, 3)
	if err != nil {
		t.Fatal(err)
	}
	want := FolderListing{
		Path:    &first,
		CanGoUp: true,
		Items:   []FolderItem{{Name: "child", Path: filepath.Join(first, "child")}},
	}
	if !reflect.DeepEqual(listing, want) {
		t.Fatalf("多根中的根目录应返回列表层，实际 %#v，预期 %#v", listing, want)
	}
}
