package provider_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/steward/provider"
	"github.com/qlqq/roomusic/backend/internal/upstreammock"
)

const mockModel = "fixture-model"
const mockTool = "search_music_metadata"

var mockProtocols = []struct {
	protocol provider.Protocol
	endpoint string
	create   func(*http.Client, string, string) (provider.Provider, error)
}{
	{provider.ProtocolResponses, "/openai/v1", provider.NewResponses},
	{provider.ProtocolOpenAI, "/openai/chat/v1", provider.NewOpenAI},
	{provider.ProtocolMessages, "/anthropic", provider.NewMessages},
}

func TestUpstreamMockProbeAndScriptedToolRoundTrip(t *testing.T) {
	handler, err := upstreammock.New(upstreammock.Options{
		ScriptFile: filepath.Join("..", "..", "upstreammock", "testdata", "conversation-test.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	for _, tc := range mockProtocols {
		t.Run(string(tc.protocol), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			endpoint := server.URL + tc.endpoint
			probe, err := provider.Probe(ctx, server.Client(), endpoint, "synthetic-model-key", mockModel)
			if err != nil || probe.Protocol != tc.protocol || probe.ToolSupport != provider.ToolSupportSupported ||
				probe.ResponsesAvailable != (tc.protocol == provider.ProtocolResponses) {
				t.Fatalf("端点检测 = %+v，错误 %v", probe, err)
			}
			if len(probe.Models) != 2 || probe.Models[0].ID != mockModel || probe.Models[1].ID != "fixture-reviewer" {
				t.Fatalf("脚本模型列表 = %+v", probe.Models)
			}
			if tc.protocol != provider.ProtocolOpenAI && probe.NativeSearch != provider.NativeSearchUnsupported {
				t.Errorf("原生搜索能力 = %s，期望 unsupported", probe.NativeSearch)
			}

			model, err := tc.create(server.Client(), endpoint, "synthetic-model-key")
			if err != nil {
				t.Fatal(err)
			}
			request := provider.Request{
				Model: mockModel, MaxOutputTokens: 128,
				Messages: []provider.Message{{Role: provider.RoleUser, Content: []provider.ContentBlock{{Type: provider.BlockText, Text: "请试查发行"}}}},
				Tools:    []provider.ToolDefinition{{Name: mockTool, InputSchema: json.RawMessage(`{"type":"object","properties":{"entity":{"type":"string"},"query":{"type":"string"},"limit":{"type":"integer"}}}`)}},
			}
			first := readMockTurn(t, ctx, model, request)
			if first.finish == nil || first.finish.Reason != provider.FinishToolCalls || len(first.calls) != 1 || first.text != "" {
				t.Fatalf("工具轮结果 = %+v", first)
			}
			call := first.calls[0]
			var args struct {
				Entity, Query string
				Limit         int
			}
			if call.ID == "" || call.Name != mockTool || json.Unmarshal(call.Arguments, &args) != nil ||
				args.Entity != "release" || args.Query != "潮线以北" || args.Limit != 1 {
				t.Fatalf("工具调用 = %+v，参数 = %+v", call, args)
			}

			request.Messages = append(request.Messages,
				provider.Message{Role: provider.RoleAssistant, ProviderBlocks: first.blocks, Content: []provider.ContentBlock{{Type: provider.BlockToolCall, ToolCall: call}}},
				provider.Message{Role: provider.RoleTool, Content: []provider.ContentBlock{{Type: provider.BlockToolResult, ToolResult: &provider.ToolResult{
					CallID: call.ID, Content: `{"title":"潮线以北","artist":"纸屿"}`,
				}}}},
			)
			second := readMockTurn(t, ctx, model, request)
			if second.finish == nil || second.finish.Reason != provider.FinishNormal ||
				len(second.calls) != 0 || second.text != "已找到合成发行《潮线以北》。" {
				t.Fatalf("工具结果回传后的模型回复 = %+v", second)
			}
		})
	}
}

type mockTurn struct {
	text   string
	calls  []*provider.ToolCall
	finish *provider.Finish
	blocks []provider.OpaqueBlock
}

func readMockTurn(t *testing.T, ctx context.Context, model provider.Provider, request provider.Request) mockTurn {
	t.Helper()
	stream, err := model.Stream(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	var result mockTurn
	var usage *provider.Usage
	for {
		event, err := stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		switch event.Type {
		case provider.EventText:
			result.text += event.Text
		case provider.EventToolCall:
			result.calls = append(result.calls, event.ToolCall)
		case provider.EventDone:
			if result.finish != nil {
				t.Fatal("同一轮重复发出结束事件")
			}
			result.finish, result.blocks = event.Finish, event.ProviderBlocks
		case provider.EventUsage:
			usage = event.Usage
		}
	}
	if usage == nil || usage.InputTokens == nil || *usage.InputTokens <= 0 || usage.OutputTokens == nil || *usage.OutputTokens <= 0 {
		t.Errorf("流缺少最终用量: %+v", usage)
	}
	return result
}

func TestUpstreamMockDropNeverCompletesTurn(t *testing.T) {
	handler, err := upstreammock.New(upstreammock.Options{Scenarios: map[string]string{"openai": "drop", "anthropic": "drop"}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	for _, tc := range mockProtocols {
		t.Run(string(tc.protocol), func(t *testing.T) {
			model, err := tc.create(server.Client(), server.URL+tc.endpoint, "synthetic-model-key")
			if err != nil {
				t.Fatal(err)
			}
			// 探测工具令默认脚本产生工具参数，以核对流截断时不会交付可执行的调用。
			for _, toolTurn := range []bool{false, true} {
				name := "text"
				if toolTurn {
					name = "tool"
				}
				t.Run(name, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
					defer cancel()
					req := provider.Request{Model: "mock-model", MaxOutputTokens: 128,
						Messages: []provider.Message{{Role: provider.RoleUser, Content: []provider.ContentBlock{{Type: provider.BlockText, Text: "试用断流"}}}},
					}
					if toolTurn {
						req.Tools = []provider.ToolDefinition{{Name: "roomusic_probe", InputSchema: json.RawMessage(`{"type":"object","properties":{}}`)}}
					}
					stream, err := model.Stream(ctx, req)
					if err != nil {
						t.Fatal(err)
					}
					defer stream.Close()
					var text strings.Builder
					for {
						event, err := stream.Next()
						if err != nil {
							var typed *provider.Error
							if !errors.As(err, &typed) || typed.Kind != provider.ErrorStreamInterrupted {
								t.Fatalf("断流结束错误 = %v，期望 stream_interrupted", err)
							}
							break
						}
						switch event.Type {
						case provider.EventDone, provider.EventToolCall:
							t.Fatalf("断流提交了完整轮次或工具调用: %+v", event)
						case provider.EventText:
							text.WriteString(event.Text)
						}
					}
					if !toolTurn && !strings.Contains(text.String(), "合成回复") {
						t.Fatalf("断流前未收到合成文本: %q", text.String())
					}
				})
			}
		})
	}
}
