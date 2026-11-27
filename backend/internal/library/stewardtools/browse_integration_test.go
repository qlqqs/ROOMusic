package stewardtools

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/library"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

func TestPostgreSQLStewardBrowseToolResults(t *testing.T) {
	ctx := t.Context()
	db, err := postgres.Open(ctx, postgrestest.NewSchema(t, "steward_browse_"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rootID, root := uuid.New(), t.TempDir()
	if _, err := db.ExecContext(ctx, `INSERT INTO library_roots(id,path,status,revision) VALUES($1,$2,'active',1)`, rootID, root); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO library_root_exclusions(root_id,relative_path) VALUES($1,'private')`, rootID); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "private"), 0o755); err != nil {
		t.Fatal(err)
	}
	const textLimit = 64 << 10
	atLimit := strings.Repeat("\t", textLimit)
	for name, contents := range map[string]string{
		"limit.txt": atLimit, "oversize.txt": atLimit + "\n", "private/hidden.txt": "合成排除文件",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	service := library.NewService(ctx, db, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	registry := browseTestRegistry(t, service)
	admin := tool.Caller{User: identity.User{Role: "admin"}}
	invoke := func(name, path string) (tool.Result, error) {
		t.Helper()
		params, err := json.Marshal(map[string]any{"root_id": rootID, "path": path})
		if err != nil {
			t.Fatal(err)
		}
		return registry.Invoke(ctx, admin, name, params)
	}

	listed, err := invoke("list_library_directory", "")
	if err != nil || !listed.Untrusted || listed.Truncated {
		t.Fatalf("目录工具输出应标为不可信，实际为 %+v / %v", listed, err)
	}
	if bytes.Contains(listed.Output, []byte(root)) || bytes.Contains(listed.Output, []byte("private")) {
		t.Errorf("目录工具输出泄露根绝对路径或排除目录：%s", listed.Output)
	}
	read, err := invoke("read_library_file", "limit.txt")
	if err != nil || !read.Untrusted || read.Truncated {
		t.Fatalf("64 KiB 文本应完整返回并标为不可信，实际长度 %d，截断=%v，不可信=%v，错误=%v", len(read.Output), read.Truncated, read.Untrusted, err)
	}
	var file library.LibraryFileRead
	if err := json.Unmarshal(read.Output, &file); err != nil {
		t.Fatal(err)
	}
	if file.Path != "limit.txt" || file.SizeBytes != textLimit || file.Encoding != "utf-8" || file.Text == nil || *file.Text != atLimit {
		t.Fatal("64 KiB 文本的相对路径、大小、编码或内容不符")
	}
	_, err = invoke("read_library_file", "oversize.txt")
	requireBrowseToolError(t, err, "too_large")

	for _, tc := range []struct{ tool, excluded, missing string }{
		{"list_library_directory", "private", "absent"},
		{"read_library_file", "private/hidden.txt", "absent.txt"},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			_, excluded := invoke(tc.tool, tc.excluded)
			_, absent := invoke(tc.tool, tc.missing)
			excludedError := requireBrowseToolError(t, excluded, tool.CodeTargetNotFound)
			absentError := requireBrowseToolError(t, absent, tool.CodeTargetNotFound)
			if excludedError.Message != absentError.Message || strings.Contains(excludedError.Message, "排除") {
				t.Errorf("排除路径与不存在路径的错误文案应相同且隐藏排除规则：%q 与 %q", excludedError.Message, absentError.Message)
			}
		})
	}
}
