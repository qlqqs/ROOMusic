package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func c8ResponseItem(index int, item string) string {
	return namedFrame("response.output_item.done", fmt.Sprintf(`{"type":"response.output_item.done","output_index":%d,"item":%s}`, index, item))
}
func c8ResponseEnd() string {
	return namedFrame("response.completed", `{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":9,"output_tokens":4,"input_tokens_details":{"cached_tokens":5},"output_tokens_details":{"reasoning_tokens":2}}}}`)
}
func c8MessageBlock(index int, block string) string {
	return namedFrame("content_block_start", fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":%s}`, index, block)) + namedFrame("content_block_stop", fmt.Sprintf(`{"type":"content_block_stop","index":%d}`, index))
}
func TestC8ResponsesRequestToolsResultsAndForcedReview(t *testing.T) {
	for _, force := range []string{"", "submit_review"} {
		t.Run(force, func(t *testing.T) {
			req := testRequest()
			req.NativeSearch = true
			req.ForceTool = force
			if force != "" {
				req.Tools[0].Name = force
			}
			req.Messages = append(req.Messages, Message{Role: RoleTool, Content: []ContentBlock{{Type: BlockToolResult, ToolResult: &ToolResult{CallID: "call-1", Content: `{"ok":true}`}}}})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				if r.URL.Path != "/v1/responses" || body["store"] != false || body["stream"] != true || body["instructions"] != req.System {
					t.Errorf("请求=%v", body)
				}
				tools := body["tools"].([]any)
				function := tools[0].(map[string]any)
				if function["type"] != "function" || function["strict"] != false || function["name"] != req.Tools[0].Name || function["parameters"] == nil {
					t.Errorf("函数工具=%v", function)
				}
				last := body["input"].([]any)[1]
				if !reflect.DeepEqual(last, map[string]any{"type": "function_call_output", "call_id": "call-1", "output": `{"ok":true}`}) {
					t.Errorf("工具结果=%v", last)
				}
				if force != "" {
					if len(tools) != 1 || !reflect.DeepEqual(body["tool_choice"], map[string]any{"type": "function", "name": "submit_review"}) || body["parallel_tool_calls"] != false {
						t.Errorf("强制工具=%v", body)
					}
				} else if len(tools) != 2 || tools[1].(map[string]any)["type"] != "web_search" {
					t.Errorf("搜索工具=%v", tools)
				}
				io.WriteString(w, c8ResponseEnd())
			}))
			defer server.Close()
			p, err := NewResponses(server.Client(), server.URL, syntheticKey)
			if err != nil {
				t.Fatal(err)
			}
			events, err := collectStream(t, p, req)
			if err != nil {
				t.Fatal(err)
			}
			u := events[len(events)-2].Usage
			if u == nil || *u.InputTokens != 9 || *u.OutputTokens != 4 {
				t.Fatalf("用量=%+v", u)
			}
		})
	}
}
func TestC8ResponsesStreamingSearchCitationsAndCalls(t *testing.T) {
	body := namedFrame("response.output_text.delta", `{"type":"response.output_text.delta","delta":"合成回答"}`) + c8ResponseItem(0, `{"id":"search-1","type":"web_search_call","status":"completed","action":{"type":"search","queries":["查询甲","查询乙"],"sources":[{"url":"https://example.org"},{"url":"https://example.org","title":"来源"},{"url":"file:///secret"}]}}`) + c8ResponseItem(1, `{"type":"message","content":[{"type":"output_text","text":"合成回答","annotations":[{"type":"url_citation","url":"https://example.org","title":"来源"}]}]}`) + namedFrame("response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","delta":"invalid-partial"}`) + c8ResponseItem(2, `{"type":"function_call","id":"item-1","call_id":"call-1","name":"lookup","arguments":"{\"query\":\"合成\"}"}`) + c8ResponseEnd()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) }))
	defer server.Close()
	p, _ := NewResponses(server.Client(), server.URL, syntheticKey)
	events, err := collectStream(t, p, testRequest())
	if err != nil {
		t.Fatal(err)
	}
	calls, searches, citations := 0, 0, 0
	for _, e := range events {
		switch e.Type {
		case EventToolCall:
			calls++
			if e.ToolCall.ID != "call-1" || string(e.ToolCall.Arguments) != `{"query":"合成"}` {
				t.Fatal(e)
			}
		case EventSearch:
			searches++
			if e.Search.Query != "查询甲\n查询乙" || len(e.Search.Results) != 1 || e.Search.Results[0].Title != "来源" {
				t.Fatal(e.Search)
			}
		case EventCitations:
			citations++
			if len(e.Citations) != 1 {
				t.Fatal(e)
			}
		}
	}
	done := events[len(events)-1]
	if calls != 1 || searches != 1 || citations != 1 || done.Finish.Reason != FinishToolCalls || len(done.ProviderBlocks) != 3 {
		t.Fatalf("事件=%+v", events)
	}
}
func TestC8ResponsesErrorsRedactAndRejectIncomplete(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		kind       ErrorKind
	}{
		{"rate_limit", namedFrame("response.failed", `{"type":"response.failed","response":{"error":{"code":"rate_limit_exceeded","message":"`+syntheticKey+`"}}}`), ErrorRateLimit},
		{"error", namedFrame("error", `{"type":"error","code":"rate_limit_exceeded","message":"`+syntheticKey+`"}`), ErrorRateLimit},
		{"unfinished", namedFrame("response.output_item.added", `{"type":"response.output_item.added","output_index":0,"item":{"type":"message"}}`) + c8ResponseEnd(), ErrorInvalidResponse},
		{"interrupted", namedFrame("response.output_text.delta", `{"type":"response.output_text.delta","delta":"私密内容"}`), ErrorStreamInterrupted},
		{"bad_json", dataFrame(`{"type":"` + syntheticKey), ErrorInvalidResponse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, tc.body) }))
			defer server.Close()
			p, _ := NewResponses(server.Client(), server.URL, syntheticKey)
			_, err := collectStream(t, p, testRequest())
			var typed *Error
			if !errors.As(err, &typed) || typed.Kind != tc.kind {
				t.Fatalf("错误=%v 期望=%s", err, tc.kind)
			}
			if strings.Contains(err.Error(), syntheticKey) || strings.Contains(err.Error(), "私密内容") {
				t.Fatal("公开错误泄漏")
			}
		})
	}
}
func TestC8MessagesNativeSearchPauseAndReplay(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(fmt.Sprint(failed), func(t *testing.T) {
			result := `[{"type":"web_search_result","url":"https://example.org","title":"来源","encrypted_content":"opaque-encrypted"}]`
			if failed {
				result = `{"type":"web_search_tool_result_error","error_code":"max_uses_exceeded"}`
			}
			first := namedFrame("message_start", `{"type":"message_start","message":{}}`) + namedFrame("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"server_tool_use","id":"search-1","name":"web_search","input":{}}}`) + namedFrame("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"query\":\"私密查询\"}"}}`) + namedFrame("content_block_stop", `{"type":"content_block_stop","index":0}`) + messagesEnd("pause_turn")
			second := c8MessageBlock(0, `{"type":"web_search_tool_result","tool_use_id":"search-1","content":`+result+`}`) + namedFrame("content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"text","text":"回答"}}`) + namedFrame("content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"citations_delta","citation":{"type":"web_search_result_location","url":"https://example.org","title":"来源","encrypted_index":"opaque-index"}}}`) + namedFrame("content_block_stop", `{"type":"content_block_stop","index":1}`) + messagesEnd("end_turn")
			count := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count++
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				raw, _ := json.Marshal(body)
				if !strings.Contains(string(raw), `"max_uses":5`) || !strings.Contains(string(raw), "web_search_20250305") {
					t.Errorf("搜索定义=%s", raw)
				}
				if count == 1 {
					io.WriteString(w, first)
				} else {
					if !strings.Contains(string(raw), "search-1") || !strings.Contains(string(raw), "私密查询") {
						t.Error("pause_turn 丢失原始块")
					}
					if count == 3 && !failed && !strings.Contains(string(raw), "opaque-encrypted") {
						t.Error("多轮重放丢失密文")
					}
					io.WriteString(w, second)
				}
			}))
			defer server.Close()
			p := makeProvider(t, ProtocolMessages, server.Client(), server.URL)
			req := testRequest()
			req.NativeSearch = true
			events, err := collectStream(t, p, req)
			if err != nil {
				t.Fatal(err)
			}
			done := events[len(events)-1]
			if done.Finish.Reason != FinishPause || len(done.ProviderBlocks) != 1 {
				t.Fatalf("pause=%+v", done)
			}
			req.Messages = append(req.Messages, Message{Role: RoleAssistant, Content: []ContentBlock{}, ProviderBlocks: done.ProviderBlocks})
			events, err = collectStream(t, p, req)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, e := range events {
				if e.Type == EventSearch {
					found = true
					if e.Search.Query != "私密查询" || e.Search.SearchID != "search-1" {
						t.Fatal(e.Search)
					}
					if failed && (e.Search.Status != "failed" || e.Search.ErrorCode != "search_failed") {
						t.Fatal(e.Search)
					}
				}
			}
			if !found {
				t.Fatal("缺少搜索事件")
			}
			done = events[len(events)-1]
			raw, _ := json.Marshal(done.ProviderBlocks)
			if !strings.Contains(string(raw), "opaque-index") {
				t.Fatal("引用原始字段未保留")
			}
			req.Messages = append(req.Messages, Message{Role: RoleAssistant, Content: []ContentBlock{}, ProviderBlocks: done.ProviderBlocks})
			if _, err = collectStream(t, p, req); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestC8MessagesForcedReviewExcludesSearch(t *testing.T) {
	req := testRequest()
	req.NativeSearch = true
	req.ForceTool = "submit_review"
	body, err := messagesRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(body)
	if strings.Contains(string(raw), "web_search") {
		t.Fatal("审查携带搜索")
	}
}
func TestC8ProbeResponsesAndNativeSearch(t *testing.T) {
	for _, tc := range []struct {
		name        string
		protocol    Protocol
		available   bool
		chatCalls   int32
		nativeCalls int32
		modelCalls  int32
	}{
		{"tools_supported", ProtocolResponses, true, 0, 1, 2},
		{"not_found", ProtocolOpenAI, false, 1, 0, 2},
		{"no_tool_call", ProtocolOpenAI, true, 1, 1, 3},
	} {
		for _, supported := range []bool{true, false} {
			t.Run(tc.name+"/native_"+fmt.Sprint(supported), func(t *testing.T) {
				var responsesCalls, chatCalls, nativeCalls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet {
						if r.URL.Path != "/v1/models" {
							t.Errorf("模型列表路径=%s", r.URL.Path)
						}
						io.WriteString(w, `{"data":[{"id":"synthetic-model"}]}`)
						return
					}
					body := checkRequest(t, r)
					if body["model"] != "synthetic-model" || body["stream"] != true {
						t.Errorf("模型请求=%v", body)
					}
					switch r.URL.Path {
					case "/v1/responses":
						raw, _ := json.Marshal(body["tools"])
						if strings.Contains(string(raw), "web_search") {
							nativeCalls.Add(1)
							if responsesCalls.Load() != 1 || chatCalls.Load() != tc.chatCalls {
								t.Error("自带搜索没有在工具探测之后执行")
							}
							if !supported {
								w.WriteHeader(http.StatusBadRequest)
								io.WriteString(w, `{"error":{"code":"tool_unsupported","message":"web_search is not supported"}}`)
								return
							}
							io.WriteString(w, c8ResponseItem(0, `{"type":"web_search_call","id":"s1","status":"completed","action":{"type":"search","query":"合成","sources":[]}}`)+c8ResponseEnd())
							return
						}
						responsesCalls.Add(1)
						if chatCalls.Load() != 0 || body["max_output_tokens"] != float64(1024) || !strings.Contains(string(raw), "roomusic_probe") {
							t.Errorf("Responses 未优先执行工具探测：%v", body)
						}
						switch tc.name {
						case "not_found":
							http.NotFound(w, r)
						case "no_tool_call":
							io.WriteString(w, c8ResponseEnd())
						default:
							io.WriteString(w, c8ResponseItem(0, `{"type":"function_call","id":"item-1","call_id":"call-1","name":"roomusic_probe","arguments":"{}"}`)+c8ResponseEnd())
						}
					case "/v1/chat/completions":
						chatCalls.Add(1)
						if responsesCalls.Load() != 1 {
							t.Error("Chat Completions 没有等待 Responses 探测")
						}
						io.WriteString(w, openAICalls("whole", 1, "roomusic_probe"))
					default:
						t.Errorf("意外模型路径=%s", r.URL.Path)
						http.NotFound(w, r)
					}
				}))
				defer server.Close()
				result, err := Probe(context.Background(), server.Client(), server.URL, syntheticKey, "synthetic-model")
				if err != nil {
					t.Fatal(err)
				}
				if result.Protocol != tc.protocol || result.ToolSupport != ToolSupportSupported || result.ResponsesAvailable != tc.available {
					t.Fatalf("工具探测=%+v", result)
				}
				wantSearch := NativeSearchUnknown
				if tc.available {
					wantSearch = NativeSearchSupported
					if !supported {
						wantSearch = NativeSearchUnsupported
					}
				}
				if result.NativeSearch != wantSearch {
					t.Fatalf("自带搜索探测=%+v", result)
				}
				if responsesCalls.Load() != 1 || chatCalls.Load() != tc.chatCalls || nativeCalls.Load() != tc.nativeCalls || responsesCalls.Load()+chatCalls.Load()+nativeCalls.Load() != tc.modelCalls {
					t.Fatalf("模型调用次数：Responses=%d，Chat=%d，自带搜索=%d；期望总数=%d", responsesCalls.Load(), chatCalls.Load(), nativeCalls.Load(), tc.modelCalls)
				}
				wantFailures := 0
				if tc.name == "not_found" {
					wantFailures = 1
					if len(result.Failures) != 1 || result.Failures[0].Step != "responses" || result.Failures[0].Protocol != ProtocolResponses || result.Failures[0].Error.StatusCode != http.StatusNotFound {
						t.Fatalf("Responses 失败归属错误：%+v", result.Failures)
					}
				} else if !supported {
					wantFailures = 1
					if len(result.Failures) != 1 || result.Failures[0].Step != "native_search" || result.Failures[0].Protocol != ProtocolResponses || result.Failures[0].Error.Kind != ErrorToolUnsupported {
						t.Fatalf("自带搜索失败归属错误：%+v", result.Failures)
					}
				}
				if len(result.Failures) != wantFailures {
					t.Fatalf("不应产生额外探测失败：%+v", result.Failures)
				}
			})
		}
	}
}

