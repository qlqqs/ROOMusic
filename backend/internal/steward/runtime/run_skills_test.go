package runtime

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/steward/provider"
	"github.com/qlqq/roomusic/backend/internal/steward/skills"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

func TestRunSystemPromptSkillVisibility(t *testing.T) {
	for _, tc := range []struct {
		name, role string
		background bool
		wantSkill  bool
	}{
		{"admin_chat", "admin", false, true},
		{"member_chat", "user", false, false},
		{"admin_background", "admin", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry := tool.NewRegistry()
			if err := registry.Register(skills.Tool()); err != nil {
				t.Fatal(err)
			}
			store := &memoryRunStore{}
			p := &fakeProvider{next: func(_ context.Context, step int, _ provider.Request) ([]provider.Event, error) {
				if step == 1 && tc.wantSkill {
					return []provider.Event{
						modelCall("load_skill", `{"name":"album-curation"}`),
						modelDone(provider.FinishToolCalls),
					}, nil
				}
				return []provider.Event{modelDone(provider.FinishNormal)}, nil
			}}
			params := runParams(p)
			params.Caller.User.Role = tc.role
			params.Background = tc.background
			if tc.background {
				// 保留管理员可用的技能，让用例核对后台运行本身抑制提示的规则。
				params.Caller.Source = tool.SourceCuration
				store.detail.Conversation.Mode = "steward"
			}
			runner := NewRunner(store, registry, slog.New(slog.NewTextHandler(io.Discard, nil)))
			var end *RunEnd
			if err := runner.Run(t.Context(), params, func(event Event) {
				if event.Type == EventDone {
					end = event.End
				}
			}); err != nil {
				t.Fatal(err)
			}
			wantRequests := 1
			if tc.wantSkill {
				wantRequests = 2
			}
			if end == nil || end.Reason != EndCompleted || len(p.requests) != wantRequests {
				t.Fatalf("管家终态 = %+v，模型请求数 = %d，期望完成且有 %d 次请求", end, len(p.requests), wantRequests)
			}
			for _, request := range p.requests {
				for _, marker := range []string{"可用技能", "album-curation"} {
					if strings.Contains(request.System, marker) != tc.wantSkill {
						t.Errorf("系统提示对 %q 的可见性不符：%q", marker, request.System)
					}
				}
				if !tc.background {
					hasTool := slices.ContainsFunc(request.Tools, func(d provider.ToolDefinition) bool { return d.Name == "load_skill" })
					if hasTool != tc.wantSkill {
						t.Errorf("聊天模型的 load_skill 可见性 = %v，期望 %v", hasTool, tc.wantSkill)
					}
				}
			}
			if tc.wantSkill {
				var content string
				for _, message := range p.requests[1].Messages {
					for _, block := range message.Content {
						if result := block.ToolResult; result != nil && result.CallID == "call-load_skill" && !result.IsError {
							content = result.Content
						}
					}
				}
				if !strings.HasPrefix(content, "<roomusic-source kind=\"tool\" name=\"load_skill\">\n") ||
					!strings.HasSuffix(content, "\n</roomusic-source>") || !strings.Contains(content, "# 整理一张专辑") {
					t.Errorf("模型应收到标明不可信来源的技能内容，实际为 %q", content)
				}
			}
		})
	}
}
