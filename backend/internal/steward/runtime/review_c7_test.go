package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/steward/provider"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

const c7ReviewOpaqueContent = "synthetic-c7-encrypted-reasoning"

func TestC7ReviewRejectsInvalidResults(t *testing.T) {
	item := `{"item_id":"a","decision":"approved","risk":"low","reasons":["证据充分"]}`
	cases := map[string]string{
		"valid":              `{"items":[` + item + `]}`,
		"missing":            `{"items":[]}`,
		"extra":              `{"items":[` + item + `,` + strings.ReplaceAll(item, `"a"`, `"b"`) + `]}`,
		"foreign":            `{"items":[` + strings.ReplaceAll(item, `"a"`, `"b"`) + `]}`,
		"duplicate_id":       `{"items":[` + item + `,` + item + `]}`,
		"duplicate_key":      `{"items":[],"items":[` + item + `]}`,
		"duplicate_decision": `{"items":[` + strings.Replace(item, `"decision":"approved"`, `"decision":"rejected","decision":"approved"`, 1) + `]}`,
		"unknown_key":        `{"items":[` + strings.Replace(item, `"risk"`, `"source":"model","risk"`, 1) + `]}`,
		"missing_risk":       `{"items":[` + strings.Replace(item, `"risk":"low",`, "", 1) + `]}`,
		"null_reasons":       `{"items":[` + strings.Replace(item, `["证据充分"]`, `null`, 1) + `]}`,
		"empty_reasons":      `{"items":[` + strings.Replace(item, `["证据充分"]`, `[]`, 1) + `]}`,
		"blank_reason":       `{"items":[` + strings.Replace(item, `证据充分`, `  `, 1) + `]}`,
		"nul_reason":         `{"items":[` + strings.Replace(item, `证据充分`, `\u0000`, 1) + `]}`,
		"wrong_enum":         `{"items":[` + strings.Replace(item, `approved`, `approve`, 1) + `]}`,
		"wrong_type":         `{"items":[` + strings.Replace(item, `"low"`, `1`, 1) + `]}`,
		"trailing_json":      `{"items":[` + item + `]} {}`,
		"invalid_utf8":       `{"items":[` + strings.Replace(item, `证据充分`, string([]byte{0xff}), 1) + `]}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			items := []PlanItem{{ID: "a"}}
			if name == "duplicate_id" {
				items = append(items, PlanItem{ID: "b"})
			}
			got, err := validateReview(json.RawMessage(raw), items)
			if name == "valid" {
				if err != nil || len(got) != 1 || got[0].Source != "model" || got[0].Decision != "approved" {
					t.Fatal(got, err)
				}
			} else if !errors.Is(err, ErrReviewUnavailable) {
				t.Fatalf("应拒绝无效审查，got=%+v err=%v", got, err)
			}
		})
	}
}

type c7ProviderFunc func(context.Context, provider.Request) (provider.Stream, error)

func (f c7ProviderFunc) Stream(ctx context.Context, r provider.Request) (provider.Stream, error) {
	return f(ctx, r)
}

type c7ReviewStream struct {
	events            []provider.Event
	nextErr, closeErr error
	closed            bool
}

func (s *c7ReviewStream) Next() (provider.Event, error) {
	if len(s.events) == 0 {
		if s.nextErr != nil {
			return provider.Event{}, s.nextErr
		}
		return provider.Event{}, io.EOF
	}
	e := s.events[0]
	s.events = s.events[1:]
	return e, nil
}
func (s *c7ReviewStream) Close() error { s.closed = true; return s.closeErr }

func TestC7ReviewCallStrictStreamAndDeadline(t *testing.T) {
	want := []ReviewItem{{ItemID: "a", Decision: "approved", Risk: "low", Reasons: []string{"证据充分"}, Source: "model"}}
	for _, name := range []string{"valid", "text", "opaque", "wrong_tool", "multiple_calls", "no_call", "wrong_finish", "output_limit", "content_filter", "nil_finish", "nil_call", "nil_usage", "truncated", "close_error", "stream_error"} {
		t.Run(name, func(t *testing.T) {
			call := modelCall("submit_review", `{"items":[{"item_id":"a","decision":"approved","risk":"low","reasons":["证据充分"]}]}`)
			events := []provider.Event{call, modelUsage(12, 4), modelDone(provider.FinishToolCalls)}
			stream := &c7ReviewStream{}
			switch name {
			case "text":
				events = append([]provider.Event{{Type: provider.EventText, Text: "批准"}}, events...)
			case "opaque":
				block := provider.OpaqueBlock{Protocol: provider.ProtocolResponses, Data: json.RawMessage(`{"type":"reasoning","encrypted_content":"` + c7ReviewOpaqueContent + `"}`)}
				events[2].ProviderBlocks = []provider.OpaqueBlock{block}
				events = append([]provider.Event{{Type: provider.EventOpaque, Opaque: &block}}, events...)
			case "wrong_tool":
				events[0] = modelCall("other", `{}`)
			case "multiple_calls":
				events = append([]provider.Event{call}, events...)
			case "no_call":
				events = events[1:]
			case "wrong_finish":
				events[2] = modelDone(provider.FinishNormal)
			case "output_limit":
				events[2] = modelDone(provider.FinishOutputLimit)
			case "content_filter":
				events[2] = provider.Event{Type: provider.EventDone, Finish: &provider.Finish{Reason: provider.FinishOther, RawReason: "content_filter"}}
			case "nil_finish":
				events[2] = provider.Event{Type: provider.EventDone}
			case "nil_call":
				events[0] = provider.Event{Type: provider.EventToolCall}
			case "nil_usage":
				events[1] = provider.Event{Type: provider.EventUsage}
			case "truncated":
				events = events[:1]
			case "close_error":
				stream.closeErr = errors.New("C7 私密关闭错误")
			case "stream_error":
				events = nil
				stream.nextErr = errors.New("C7 私密流错误")
			}
			stream.events = events
			hits := 0
			p := c7ProviderFunc(func(ctx context.Context, r provider.Request) (provider.Stream, error) {
				hits++
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) < 59*time.Second || time.Until(deadline) > 60*time.Second {
					t.Error("审查未设置 60 秒超时")
				}
				if r.ForceTool != "submit_review" || len(r.Tools) != 1 || r.Tools[0].Name != "submit_review" || len(r.Messages) != 1 || r.Messages[0].Role != provider.RoleUser || r.Model != "review-model" || r.System != ReviewSystemPrompt {
					t.Errorf("request=%+v", r)
				}
				return stream, nil
			})
			results, usage, err := callReview(context.Background(), p, "review-model", []PlanItem{{ID: "a", Tool: "correct_release", Revertible: true}}, nil)
			if name == "valid" || name == "opaque" || name == "wrong_finish" {
				if err != nil || !reflect.DeepEqual(results, want) || usage.InputTokens == nil || *usage.InputTokens != 12 {
					t.Fatal(results, usage, err)
				}
			} else if err == nil {
				t.Fatal("错误流被接受")
			}
			if hits != 1 || !stream.closed {
				t.Errorf("hits=%d closed=%v", hits, stream.closed)
			}
		})
	}
}

func TestC7ReviewResponsesReasoningIsDiscarded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/responses" {
			t.Errorf("Responses 请求为 %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `event: response.output_item.done
data: {"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"`+c7ReviewOpaqueContent+`"}}

event: response.output_item.done
data: {"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"submit_review","status":"completed","arguments":"{\"items\":[{\"item_id\":\"item-1\",\"decision\":\"needs_human_confirmation\",\"risk\":\"high\",\"reasons\":[\"需要人工核对\"]}]}"}}

event: response.completed
data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":12,"output_tokens":4}}}

`)
	}))
	defer server.Close()
	p, err := provider.NewResponses(server.Client(), server.URL, "synthetic-key")
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	params := runParams(p)
	params.Caller.User.Role = "admin"
	params.ReviewModel = "gpt-6.2-sol"
	store := &backgroundRunStore{}
	plan, err := store.CreatePlan(ctx, params.Caller.User.ID, params.ConversationID, []PlanItem{{
		Tool: "synthetic_change", Input: json.RawMessage(`{}`), Revertible: true,
	}}, false)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	runner := NewRunner(store, backgroundSharedRegistry(t), logger).WithReviewEvidence(func(context.Context, tool.Caller, PlanItem) (ReviewEvidenceFacts, error) {
		return ReviewEvidenceFacts{Targets: json.RawMessage(`[]`)}, nil
	})
	var events []Event
	state := runState{Runner: runner, params: params, logger: logger, sink: func(e Event) { events = append(events, e) }}
	_, end, err := state.reviewPlan(ctx, plan)
	saved := store.detail.Plans[0].Review
	want := []ReviewItem{{ItemID: plan.Items[0].ID, Decision: "needs_human_confirmation", Risk: "high", Reasons: []string{"需要人工核对"}, Source: "model"}}
	if err != nil || end != EndAwaitingConfirmation || saved == nil || saved.Status != "completed" || !reflect.DeepEqual(saved.Items, want) {
		t.Fatalf("审查结果=%+v，结束原因=%s，错误=%v", saved, end, err)
	}
	if len(events) != 3 || events[1].Type != EventReviewFinished || !reflect.DeepEqual(events[1].Review, saved) {
		t.Fatalf("审查事件=%+v", events)
	}
	for _, output := range []struct {
		name  string
		value any
	}{{"保存的审查", saved}, {"事件", events}} {
		raw, err := json.Marshal(output.value)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte(c7ReviewOpaqueContent)) {
			t.Errorf("%s泄漏不透明块内容", output.name)
		}
	}
	if !strings.Contains(logs.String(), "steward.review.finished") || strings.Contains(logs.String(), c7ReviewOpaqueContent) {
		t.Error("审查结束日志缺失或泄漏不透明块内容")
	}
}

func TestC7ReviewCallHonorsShortParentDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	hits := 0
	p := c7ProviderFunc(func(ctx context.Context, r provider.Request) (provider.Stream, error) {
		hits++
		deadline, _ := ctx.Deadline()
		if time.Until(deadline) > 25*time.Millisecond {
			t.Error("父级短期限被覆盖")
		}
		<-ctx.Done()
		return nil, ctx.Err()
	})
	started := time.Now()
	_, _, err := callReview(ctx, p, "model", nil, nil)
	if !errors.Is(err, context.DeadlineExceeded) || hits != 1 || time.Since(started) > time.Second {
		t.Fatal(err, hits, time.Since(started))
	}
}
