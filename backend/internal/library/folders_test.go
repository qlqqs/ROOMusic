package library

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestListFoldersFiltersAndSorts(t *testing.T) {
	root := realTestPath(t, t.TempDir())
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
	parent := filepath.Dir(root)
	want := FolderListing{
		Path: root, Parent: &parent, CanGoUp: true, Selectable: true,
		Items: []FolderItem{
			{Name: "Disc 10", Path: filepath.Join(root, "Disc 10"), Selectable: true},
			{Name: "Disc 2", Path: filepath.Join(root, "Disc 2"), Selectable: true},
			{Name: "Jazz", Path: filepath.Join(root, "Jazz"), Selectable: true},
			{Name: "classical", Path: filepath.Join(root, "classical"), Selectable: true},
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
	want = FolderListing{Path: child, Parent: &root, CanGoUp: true, Selectable: true, Items: []FolderItem{}}
	if !reflect.DeepEqual(listing, want) {
		t.Fatalf("空子目录列表 = %#v，预期 %#v", listing, want)
	}
}

func TestListFoldersTruncatesAfterFiltering(t *testing.T) {
	root := realTestPath(t, t.TempDir())
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
			parent := filepath.Dir(root)
			want := FolderListing{Path: root, Parent: &parent, CanGoUp: true, Selectable: true, Truncated: test.truncated, Items: []FolderItem{}}
			for _, name := range test.names {
				want.Items = append(want.Items, FolderItem{Name: name, Path: filepath.Join(root, name), Selectable: true})
			}
			if !reflect.DeepEqual(listing, want) {
				t.Fatalf("截断目录列表 = %#v，预期 %#v", listing, want)
			}
		})
	}
}

func TestListFoldersRejectsInvalidPaths(t *testing.T) {
	base := realTestPath(t, t.TempDir())
	root := filepath.Join(base, "root")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(base, "other")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatal(err)
	}
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
		{name: "允许根旁边的目录", path: other},
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
	base := realTestPath(t, t.TempDir())
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
	if err := os.Mkdir(filepath.Join(base, "other"), 0o755); err != nil {
		t.Fatal(err)
	}

	listing, err := ListFolders("", []string{first}, 3)
	if err != nil {
		t.Fatal(err)
	}
	firstSegment := strings.Split(strings.TrimPrefix(base, string(filepath.Separator)), string(filepath.Separator))[0]
	wantRoot := "/" + firstSegment
	want := FolderListing{
		Path: "/", Selectable: false, Items: []FolderItem{{Name: firstSegment, Path: wantRoot, Selectable: false}},
	}
	if !reflect.DeepEqual(listing, want) {
		t.Fatalf("根目录列表 = %#v，预期 %#v", listing, want)
	}

	parent := filepath.Dir(base)
	listing, err = ListFolders(base, []string{link, second, first}, 3)
	if err != nil {
		t.Fatal(err)
	}
	want = FolderListing{
		Path: base, Parent: &parent, CanGoUp: true, Selectable: false,
		Items: []FolderItem{
			{Name: "a-root", Path: second, Selectable: true},
			{Name: "z-root", Path: first, Selectable: true},
		},
	}
	if !reflect.DeepEqual(listing, want) {
		t.Fatalf("允许根上层路径列表 = %#v，预期 %#v", listing, want)
	}
}

func realTestPath(t *testing.T, path string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return real
}