func TestC8MessagesProbeNativeCapabilities(t *testing.T) {
	for _, tc := range []struct {
		capability string
		want       NativeSearchSupport
	}{{`true`, NativeSearchSupported}, {`false`, NativeSearchUnsupported}, {`null`, NativeSearchUnknown}} {
		t.Run(tc.capability, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" {
					io.WriteString(w, `{"data":[{"id":"synthetic-model","capabilities":{"server_tools":{"web_search":{"supported":`+tc.capability+`}}}}],"has_more":false}`)
					return
				}
				io.WriteString(w, messageCalls(1, true, "roomusic_probe"))
			}))
			defer server.Close()
			result, err := Probe(context.Background(), server.Client(), server.URL, syntheticKey, "synthetic-model")
			if err != nil || result.Protocol != ProtocolMessages || result.NativeSearch != tc.want || result.ResponsesAvailable {
				t.Fatalf("结果=%+v 错误=%v", result, err)
			}
		})
	}
}
func TestC8ResponsesIncompleteSearchAndTokenLimit(t *testing.T) {
	body := c8ResponseItem(0, `{"type":"web_search_call","id":"failed-search","status":"failed","action":{"type":"search"}}`) + c8ResponseItem(1, `{"type":"function_call","id":"item","call_id":"call","name":"lookup","status":"incomplete","arguments":"{"}`) + namedFrame("response.incomplete", `{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) }))
	defer server.Close()
	p, _ := NewResponses(server.Client(), server.URL, syntheticKey)
	events, err := collectStream(t, p, testRequest())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.Type == EventToolCall {
			t.Fatal("未完成参数被执行")
		}
		if e.Type == EventSearch && (e.Search.Status != "failed" || e.Search.ErrorCode != "search_failed" || e.Search.Query != "" || e.Search.Results == nil) {
			t.Fatal(e.Search)
		}
	}
	done := events[len(events)-1]
	if done.Finish.Reason != FinishOutputLimit || len(done.ProviderBlocks) != 1 {
		t.Fatalf("结束=%+v", done)
	}
}
