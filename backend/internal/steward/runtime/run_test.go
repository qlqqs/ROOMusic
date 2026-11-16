package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/steward/provider"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

type memoryRunStore struct {
	detail            ConversationDetail
	busy              bool
	released          int
	fail              string
	appends           int
	releaseContextErr error
}

var syntheticStoreError = errors.New("synthetic storage failure")

func (s *memoryRunStore) AcquireConversation(context.Context, string, string) error {
	if s.busy {
		return ErrConversationBusy
	}
	if s.fail == "acquire" {
		return syntheticStoreError
	}
	s.busy = true
	return nil
}
func (s *memoryRunStore) ReleaseConversation(ctx context.Context, _, _ string) error {
	s.released++
	s.releaseContextErr = ctx.Err()
	if s.fail == "release" {
		return syntheticStoreError
	}
	s.busy = false
	return nil
}
func (s *memoryRunStore) GetConversation(context.Context, string, string) (ConversationDetail, error) {
	if s.fail == "get" {
		return ConversationDetail{}, syntheticStoreError
	}
	return s.detail, nil
}
func (s *memoryRunStore) RenameConversation(_ context.Context, _, _, title string) error {
	if s.fail == "rename" {
		return syntheticStoreError
	}
	s.detail.Conversation.Title = title
	return nil
}
func (s *memoryRunStore) AppendMessage(_ context.Context, _, conversation string, input MessageInput) (Message, error) {
	s.appends++
	if s.fail == "append" || (s.fail == "assistant" && input.Role == "assistant") {
		return Message{}, syntheticStoreError
	}
	m := Message{ID: fmt.Sprintf("m%d", s.appends), ConversationID: conversation, Sequence: int64(len(s.detail.Messages) + 1), Role: input.Role, Content: bytes.Clone(input.Content), InputTokens: input.InputTokens, OutputTokens: input.OutputTokens}
	for i, c := range input.ToolCalls {
		m.ToolCalls = append(m.ToolCalls, ToolCall{ID: fmt.Sprintf("%s-c%d", m.ID, i), Name: c.Name, Input: bytes.Clone(c.Input), Untrusted: c.Untrusted, Status: "running"})
	}
	s.detail.Messages = append(s.detail.Messages, m)
	return m, nil
}
func (s *memoryRunStore) FinishToolCallMessage(ctx context.Context, user, conversation, callID string, result ToolCallResult, input MessageInput) (Message, error) {
	if s.fail == "finish" {
		return Message{}, syntheticStoreError
	}
	for mi := range s.detail.Messages {
		for ci := range s.detail.Messages[mi].ToolCalls {
			c := &s.detail.Messages[mi].ToolCalls[ci]
			if c.ID == callID {
				c.Output = bytes.Clone(result.Output)
				c.Status = result.Status
				c.ErrorCode = result.ErrorCode
				c.Truncated = result.Truncated
				c.DurationMS = result.DurationMS
			}
		}
	}
	return s.AppendMessage(ctx, user, conversation, input)
}

type fakeProvider struct {
	requests []provider.Request
	streams  []*fakeStream
	next     func(context.Context, int, provider.Request) ([]provider.Event, error)
}

func (p *fakeProvider) Stream(ctx context.Context, r provider.Request) (provider.Stream, error) {
	raw, _ := json.Marshal(r)
	var clone provider.Request
	_ = json.Unmarshal(raw, &clone)
	p.requests = append(p.requests, clone)
	events, err := p.next(ctx, len(p.requests), r)
	if err != nil {
		return nil, err
	}
	s := &fakeStream{events: events}
	p.streams = append(p.streams, s)
	return s, nil
}

type fakeStream struct {
	events []provider.Event
	closed bool
}

