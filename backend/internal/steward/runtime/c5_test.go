package runtime

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/steward/provider"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

func TestRunC5ModelToolsContainReadAndPersonalOnly(t *testing.T) {
	r := tool.NewRegistry()
	for _, scope := range []tool.Scope{tool.ScopeRead, tool.ScopePersonal, tool.ScopeShared} {
		err := r.Register(tool.Definition{Name: string(scope), Scope: scope, MinRole: tool.RoleMember, Input: json.RawMessage(`{"type":"object"}`), Output: json.RawMessage(`{}`), Handler: func(context.Context, tool.Caller, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{}`), nil
		}})
		if err != nil {
			t.Fatal(err)
		}
	}
	p := &fakeProvider{next: func(context.Context, int, provider.Request) ([]provider.Event, error) {
		return []provider.Event{modelDone(provider.FinishNormal)}, nil
	}}
	if err := NewRunner(&memoryRunStore{}, r, slog.New(slog.NewTextHandler(io.Discard, nil))).Run(context.Background(), runParams(p), func(Event) {}); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, d := range p.requests[0].Tools {
		names = append(names, d.Name)
	}
	if !reflect.DeepEqual(names, []string{"personal", "read"}) {
		t.Fatalf("model tools=%v", names)
	}
}

func TestC5ContextAcceptsBoundariesAndNormalizesIDs(t *testing.T) {
	id := "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA"
	for _, kind := range []string{"album", "artist", "playlist", "track", "other"} {
		t.Run(kind, func(t *testing.T) {
			raw, _ := json.Marshal(MessageContext{Page: &PageContext{Kind: kind, Path: strings.Repeat("路", 512), Title: strings.Repeat("题", 300), ID: id}, Playback: &PlaybackContext{ZoneID: id, TrackID: id, ZoneName: strings.Repeat("区", 300), Title: strings.Repeat("曲", 300), Artist: strings.Repeat("人", 300), Status: "playing"}})
			normalized, err := NormalizeContext(raw)
			if err != nil {
				t.Fatal(err)
			}
			var got MessageContext
			if err := json.Unmarshal(normalized, &got); err != nil {
				t.Fatal(err)
			}
			if got.Page.ID != strings.ToLower(id) || got.Playback.ZoneID != strings.ToLower(id) || got.Playback.TrackID != strings.ToLower(id) || len([]rune(got.Page.Path)) != 512 || len([]rune(got.Page.Title)) != 300 {
				t.Fatalf("边界值被改变：%s", normalized)
			}
		})
	}
	for _, status := range []string{"", "idle", "loading", "playing", "paused", "buffering", "error"} {
		t.Run("status/"+status, func(t *testing.T) {
			raw, _ := json.Marshal(MessageContext{Playback: &PlaybackContext{Status: status}})
			if _, err := NormalizeContext(raw); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, raw := range []json.RawMessage{nil, json.RawMessage(`null`)} {
		normalized, err := NormalizeContext(raw)
		if err != nil || normalized != nil {
			t.Fatalf("省略上下文：%s %v", normalized, err)
		}
	}
}

func TestRunC5InvalidInputKeepsHandlerMessage(t *testing.T) {
	for _, message := range []string{"跳转需要 position_seconds", "track_ids 与 release_id 必须二选一", "移动需要 to_index"} {
		t.Run(message, func(t *testing.T) {
			r := tool.NewRegistry()
			if err := r.Register(tool.Definition{Name: "synthetic_personal", Scope: tool.ScopePersonal, MinRole: tool.RoleMember,
				Input: json.RawMessage(`{"type":"object","additionalProperties":false}`), Output: json.RawMessage(`{}`),
				Handler: func(context.Context, tool.Caller, json.RawMessage) (json.RawMessage, error) {
					return nil, &tool.Error{Code: tool.CodeInvalidInput, Message: message}
				}}); err != nil {
				t.Fatal(err)
			}
			store := &memoryRunStore{}
			assertOutput := func(raw []byte) {
				t.Helper()
				var got struct{ Code, Message string }
				if err := json.Unmarshal(raw, &got); err != nil || got.Code != string(tool.CodeInvalidInput) || got.Message != message {
					t.Fatalf("处理函数说明丢失：%s %v", raw, err)
				}
			}
			p := &fakeProvider{next: func(_ context.Context, step int, request provider.Request) ([]provider.Event, error) {
				if step == 1 {
					return []provider.Event{modelCall("synthetic_personal", `{}`), modelDone(provider.FinishToolCalls)}, nil
				}
				result := request.Messages[len(request.Messages)-1].Content[0].ToolResult
				if result == nil || !result.IsError {
					t.Fatalf("没有错误结果：%+v", result)
				}
				assertOutput([]byte(result.Content))
				return []provider.Event{modelDone(provider.FinishNormal)}, nil
			}}
			var outputEvents int
			if err := NewRunner(store, r, slog.New(slog.NewTextHandler(io.Discard, nil))).Run(context.Background(), runParams(p), func(event Event) {
				if event.ToolFinished != nil {
					outputEvents++
					assertOutput(event.ToolFinished.Output)
				}
			}); err != nil {
				t.Fatal(err)
			}
			if len(p.requests) != 2 || outputEvents != 1 {
				t.Fatalf("模型请求=%d 工具事件=%d", len(p.requests), outputEvents)
			}
			call := store.detail.Messages[1].ToolCalls[0]
			if call.Status != "failed" || call.ErrorCode == nil || *call.ErrorCode != string(tool.CodeInvalidInput) {
				t.Fatalf("调用未记录失败：%+v", call)
			}
			assertOutput(call.Output)
		})
	}
}
