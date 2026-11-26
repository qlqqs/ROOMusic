package runtime

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/qlqq/roomusic/backend/internal/steward/provider"
)

func TestCurationModelRulesStepRetries(t *testing.T) {
	for _, tc := range []struct {
		name       string
		wantEnd    EndReason
		sent       int
		laterStep  bool
		cancel     bool
		delay      time.Duration
		secondSend time.Duration
	}{
		{"五次含首发", EndProviderError, 5, false, false, 0, 0},
		{"复用首发起算的十秒预算", EndProviderError, 2, false, false, 6 * time.Second, 6 * time.Second},
		{"成功后工具只执行一次", EndCompleted, 3, true, false, 0, 300 * time.Millisecond},
		{"取消", EndCancelled, 1, false, true, 6 * time.Second, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				initial := time.Now()
				var sentAt []time.Duration
				p := &fakeProvider{next: func(_ context.Context, n int, r provider.Request) ([]provider.Event, error) {
					sentAt = append(sentAt, time.Since(initial))
					if tc.laterStep {
						switch n {
						case 1:
							return []provider.Event{{Type: provider.EventText, Text: "未完成的片段"}, modelCall("lookup", `{"query":"失败片段"}`), modelUsage(2, 1)}, nil
						case 2:
							if len(r.Messages) != 1 {
								t.Fatalf("第一次失败的片段进入了重试历史：%+v", r.Messages)
							}
							return []provider.Event{modelCall("lookup", `{"query":"完整回答"}`), modelUsage(3, 1), modelDone(provider.FinishToolCalls)}, nil
						case 3:
							return []provider.Event{{Type: provider.EventText, Text: "有效结果"}, modelUsage(4, 1), modelDone(provider.FinishNormal)}, nil
						}
					}
					if tc.cancel {
						time.AfterFunc(time.Second, cancel)
					}
					return nil, &provider.Error{Kind: provider.ErrorRateLimit, StatusCode: 429, RetryDelay: &tc.delay}
				}}
				params := runParams(p)
				params.Protocol = provider.ProtocolOpenAI
				params.Endpoint = "http://127.0.0.1"
				params.RequestBudget = 128000
				var usage []int64
				params.Usage = func(_ context.Context, kind UsageKind, u provider.Usage) {
					if kind != UsageMain {
						t.Fatalf("用量类别 %s", kind)
					}
					usage = append(usage, *u.InputTokens)
				}
				store := &memoryRunStore{}
				var end EndReason
				var toolStarts int
				err := NewRunner(store, runtimeRegistry(t), slog.New(slog.DiscardHandler)).Run(ctx, params, func(e Event) {
					switch e.Type {
					case EventDone:
						end = e.End.Reason
					case EventToolStarted:
						toolStarts++
					case EventText:
						if e.Text == "未完成的片段" {
							t.Error("部分回复出现在事件中")
						}
					}
				})
				if err != nil && !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				if end != tc.wantEnd || len(sentAt) != tc.sent {
					t.Fatalf("结束状态=%s 发起时刻=%v，期望 %s/%d", end, sentAt, tc.wantEnd, tc.sent)
				}
				if len(sentAt) > 1 && sentAt[1] != tc.secondSend {
					t.Errorf("第二次请求在 %v，期望 %v", sentAt[1], tc.secondSend)
				}
				if tc.laterStep && (toolStarts != 1 || !slices.Equal(usage, []int64{2, 3, 4})) {
					t.Errorf("完整步骤应只执行一个工具，三次实际已知用量应分别入账，实际工具=%d 用量=%v", toolStarts, usage)
				}
				assertReleased(t, store, p)
			})
		})
	}
}

func TestCurationModelRulesFailedUsageWhenRetryBlocked(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := &fakeProvider{next: func(_ context.Context, n int, _ provider.Request) ([]provider.Event, error) {
			if n != 1 {
				t.Fatalf("后续请求被挡下后仍然发出了第 %d 次", n)
			}
			return []provider.Event{modelUsage(9, 3)}, nil
		}}
		params := runParams(p)
		params.Protocol = provider.ProtocolOpenAI
		params.Endpoint = "http://127.0.0.1"
		params.RequestBudget = 128000
		checks := 0
		params.BeforeRequest = func(context.Context) error {
			checks++
			if checks == 2 {
				return ErrRunStopped
			}
			return nil
		}
		var recorded [][2]int64
		params.Usage = func(_ context.Context, kind UsageKind, u provider.Usage) {
			if kind != UsageMain {
				t.Fatalf("用量类别 %s", kind)
			}
			recorded = append(recorded, [2]int64{*u.InputTokens, *u.OutputTokens})
		}
		var end EndReason
		store := &memoryRunStore{}
		err := NewRunner(store, runtimeRegistry(t), slog.New(slog.DiscardHandler)).Run(t.Context(), params, func(e Event) {
			if e.Type == EventDone {
				end = e.End.Reason
			}
		})
		if err != nil || end != EndCurationStopped || checks != 2 || len(p.requests) != 1 {
			t.Fatalf("失败请求只发送一次且停止，实际状态 %s 检查 %d 请求 %d 错误 %v", end, checks, len(p.requests), err)
		}
		if !slices.Equal(recorded, [][2]int64{{9, 3}}) {
			t.Fatalf("已发出的中断请求应按实际用量入账一次，期望 [[9 3]]，实际 %v", recorded)
		}
		assertReleased(t, store, p)
	})
}