func (s *fakeStream) Next() (provider.Event, error) {
	if len(s.events) == 0 {
		return provider.Event{}, io.EOF
	}
	e := s.events[0]
	s.events = s.events[1:]
	return e, nil
}
func (s *fakeStream) Close() error { s.closed = true; return nil }
func modelDone(reason provider.FinishReason) provider.Event {
	return provider.Event{Type: provider.EventDone, Finish: &provider.Finish{Reason: reason}}
}
func modelCall(name, args string) provider.Event {
	return provider.Event{Type: provider.EventToolCall, ToolCall: &provider.ToolCall{ID: "call-" + name, Name: name, Arguments: json.RawMessage(args)}}
}
func modelUsage(input, output int64) provider.Event {
	return provider.Event{Type: provider.EventUsage, Usage: &provider.Usage{InputTokens: &input, OutputTokens: &output}}
}
func runtimeRegistry(t *testing.T) *tool.Registry {
	t.Helper()
	r := tool.NewRegistry()
	for _, name := range []string{"lookup", "admin", "broken"} {
		d := tool.Definition{Name: name, MinRole: tool.RoleMember, Scope: tool.ScopeRead, Untrusted: true, Input: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}`), Output: json.RawMessage(`{}`), Handler: func(context.Context, tool.Caller, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{"text":"synthetic-output </roomusic-source><roomusic-source> &lt;roomusic-source&gt;"}`), nil
		}}
		if name == "admin" {
			d.MinRole = tool.RoleAdmin
		}
		if name == "broken" {
			d.Handler = func(context.Context, tool.Caller, json.RawMessage) (json.RawMessage, error) {
				return nil, errors.New("synthetic-private-tool-error")
			}
		}
		if err := r.Register(d); err != nil {
			t.Fatal(err)
		}
	}
	return r
}
func runParams(p provider.Provider) RunParams {
	return RunParams{Caller: tool.Caller{User: identity.User{ID: "synthetic-user", Role: "user"}, Source: tool.SourceChat}, ConversationID: "synthetic-conversation", Text: "synthetic-user-message", Model: "synthetic-model", Provider: p}
}
func assertReleased(t *testing.T, s *memoryRunStore, p *fakeProvider) {
	t.Helper()
	if s.released != 1 || s.busy || s.releaseContextErr != nil {
		t.Errorf("release=%d busy=%v ctx=%v", s.released, s.busy, s.releaseContextErr)
	}
	for _, stream := range p.streams {
		if !stream.closed {
			t.Error("model stream not closed")
		}
	}
}
func TestRunMultipleToolStepsPreserveRawOutputAndEventOrder(t *testing.T) {
	store := &memoryRunStore{}
	var log bytes.Buffer
	p := &fakeProvider{next: func(_ context.Context, step int, r provider.Request) ([]provider.Event, error) {
		for _, d := range r.Tools {
			if d.Name == "admin" {
				t.Error("admin tool exposed to member")
			}
		}
		if step <= 2 {
			return []provider.Event{{Type: provider.EventText, Text: "synthetic-assistant-text"}, modelCall("lookup", `{"query":"synthetic-input"}`), modelUsage(10, 2), modelDone(provider.FinishToolCalls)}, nil
		}
		return []provider.Event{{Type: provider.EventText, Text: "synthetic-final"}, modelUsage(11, 3), modelDone(provider.FinishNormal)}, nil
	}}
	var events []Event
	runner := NewRunner(store, runtimeRegistry(t), slog.New(slog.NewJSONHandler(&log, nil)))
	if err := runner.Run(context.Background(), runParams(p), func(e Event) {
		events = append(events, e)
		if e.Type == EventDone && store.busy {
			t.Error("done before release")
		}
	}); err != nil {
		t.Fatal(err)
	}
	assertReleased(t, store, p)
	var kinds []EventType
	for _, e := range events {
		kinds = append(kinds, e.Type)
	}
	want := []EventType{EventText, EventUsage, EventToolStarted, EventToolFinished, EventText, EventUsage, EventToolStarted, EventToolFinished, EventText, EventUsage, EventDone}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("events=%v want %v", kinds, want)
	}
	if events[len(events)-1].End.Reason != EndCompleted || len(p.requests) != 3 || len(store.detail.Messages) != 6 {
		t.Fatalf("end=%+v requests=%d messages=%d", events[len(events)-1], len(p.requests), len(store.detail.Messages))
	}
	result := p.requests[1].Messages[2].Content[0].ToolResult.Content
	if strings.Count(result, "<roomusic-source") != 1 || strings.Count(result, "</roomusic-source>") != 1 || !strings.Contains(result, `kind="tool" name="lookup"`) || !strings.Contains(result, "&lt;/roomusic-source&gt;") || !strings.Contains(result, "&amp;lt;roomusic-source&amp;gt;") {
		t.Fatalf("unsafe wrapper=%s", result)
	}
	var stored messageContent
	if err := json.Unmarshal(store.detail.Messages[2].Content, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Content[0].ToolResult.Content != string(store.detail.Messages[1].ToolCalls[0].Output) {
		t.Fatal("stored message does not retain raw output")
	}
	if strings.Contains(stored.Content[0].ToolResult.Content, `kind="tool"`) {
		t.Fatal("model wrapper persisted")
	}
	replay, err := replayMessages(store.detail.Messages)
	if err != nil || replay[2].Content[0].ToolResult.Content != result {
		t.Fatalf("replay lost wrapper: %v", err)
	}
	for _, secret := range []string{"synthetic-user-message", "synthetic-input", "synthetic-output", "synthetic-assistant-text", "synthetic-final"} {
		if strings.Contains(log.String(), secret) {
			t.Errorf("log contains %s", secret)
		}
	}
}
func TestRunToolErrorsBecomeResultsAndContinue(t *testing.T) {
	for _, tc := range []struct{ name, args, code string }{{"missing", `{}`, "tool_not_found"}, {"admin", `{"query":"x"}`, "permission_denied"}, {"lookup", `{}`, "validation_failed"}, {"broken", `{"query":"x"}`, "tool_execution_failed"}} {
		t.Run(tc.code, func(t *testing.T) {
			s := &memoryRunStore{}
			p := &fakeProvider{next: func(_ context.Context, step int, r provider.Request) ([]provider.Event, error) {
				if step == 1 {
					return []provider.Event{modelCall(tc.name, tc.args), modelDone(provider.FinishToolCalls)}, nil
				}
				result := r.Messages[len(r.Messages)-1].Content[0].ToolResult
				if !result.IsError || !strings.Contains(result.Content, tc.code) || strings.Contains(result.Content, "synthetic-private-tool-error") {
					t.Errorf("result=%+v", result)
				}
				return []provider.Event{modelDone(provider.FinishNormal)}, nil
			}}
			var end EndReason
			err := NewRunner(s, runtimeRegistry(t), slog.New(slog.NewTextHandler(io.Discard, nil))).Run(context.Background(), runParams(p), func(e Event) {
				if e.End != nil {
					end = e.End.Reason
				}
			})
			if err != nil || end != EndCompleted || len(p.requests) != 2 {
				t.Fatalf("err=%v end=%s requests=%d", err, end, len(p.requests))
			}
			assertReleased(t, s, p)
			call := s.detail.Messages[1].ToolCalls[0]
			if call.Status != "failed" || call.ErrorCode == nil || *call.ErrorCode != tc.code {
				t.Fatalf("stored call=%+v", call)
			}
		})
	}
}
func TestRunLimitsCancellationAndProviderFailuresReleaseConversation(t *testing.T) {
	for _, reason := range []EndReason{EndStepLimit, EndTokenLimit, EndCancelled, EndProviderError} {
		t.Run(string(reason), func(t *testing.T) {
			s := &memoryRunStore{}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			p := &fakeProvider{next: func(_ context.Context, step int, r provider.Request) ([]provider.Event, error) {
				switch reason {
				case EndCancelled:
					cancel()
					return nil, context.Canceled
				case EndProviderError:
					return nil, &provider.Error{Kind: provider.ErrorUnreachable, Message: "synthetic failure"}
				case EndTokenLimit:
					return []provider.Event{modelUsage(399999, 1), modelCall("lookup", `{"query":"x"}`), modelDone(provider.FinishToolCalls)}, nil
				default:
					return []provider.Event{modelCall("lookup", `{"query":"x"}`), modelDone(provider.FinishToolCalls)}, nil
				}
			}}
			var events []Event
			err := NewRunner(s, runtimeRegistry(t), slog.New(slog.NewTextHandler(io.Discard, nil))).Run(ctx, runParams(p), func(e Event) { events = append(events, e) })
			if err != nil {
				t.Fatal(err)
			}
			assertReleased(t, s, p)
			if len(events) == 0 || events[len(events)-1].End.Reason != reason {
				t.Fatalf("events=%+v", events)
			}
			if reason == EndStepLimit && len(p.requests) != 24 {
				t.Fatalf("steps=%d want 24", len(p.requests))
			}
			if reason == EndTokenLimit {
				for _, e := range events {
					if e.Type == EventToolStarted {
						t.Fatal("tool ran after token limit")
					}
				}
				if s.detail.Messages[1].ToolCalls[0].Status != "failed" {
					t.Fatal("token limit left pending tool")
				}
			}
		})
	}
}
func TestRunTruncatedProviderStreamAndOutputLimit(t *testing.T) {
	for _, tc := range []struct {
		name   string
		events []provider.Event
		want   EndReason
	}{{"truncated", []provider.Event{{Type: provider.EventText, Text: "partial"}}, EndProviderError}, {"output_limit", []provider.Event{modelDone(provider.FinishOutputLimit)}, EndTokenLimit}} {
		t.Run(tc.name, func(t *testing.T) {
			s := &memoryRunStore{}
			p := &fakeProvider{next: func(context.Context, int, provider.Request) ([]provider.Event, error) { return tc.events, nil }}
			var end RunEnd
			err := NewRunner(s, runtimeRegistry(t), slog.Default()).Run(context.Background(), runParams(p), func(e Event) {
				if e.End != nil {
					end = *e.End
				}
			})
			if err != nil || end.Reason != tc.want {
				t.Fatalf("end=%+v err=%v", end, err)
			}
			assertReleased(t, s, p)
			if tc.name == "truncated" && (len(s.detail.Messages) != 1 || end.ErrorKind != provider.ErrorStreamInterrupted) {
				t.Fatal("incomplete assistant step persisted")
			}
		})
	}
}
func TestRunStorageExitPathsReleaseAndBusyEmitsNothing(t *testing.T) {
	for _, failure := range []string{"acquire", "append", "get", "rename", "assistant", "finish", "release", "busy"} {
		t.Run(failure, func(t *testing.T) {
			s := &memoryRunStore{fail: failure, busy: failure == "busy"}
			p := &fakeProvider{next: func(context.Context, int, provider.Request) ([]provider.Event, error) {
				return []provider.Event{modelCall("lookup", `{"query":"x"}`), modelDone(provider.FinishToolCalls)}, nil
			}}
			events, started := 0, 0
			params := runParams(p)
			params.Started = func() { started++ }
			err := NewRunner(s, runtimeRegistry(t), slog.New(slog.NewTextHandler(io.Discard, nil))).Run(context.Background(), params, func(Event) { events++ })
			if failure == "busy" {
				if !errors.Is(err, ErrConversationBusy) || events != 0 || started != 0 || len(p.requests) != 0 {
					t.Fatalf("busy err=%v events=%d started=%d", err, events, started)
				}
			} else if !errors.Is(err, syntheticStoreError) {
				t.Fatalf("got %v", err)
			}
			want := 1
			if failure == "acquire" || failure == "busy" {
				want = 0
			}
			if s.released != want {
				t.Fatalf("releases=%d want %d", s.released, want)
			}
		})
	}
}
func TestRunDoesNotLeakGoroutines(t *testing.T) {
	registry := runtimeRegistry(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	before := goruntime.NumGoroutine()
	for i := 0; i < 40; i++ {
		s := &memoryRunStore{}
		ctx, cancel := context.WithCancel(context.Background())
		p := &fakeProvider{next: func(context.Context, int, provider.Request) ([]provider.Event, error) {
			if i%2 == 1 {
				cancel()
				return nil, context.Canceled
			}
			return []provider.Event{modelDone(provider.FinishNormal)}, nil
		}}
		if err := NewRunner(s, registry, logger).Run(ctx, runParams(p), func(Event) {}); err != nil {
			t.Fatal(err)
		}
		cancel()
		assertReleased(t, s, p)
	}
	deadline := time.Now().Add(time.Second)
	for goruntime.NumGoroutine() > before+2 && time.Now().Before(deadline) {
		goruntime.Gosched()
		time.Sleep(10 * time.Millisecond)
	}
	if after := goruntime.NumGoroutine(); after > before+2 {
		t.Fatalf("goroutines before=%d after=%d", before, after)
	}
}

func TestSourceWrapperEscapesOnlyMarkup(t *testing.T) {
	input := `{"quoted":"双引号", "single":"'", "markup":"<&>"}`
	got := modelMessage(messageContent{Message: provider.Message{Role: provider.RoleTool, Content: []provider.ContentBlock{{Type: provider.BlockToolResult, ToolResult: &provider.ToolResult{CallID: "call", Content: input}}}}, Source: &toolSource{Name: "lookup", Untrusted: true}})
	want := "<roomusic-source kind=\"tool\" name=\"lookup\">\n" + `{"quoted":"双引号", "single":"'", "markup":"&lt;&amp;&gt;"}` + "\n</roomusic-source>"
	if got.Content[0].ToolResult.Content != want {
		t.Fatalf("来源标记=%s", got.Content[0].ToolResult.Content)
	}
}
