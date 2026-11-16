package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/steward/provider"
)

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
	for _, name := range []string{"valid", "text", "opaque", "wrong_tool", "multiple_calls", "no_call", "wrong_finish", "output_limit", "content_filter", "nil_finish", "nil_call", "nil_usage", "truncated", "close_error", "stream_error"} {
		t.Run(name, func(t *testing.T) {
			call := modelCall("submit_review", `{"items":[{"item_id":"a","decision":"approved","risk":"low","reasons":["证据充分"]}]}`)
			events := []provider.Event{call, modelUsage(12, 4), modelDone(provider.FinishToolCalls)}
			stream := &c7ReviewStream{}
			switch name {
			case "text":
				events = append([]provider.Event{{Type: provider.EventText, Text: "批准"}}, events...)
			case "opaque":
				events = append([]provider.Event{{Type: provider.EventOpaque}}, events...)
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
			if name == "valid" || name == "wrong_finish" {
				if err != nil || len(results) != 1 || usage.InputTokens == nil || *usage.InputTokens != 12 {
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
