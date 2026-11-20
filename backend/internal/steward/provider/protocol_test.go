package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const syntheticKey = "synthetic-provider-key-7f93"

func dataFrame(data string) string        { return "data: " + data + "\n\n" }
func namedFrame(name, data string) string { return "event: " + name + "\n" + dataFrame(data) }
func openAIEnd(reason string) string {
	return dataFrame(`{"choices":[{"index":0,"delta":{},"finish_reason":"`+reason+`"}]}`) + dataFrame("[DONE]")
}
func messagesEnd(reason string) string {
	return namedFrame("message_delta", `{"type":"message_delta","delta":{"stop_reason":"`+reason+`"}}`) + namedFrame("message_stop", `{"type":"message_stop"}`)
}
func textResponse(protocol Protocol, usage bool) string {
	if protocol == ProtocolOpenAI {
		body := dataFrame(`{"choices":[{"index":0,"delta":{"content":"合成"}}]}`) + dataFrame(`{"choices":[{"index":0,"delta":{"content":"回答"},"finish_reason":"stop"}]}`)
		if usage {
			body += dataFrame(`{"choices":[],"usage":{"prompt_tokens":7,"completion_tokens":3}}`)
		}
		return body + dataFrame("[DONE]")
	}
	start := `{"type":"message_start","message":{"usage":{"input_tokens":4,"cache_creation_input_tokens":1,"cache_read_input_tokens":2,"output_tokens":1}}}`
	if !usage {
		start = `{"type":"message_start","message":{}}`
	}
	body := namedFrame("message_start", start) + namedFrame("ping", `{"type":"ping"}`) + namedFrame("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"合成"}}`) + namedFrame("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"回答"}}`) + namedFrame("content_block_stop", `{"type":"content_block_stop","index":0}`)
	if usage {
		body += namedFrame("message_delta", `{"type":"message_delta","delta":{},"usage":{"output_tokens":3}}`)
	}
	return body + messagesEnd("end_turn")
}
func makeProvider(t *testing.T, protocol Protocol, client *http.Client, endpoint string) Provider {
	t.Helper()
	var p Provider
	var err error
	if protocol == ProtocolOpenAI {
		p, err = NewOpenAI(client, endpoint, syntheticKey)
	} else {
		p, err = NewMessages(client, endpoint, syntheticKey)
	}
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func testRequest() Request {
	return Request{Model: "synthetic-model", System: "合成系统提示", MaxOutputTokens: 128, Messages: []Message{{Role: RoleUser, Content: []ContentBlock{{Type: BlockText, Text: "合成问题"}}}}, Tools: []ToolDefinition{{Name: "lookup", Description: "合成查询", InputSchema: json.RawMessage(`{"type":"object","properties":{}}`)}}}
}
func collectStream(t *testing.T, p Provider, request Request) ([]Event, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := p.Stream(ctx, request)
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	var events []Event
	for {
		e, err := stream.Next()
		if errors.Is(err, io.EOF) {
			return events, nil
		}
		if err != nil {
			return events, err
		}
		events = append(events, e)
	}
}
func checkRequest(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		t.Error(err)
	}
	if r.Header.Get("Authorization") != "Bearer "+syntheticKey {
		t.Error("missing header key")
	}
	if strings.Contains(r.URL.String(), syntheticKey) || strings.Contains(string(raw), syntheticKey) {
		t.Error("key leaked outside request header")
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Error(err)
	}
	for _, field := range []string{"temperature", "top_p", "top_k", "tool_choice"} {
		if _, ok := body[field]; ok {
			t.Errorf("unexpected request parameter %s", field)
		}
	}
	return body
}
func TestProtocolsTextUsageAndRequestPrivacy(t *testing.T) {
	for _, protocol := range []Protocol{ProtocolOpenAI, ProtocolMessages} {
		for _, hasUsage := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/usage_%v", protocol, hasUsage), func(t *testing.T) {
				s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body := checkRequest(t, r)
					if protocol == ProtocolOpenAI {
						if r.URL.Path != "/v1/chat/completions" {
							t.Error(r.URL.Path)
						}
						if !reflect.DeepEqual(body["stream_options"], map[string]any{"include_usage": true}) {
							t.Errorf("usage not requested: %v", body)
						}
					} else {
						if r.URL.Path != "/v1/messages" || r.Header.Get("anthropic-version") != "2026-10-21" {
							t.Error("wrong Messages path/version")
						}
						if body["system"] != "合成系统提示" {
							t.Error("missing system")
						}
					}
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, textResponse(protocol, hasUsage))
				}))
				defer s.Close()
				events, err := collectStream(t, makeProvider(t, protocol, s.Client(), s.URL), testRequest())
				if err != nil {
					t.Fatal(err)
				}
				var text string
				var usage *Usage
				done := 0
				for _, e := range events {
					switch e.Type {
					case EventText:
						text += e.Text
					case EventUsage:
						usage = e.Usage
					case EventDone:
						done++
						if e.Finish.Reason != FinishNormal {
							t.Errorf("finish=%+v", e.Finish)
						}
					}
				}
				if text != "合成回答" || done != 1 {
					t.Fatalf("text=%q done=%d", text, done)
				}
				if (usage != nil) != hasUsage {
					t.Fatalf("usage=%+v", usage)
				}
				if usage != nil && (usage.InputTokens == nil || *usage.InputTokens != 7 || usage.OutputTokens == nil || *usage.OutputTokens != 3) {
					t.Fatalf("usage=%+v", usage)
				}
			})
		}
	}
}

func openAICalls(shape string, count int, name string) string {
	var body string
	for i := 0; i < count; i++ {
		initialName := name
		args := `{"query":"合成"}`
		if shape == "parameters" {
			args = `{"query":`
		}
		if shape == "name" {
			initialName = "look"
		}
		delta := map[string]any{"index": i, "id": fmt.Sprintf("call-%d", i), "type": "function", "function": map[string]any{"name": initialName, "arguments": args}}
		raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{delta}}}}})
		body += dataFrame(string(raw))
	}
	if shape != "whole" {
		for i := count - 1; i >= 0; i-- {
			function := map[string]any{}
			if shape == "parameters" {
				function["arguments"] = `"合成"}`
			} else {
				function["name"] = "up"
			}
			raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{"index": i, "function": function}}}}}})
			body += dataFrame(string(raw))
		}
	}
	return body + openAIEnd("tool_calls")
}
func messageCalls(count int, empty bool, name string) string {
	body := namedFrame("message_start", `{"type":"message_start","message":{}}`)
	for i := 0; i < count; i++ {
		raw, _ := json.Marshal(map[string]any{"type": "content_block_start", "index": i, "content_block": map[string]any{"type": "tool_use", "id": fmt.Sprintf("call-%d", i), "name": name, "input": map[string]any{}}})
		body += namedFrame("content_block_start", string(raw))
		parts := []string{`{"query":`, `"合成"}`}
		if empty {
			parts = []string{""}
		}
		for _, part := range parts {
			raw, _ := json.Marshal(map[string]any{"type": "content_block_delta", "index": i, "delta": map[string]any{"type": "input_json_delta", "partial_json": part}})
			body += namedFrame("content_block_delta", string(raw))
		}
		body += namedFrame("content_block_stop", fmt.Sprintf(`{"type":"content_block_stop","index":%d}`, i))
	}
	return body + messagesEnd("tool_use")
}
func TestProtocolsAccumulateSingleAndParallelToolCalls(t *testing.T) {
	for _, protocol := range []Protocol{ProtocolOpenAI, ProtocolMessages} {
		for _, count := range []int{1, 2} {
			shapes := []string{"parameters", "whole", "name"}
			if protocol == ProtocolMessages {
				shapes = []string{"parameters", "empty"}
			}
			for _, shape := range shapes {
				t.Run(fmt.Sprintf("%s/%d/%s", protocol, count, shape), func(t *testing.T) {
					body := openAICalls(shape, count, "lookup")
					if protocol == ProtocolMessages {
						body = messageCalls(count, shape == "empty", "lookup")
					}
					s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { checkRequest(t, r); _, _ = io.WriteString(w, body) }))
					defer s.Close()
					events, err := collectStream(t, makeProvider(t, protocol, s.Client(), s.URL), testRequest())
					if err != nil {
						t.Fatal(err)
					}
					var calls []ToolCall
					for _, e := range events {
						if e.Type == EventToolCall {
							calls = append(calls, *e.ToolCall)
						}
					}
					if len(calls) != count {
						t.Fatalf("calls=%+v", calls)
					}
					for i, c := range calls {
						want := `{"query":"合成"}`
						if shape == "empty" {
							want = `{}`
						}
						if c.ID != fmt.Sprintf("call-%d", i) || c.Name != "lookup" || string(c.Arguments) != want {
							t.Fatalf("call=%+v want args=%s", c, want)
						}
					}
					if events[len(events)-1].Finish.Reason != FinishToolCalls {
						t.Fatal("missing tool finish")
					}
				})
			}
		}
	}
}
func TestMessagesThinkingAndToolResultsRoundTrip(t *testing.T) {
	thinking := `{"type":"thinking","thinking":"","signature":"","extra":{"retained":true}}`
	body := namedFrame("message_start", `{"type":"message_start","message":{}}`) + namedFrame("content_block_start", `{"type":"content_block_start","index":0,"content_block":`+thinking+`}`) + namedFrame("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"opaque-thought"}}`) + namedFrame("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"opaque-signature"}}`) + namedFrame("content_block_stop", `{"type":"content_block_stop","index":0}`) + namedFrame("content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"redacted_thinking","data":"opaque-redacted"}}`) + namedFrame("content_block_stop", `{"type":"content_block_stop","index":1}`) + messagesEnd("end_turn")
	captured := make(chan map[string]any, 2)
	var requests atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured <- checkRequest(t, r)
		if requests.Add(1) == 1 {
			_, _ = io.WriteString(w, body)
		} else {
			_, _ = io.WriteString(w, textResponse(ProtocolMessages, false))
		}
	}))
	defer s.Close()
	p := makeProvider(t, ProtocolMessages, s.Client(), s.URL)
	events, err := collectStream(t, p, testRequest())
	if err != nil {
		t.Fatal(err)
	}
	assistant := Message{Role: RoleAssistant}
	for _, e := range events {
		if e.Type == EventOpaque {
			assistant.Content = append(assistant.Content, ContentBlock{Type: BlockOpaque, Opaque: e.Opaque})
		}
	}
	if len(assistant.Content) != 2 {
		t.Fatalf("opaque blocks=%+v", assistant.Content)
	}
	var opaque map[string]any
	_ = json.Unmarshal(assistant.Content[0].Opaque.Data, &opaque)
	if opaque["thinking"] != "opaque-thought" || opaque["signature"] != "opaque-signature" || opaque["extra"] == nil {
		t.Fatalf("thinking lost: %v", opaque)
	}
	for i := 0; i < 2; i++ {
		assistant.Content = append(assistant.Content, ContentBlock{Type: BlockToolCall, ToolCall: &ToolCall{ID: fmt.Sprintf("call-%d", i), Name: "lookup", Arguments: json.RawMessage(`{}`)}})
	}
	// 经存储 JSON 编解码再回传，思考块仍只属于原协议。
	raw, _ := json.Marshal(assistant)
	if err := json.Unmarshal(raw, &assistant); err != nil {
		t.Fatal(err)
	}
	request := testRequest()
	request.Messages = append(request.Messages, assistant, Message{Role: RoleTool, Content: []ContentBlock{{Type: BlockToolResult, ToolResult: &ToolResult{CallID: "call-0", Content: "first"}}}}, Message{Role: RoleUser, Content: []ContentBlock{{Type: BlockText, Text: "after results"}}}, Message{Role: RoleTool, Content: []ContentBlock{{Type: BlockToolResult, ToolResult: &ToolResult{CallID: "call-1", Content: "failed", IsError: true}}}})
	if _, err := collectStream(t, p, request); err != nil {
		t.Fatal(err)
	}
	<-captured
	received := <-captured
	messages := received["messages"].([]any)
	a := messages[1].(map[string]any)["content"].([]any)
	if !reflect.DeepEqual(a[0], opaque) {
		t.Fatalf("thinking altered: %v", a[0])
	}
	if a[1].(map[string]any)["data"] != "opaque-redacted" {
		t.Fatal("redacted block lost")
	}
	last := messages[2].(map[string]any)
	blocks := last["content"].([]any)
	if len(messages) != 3 || last["role"] != "user" || len(blocks) != 3 {
		t.Fatalf("parallel results shape=%v", messages)
	}
	for i := 0; i < 2; i++ {
		b := blocks[i].(map[string]any)
		if b["type"] != "tool_result" || b["tool_use_id"] != fmt.Sprintf("call-%d", i) || b["is_error"] != (i == 1) {
			t.Errorf("tool result=%v", b)
		}
	}
	if blocks[2].(map[string]any)["type"] != "text" {
		t.Fatal("tool results must precede text")
	}
}
func TestOpenAIToolResultWireShape(t *testing.T) {
	request := testRequest()
	request.Messages = append(request.Messages, Message{Role: RoleAssistant, Content: []ContentBlock{{Type: BlockToolCall, ToolCall: &ToolCall{ID: "a", Name: "lookup", Arguments: json.RawMessage(`{"query":"合成"}`)}}, {Type: BlockOpaque, Opaque: &OpaqueBlock{Protocol: ProtocolMessages, Data: json.RawMessage(`{"type":"thinking","signature":"foreign"}`)}}}}, Message{Role: RoleTool, Content: []ContentBlock{{Type: BlockToolResult, ToolResult: &ToolResult{CallID: "a", Content: `{"code":"failed"}`, IsError: true}}}})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := checkRequest(t, r)
		raw, _ := json.Marshal(body)
		if strings.Contains(string(raw), "foreign") {
			t.Error("cross-protocol opaque block leaked")
		}
		messages := body["messages"].([]any)
		assistant := messages[len(messages)-2].(map[string]any)
		call := assistant["tool_calls"].([]any)[0].(map[string]any)
		fn := call["function"].(map[string]any)
		if fn["arguments"] != `{"query":"合成"}` || call["type"] != "function" {
			t.Errorf("call=%v", call)
		}
		result := messages[len(messages)-1].(map[string]any)
		if !reflect.DeepEqual(result, map[string]any{"role": "tool", "tool_call_id": "a", "content": `{"code":"failed"}`}) {
			t.Errorf("result=%v", result)
		}
		_, _ = io.WriteString(w, textResponse(ProtocolOpenAI, false))
	}))
	defer s.Close()
	if _, err := collectStream(t, makeProvider(t, ProtocolOpenAI, s.Client(), s.URL), request); err != nil {
		t.Fatal(err)
	}
}

func TestProtocolsHTTPAndStreamErrorsRedactKey(t *testing.T) {
	for _, protocol := range []Protocol{ProtocolOpenAI, ProtocolMessages} {
		for _, tc := range []struct {
			name   string
			status int
			body   string
			kind   ErrorKind
		}{{"401", 401, `{"error":{"message":"` + syntheticKey + `"}}`, ErrorAuthentication}, {"404_model", 404, `{"error":{"code":"model_not_found","message":"missing"}}`, ErrorModelNotFound}, {"404_model_type", 404, `{"error":{"type":"model_not_found","message":"missing"}}`, ErrorModelNotFound}, {"404_route", 404, `{"error":{"message":"route missing"}}`, ErrorOther}, {"429", 429, `{"error":{"message":"quota"}}`, ErrorRateLimit}, {"500", 500, `{"error":{"message":"` + syntheticKey + `"}}`, ErrorOther}, {"interrupted", 200, "", ErrorStreamInterrupted}, {"error_event", 200, namedFrame("error", `{"type":"error","error":{"type":"rate_limit_error","message":"`+syntheticKey+`"}}`), ErrorRateLimit}} {
			t.Run(string(protocol)+"/"+tc.name, func(t *testing.T) {
				s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					checkRequest(t, r)
					w.WriteHeader(tc.status)
					if tc.name == "interrupted" {
						body := textResponse(protocol, false)
						if protocol == ProtocolOpenAI {
							body = strings.TrimSuffix(body, dataFrame("[DONE]"))
						} else {
							body = strings.TrimSuffix(body, namedFrame("message_stop", `{"type":"message_stop"}`))
						}
						_, _ = io.WriteString(w, body)
					} else {
						_, _ = io.WriteString(w, tc.body)
					}
				}))
				defer s.Close()
				_, err := collectStream(t, makeProvider(t, protocol, s.Client(), s.URL), testRequest())
				var typed *Error
				if !errors.As(err, &typed) || typed.Kind != tc.kind {
					t.Fatalf("got %v want %s", err, tc.kind)
				}
				if strings.Contains(err.Error(), syntheticKey) {
					t.Fatal("error contains key")
				}
			})
		}
	}
}
func TestProtocolsCancelBlockedReadReleasesConnection(t *testing.T) {
	for _, protocol := range []Protocol{ProtocolOpenAI, ProtocolMessages} {
		t.Run(string(protocol), func(t *testing.T) {
			released := make(chan struct{})
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(200)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				close(released)
			}))
			defer s.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			p := makeProvider(t, protocol, s.Client(), s.URL)
			stream, err := p.Stream(ctx, testRequest())
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			returned := make(chan error, 1)
			go func() { _, err := stream.Next(); returned <- err }()
			cancel()
			select {
			case err := <-returned:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("got %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("Next did not stop on cancellation")
			}
			select {
			case <-released:
			case <-time.After(time.Second):
				t.Fatal("connection was not released")
			}
		})
	}
}

func TestPresetRecognitionDoesNotSendRequests(t *testing.T) {
	for _, tc := range []struct {
		endpoint, name string
		protocol       Protocol
	}{{"https://api.openai.com/v1", "OpenAI", ProtocolOpenAI}, {"https://api.anthropic.com", "Anthropic", ProtocolMessages}, {"https://api.deepseek.com/anthropic", "DeepSeek", ProtocolMessages}, {"https://api.deepseek.com", "DeepSeek", ProtocolOpenAI}, {"http://127.0.0.1:11434", "Ollama", ProtocolOpenAI}, {"http://[::1]:1234/v1", "LM Studio", ProtocolOpenAI}, {"https://unknown.invalid/v1", "", ""}} {
		u, _ := url.Parse(tc.endpoint)
		p := matchPreset(u)
		if tc.name == "" {
			if p != nil {
				t.Fatal("unknown preset matched")
			}
			continue
		}
		if p == nil || p.name != tc.name || p.protocol != tc.protocol {
			t.Errorf("%s preset=%+v", tc.endpoint, p)
		}
	}
}
func TestProbeProtocolsModelsAndThreeToolConclusions(t *testing.T) {
	for _, protocol := range []Protocol{ProtocolOpenAI, ProtocolMessages} {
		for _, support := range []ToolSupport{ToolSupportSupported, ToolSupportUnsupported, ToolSupportUnconfirmed} {
			t.Run(string(protocol)+"/"+string(support), func(t *testing.T) {
				s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Authorization") != "Bearer "+syntheticKey {
						t.Error("missing probe key")
					}
					if strings.Contains(r.URL.String(), syntheticKey) {
						t.Error("key in URL")
					}
					if r.Method == http.MethodGet {
						w.Header().Set("Content-Type", "application/json")
						if protocol == ProtocolMessages {
							if r.URL.Query().Get("after_id") == "a" {
								_, _ = io.WriteString(w, `{"data":[{"id":"b","display_name":"模型乙"}],"has_more":false,"last_id":"b"}`)
							} else {
								_, _ = io.WriteString(w, `{"data":[{"id":"a","display_name":"模型甲"}],"has_more":true,"last_id":"a"}`)
							}
						} else {
							_, _ = io.WriteString(w, `{"data":[{"id":"a"},{"id":"b"}]}`)
						}
						return
					}
					checkRequest(t, r)
					if r.URL.Path == "/v1/responses" {
						http.NotFound(w, r)
						return
					}
					if support == ToolSupportUnsupported {
						w.WriteHeader(400)
						_, _ = io.WriteString(w, `{"error":{"message":"model does not support tools"}}`)
						return
					}
					if support == ToolSupportUnconfirmed {
						_, _ = io.WriteString(w, textResponse(protocol, false))
						return
					}
					if protocol == ProtocolOpenAI {
						_, _ = io.WriteString(w, openAICalls("whole", 1, "roomusic_probe"))
					} else {
						_, _ = io.WriteString(w, messageCalls(1, true, "roomusic_probe"))
					}
				}))
				defer s.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				result, err := Probe(ctx, s.Client(), s.URL, syntheticKey, "a")
				if err != nil {
					t.Fatal(err)
				}
				if result.Protocol != protocol || result.ToolSupport != support || len(result.Models) != 2 || result.Models[0].ID != "a" || result.Models[1].ID != "b" {
					t.Fatalf("probe=%+v", result)
				}
				if protocol == ProtocolOpenAI {
					if result.ResponsesAvailable || len(result.Failures) == 0 || result.Failures[0].Step != "responses" || result.Failures[0].Protocol != ProtocolResponses || result.Failures[0].Error.StatusCode != http.StatusNotFound {
						t.Fatalf("Responses 失败未保留在回退结果中：%+v", result)
					}
				}
				raw, _ := json.Marshal(result)
				if strings.Contains(string(raw), syntheticKey) {
					t.Fatal("probe exposes key")
				}
			})
		}
	}
}
