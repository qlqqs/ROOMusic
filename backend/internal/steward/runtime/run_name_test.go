package runtime

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/steward/provider"
)

func TestRunSystemPromptDisplayName(t *testing.T) {
	for _, tc := range []struct {
		name, displayName, suffix string
	}{
		{"named", "小曲", "用户称呼你为「小曲」。"},
		{"filtered", "「小\r\n曲」\n「」", "用户称呼你为「小曲」。"},
		{"empty", "", ""},
		{"filtered_empty", "\r\n「」\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &memoryRunStore{}
			p := &fakeProvider{next: func(_ context.Context, step int, request provider.Request) ([]provider.Event, error) {
				if want := systemPrompt + tc.suffix + "曲库修改先生成方案，等待用户确认；本会话已获用户免确认的工具可以直接执行。"; request.System != want {
					t.Errorf("第 %d 步系统提示词=%q，期望=%q", step, request.System, want)
				}
				if step == 1 {
					return []provider.Event{modelCall("lookup", `{"query":"合成查询"}`), modelDone(provider.FinishToolCalls)}, nil
				}
				return []provider.Event{modelDone(provider.FinishNormal)}, nil
			}}
			params := runParams(p)
			params.DisplayName = tc.displayName
			runner := NewRunner(store, runtimeRegistry(t), slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err := runner.Run(context.Background(), params, func(Event) {}); err != nil {
				t.Fatal(err)
			}
			if len(p.requests) != 2 {
				t.Fatalf("未完成工具调用后的模型请求：%d", len(p.requests))
			}
			assertReleased(t, store, p)
		})
	}
}
