package stewardtools

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/library"
	"github.com/qlqq/roomusic/backend/internal/steward/skills"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

func browseTestRegistry(t *testing.T, service *library.Service) *tool.Registry {
	t.Helper()
	registry := tool.NewRegistry()
	for _, definition := range append(BrowseTools(service), skills.Tool()) {
		if err := registry.Register(definition); err != nil {
			t.Fatal(err)
		}
	}
	return registry
}

func requireBrowseToolError(t *testing.T, err error, code tool.ErrorCode) *tool.Error {
	t.Helper()
	var toolErr *tool.Error
	if !errors.As(err, &toolErr) || toolErr.Code != code {
		t.Fatalf("工具错误 = %v，期望 %s", err, code)
	}
	return toolErr
}

func TestStewardBrowseToolsAdminOnlyAndSkillVisibility(t *testing.T) {
	registry := browseTestRegistry(t, nil)
	admin := tool.Caller{User: identity.User{Role: "admin"}}
	member := tool.Caller{User: identity.User{Role: "user"}}
	const root = "10000000-0000-4000-8000-000000000001"
	cases := []struct {
		name, input string
		code        tool.ErrorCode
	}{
		{"list_library_directory", `{"root_id":"` + root + `"}`, tool.CodePermissionDenied},
		{"load_skill", `{"name":"album-curation"}`, tool.CodeTargetNotFound},
		{"read_library_file", `{"root_id":"` + root + `","path":"album.cue"}`, tool.CodePermissionDenied},
	}
	if listed := registry.List(member); len(listed) != 0 {
		t.Fatalf("成员可见的管理员工具 = %+v", listed)
	}
	definitions := registry.List(admin)
	if len(definitions) != len(cases) {
		t.Fatalf("管理员应能发现三个只读工具，实际为 %+v", definitions)
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := definitions[i]
			if d.Name != tc.name || d.Scope != tool.ScopeRead || !d.Untrusted {
				t.Errorf("工具应标为只读且输出不可信，实际为 %+v", d)
			}
			_, err := registry.Invoke(t.Context(), member, tc.name, json.RawMessage(tc.input))
			requireBrowseToolError(t, err, tc.code)
		})
	}

	_, denied := registry.Invoke(t.Context(), member, "load_skill", json.RawMessage(`{"name":"album-curation"}`))
	_, unknown := registry.Invoke(t.Context(), admin, "load_skill", json.RawMessage(`{"name":"absent-synthetic-skill"}`))
	deniedError := requireBrowseToolError(t, denied, tool.CodeTargetNotFound)
	unknownError := requireBrowseToolError(t, unknown, tool.CodeTargetNotFound)
	if deniedError.Message != unknownError.Message {
		t.Errorf("身份不足与未知技能的错误文案不同：%q 与 %q", deniedError.Message, unknownError.Message)
	}
	result, err := registry.Invoke(t.Context(), admin, "load_skill", json.RawMessage(`{"name":"album-curation"}`))
	if err != nil || !result.Untrusted || result.Truncated {
		t.Fatalf("管理员加载完整技能的结果应标为不可信：%+v / %v", result, err)
	}
	var loaded struct{ Name, Text string }
	if err := json.Unmarshal(result.Output, &loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.Name != "album-curation" || !strings.HasPrefix(loaded.Text, "---\nname: album-curation\n") ||
		!strings.Contains(loaded.Text, "# 整理一张专辑") || !strings.Contains(loaded.Text, "等用户说“继续”") {
		t.Errorf("技能返回值缺少名称或 Markdown 全文：%+v", loaded)
	}
}
