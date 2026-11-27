// Package skills 提供随服务发布的管家操作说明及其身份边界。
package skills

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

//go:embed *.md
var markdown embed.FS

const LoadToolName = "load_skill"

// Metadata 是可加入系统提示的技能名称、用途与最低身份。
type Metadata struct {
	Name        string
	Description string
	MinRole     tool.Role
}

type skill struct {
	Metadata
	text string
}

var bundled = loadBundled()

// List 返回调用者可用的技能，按名称排序。
func List(user identity.User) []Metadata {
	var result []Metadata
	for _, s := range bundled {
		if s.MinRole.Allows(user) {
			result = append(result, s.Metadata)
		}
	}
	return result
}

// Read 按精确名称返回内置 Markdown 全文；未知和越权均不可见。
func Read(user identity.User, name string) (string, bool) {
	for _, s := range bundled {
		if s.Name == name && s.MinRole.Allows(user) {
			return s.text, true
		}
	}
	return "", false
}

// Tool 返回读取内置技能的只读工具。
func Tool() tool.Definition {
	return tool.Definition{
		Name: LoadToolName, Description: "按名称读取管家内置技能的 Markdown 全文。",
		MinRole: tool.RoleAdmin, Scope: tool.ScopeRead, Untrusted: true, HideRoleDenied: true,
		Input:  json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"name":{"type":"string"}},"required":["name"]}`),
		Output: json.RawMessage(`{"type":"object"}`),
		Handler: func(_ context.Context, caller tool.Caller, raw json.RawMessage) (json.RawMessage, error) {
			var input struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(raw, &input); err != nil {
				return nil, err
			}
			content, ok := Read(caller.User, input.Name)
			if !ok {
				return nil, &tool.Error{Code: tool.CodeTargetNotFound, Message: "目标不存在"}
			}
			return json.Marshal(struct {
				Name string `json:"name"`
				Text string `json:"text"`
			}{Name: input.Name, Text: content})
		},
	}
}

func loadBundled() []skill {
	entries, err := fs.ReadDir(markdown, ".")
	if err != nil {
		panic(err)
	}
	result := make([]skill, 0, len(entries))
	for _, entry := range entries {
		data, err := markdown.ReadFile(entry.Name())
		if err != nil {
			panic(err)
		}
		text := string(data)
		header, body, found := strings.Cut(strings.TrimPrefix(text, "---\n"), "\n---\n")
		fields := make(map[string]string)
		for line := range strings.SplitSeq(header, "\n") {
			key, value, ok := strings.Cut(line, ":")
			if ok {
				fields[key] = strings.TrimSpace(value)
			}
		}
		m := Metadata{Name: fields["name"], Description: fields["description"], MinRole: tool.Role(fields["min_role"])}
		if !strings.HasPrefix(text, "---\n") || !found || strings.TrimSpace(body) == "" ||
			m.Name == "" || m.Name+".md" != entry.Name() || m.Description == "" ||
			(m.MinRole != tool.RoleMember && m.MinRole != tool.RoleAdmin) {
			panic(fmt.Sprintf("内置技能 %s 的头部无效", entry.Name()))
		}
		result = append(result, skill{Metadata: m, text: text})
	}
	return result
}
