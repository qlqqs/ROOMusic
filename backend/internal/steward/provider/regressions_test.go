package provider

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOpenAITokenBudgetFieldByEndpoint(t *testing.T) {
	for _, tc := range []struct{ endpoint, field string }{{"https://api.openai.com/v1", "max_completion_tokens"}, {"https://api.deepseek.com/v1", "max_tokens"}, {"http://127.0.0.1:11434", "max_tokens"}, {"https://api.openai.com.example.invalid/v1", "max_tokens"}} {
		t.Run(tc.endpoint, func(t *testing.T) {
			// 只检查请求编码，不连接预设中的真实端点。
			p, err := newAdapter(&http.Client{}, tc.endpoint, "", ProtocolOpenAI)
			if err != nil {
				t.Fatal(err)
			}
			body, err := p.openAIRequest(testRequest())
			if err != nil {
				t.Fatal(err)
			}
			if body[tc.field] != 128 {
				t.Errorf("预算字段错误：%v", body)
			}
			other := "max_tokens"
			if tc.field == other {
				other = "max_completion_tokens"
			}
			if _, ok := body[other]; ok {
				t.Errorf("出现多余预算字段 %s", other)
			}
		})
	}
}

func TestProtocolsOutputLimitInsideToolArguments(t *testing.T) {
	for _, protocol := range []Protocol{ProtocolOpenAI, ProtocolMessages} {
		t.Run(string(protocol), func(t *testing.T) {
			body := dataFrame(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"partial","function":{"name":"lookup","arguments":"{\"query\":"}}]},"finish_reason":"length"}]}`) + dataFrame("[DONE]")
			if protocol == ProtocolMessages {
				body = namedFrame("message_start", `{"type":"message_start","message":{}}`) + namedFrame("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"partial","name":"lookup","input":{}}}`) + namedFrame("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"query\":"}}`) + namedFrame("content_block_stop", `{"type":"content_block_stop","index":0}`) + messagesEnd("max_tokens")
			}
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, body) }))
			defer s.Close()
			events, err := collectStream(t, makeProvider(t, protocol, s.Client(), s.URL), testRequest())
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range events {
				if e.Type == EventToolCall {
					t.Error("截断参数仍被当作完整工具调用")
				}
			}
			if len(events) == 0 || events[len(events)-1].Finish == nil || events[len(events)-1].Finish.Reason != FinishOutputLimit {
				t.Fatalf("结束事件=%+v", events)
			}
		})
	}
}

func TestProtocolsTimeoutClassification(t *testing.T) {
	for _, protocol := range []Protocol{ProtocolOpenAI, ProtocolMessages} {
		t.Run(string(protocol), func(t *testing.T) {
			release := make(chan struct{})
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				select {
				case <-r.Context().Done():
				case <-release:
				}
			}))
			defer s.Close()
			defer close(release)
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			_, err := makeProvider(t, protocol, s.Client(), s.URL).Stream(ctx, testRequest())
			var failure *Error
			if !errors.As(err, &failure) || failure.Kind != ErrorTimeout {
				t.Fatalf("超时=%v", err)
			}
		})
	}
}
