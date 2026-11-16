package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/steward/memory"
	"github.com/qlqq/roomusic/backend/internal/steward/provider"
)

func TestC10F2WholeMemoryBudget(t *testing.T) {
	for _, extra := range []int{0, 1} {
		t.Run(fmt.Sprint(extra), func(t *testing.T) {
			items := []memory.Item{}
			remaining := 8000 - utf8.RuneCountInString(memoryPromptHeader+sourceEnd)
			content := strings.Repeat("<&>字🙂", 100)
			for remaining > utf8.RuneCountInString(escapeSource(content))+3 {
				items = append(items, memory.Item{Content: content})
				remaining -= utf8.RuneCountInString(escapeSource(content)) + 3
			}
			// 最后一条同样遵守 500 字限制，转义开销用于精确填满余量。
			tail := strings.Repeat("&", (remaining-3)/5) + strings.Repeat("字", (remaining-3)%5) + strings.Repeat("字", extra)
			items = append(items, memory.Item{Content: tail}, memory.Item{Content: "不应挑选"})
			got := memoryPrompt(items)
			want := memoryPromptHeader
			for _, item := range items[:len(items)-2] {
				want += "- " + escapeSource(item.Content) + "\n"
			}
			if extra == 0 {
				want += "- " + escapeSource(tail) + "\n"
			}
			want += sourceEnd
			if got != want || utf8.RuneCountInString(got) > 8000 || (extra == 0 && utf8.RuneCountInString(got) != 8000) {
				t.Fatal("整条预算边界错误", utf8.RuneCountInString(got), remaining)
			}
		})
	}
}

func TestC10F3PreflightRollback(t *testing.T) {
	for _, mode := range []string{"check", "executor", "nothing_to_continue", "expired_previous"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				fail := false
				s := NewSupervisor(context.Background(), c10Executor(func(ctx context.Context, p RunParams, sink EventSink) error {
					if fail {
						if mode == "nothing_to_continue" {
							return ErrNothingToContinue
						}
						return syntheticStoreError
					}
					return c10Waiting(ctx, p, sink)
				}))
				defer s.Shutdown(context.Background())
				old := c10Start(t, s, "a", "c", "user")
				s.Cancel("c")
				synctest.Wait()
				oldSub := old.Subscribe(0)
				oldSeq := oldSub.Replay[0].Seq
				oldSub.Close()
				time.Sleep(30 * time.Second)
				fail = true
				p := runParams(nil)
				p.Caller.User.ID = "a"
				p.ConversationID = "c"
				entered, release := make(chan struct{}), make(chan struct{})
				p.Check = func(context.Context) error {
					close(entered)
					<-release
					if mode == "check" || mode == "expired_previous" {
						return syntheticStoreError
					}
					return nil
				}
				result := make(chan error, 1)
				go func() { _, err := s.Start(context.Background(), p); result <- err }()
				<-entered
				s.mu.Lock()
				failed := s.runs["c"]
				s.mu.Unlock()
				sub := failed.Subscribe(0)
				if mode == "expired_previous" {
					time.Sleep(31 * time.Second)
					synctest.Wait()
				}
				close(release)
				if err := <-result; err == nil {
					t.Fatal("前置失败未返回")
				}
				synctest.Wait()
				if len(sub.Replay) != 0 {
					t.Fatal(sub.Replay)
				}
				for e := range sub.Events {
					t.Fatal("前置失败发布事件", e)
				}
				if len(failed.log.entries) != 0 {
					t.Fatal("失败日志未清除")
				}
				c10Finished(t, failed, true)
				restored := s.Subscribe("c", 0)
				if mode == "expired_previous" {
					if restored != nil {
						restored.Close()
						t.Fatal("已过期日志恢复")
					}
				} else {
					if restored == nil || len(restored.Replay) != 1 || restored.Replay[0].Seq != oldSeq {
						t.Fatal("旧日志未恢复")
					}
					restored.Close()
				}
				fail = false
				// 新运行仍可占满三个名额，旧定时器不会删除新运行。
				newer := c10Start(t, s, "a", "c", "user")
				c10Start(t, s, "a", "c2", "user")
				c10Start(t, s, "a", "c3", "user")
				time.Sleep(31 * time.Second)
				synctest.Wait()
				if s.runs["c"] != newer {
					t.Fatal("旧计时器删除新运行")
				}
			})
		})
	}
}

func TestC10F3RestoredRetentionNotExtended(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fail := false
		s := NewSupervisor(context.Background(), c10Executor(func(ctx context.Context, p RunParams, sink EventSink) error {
			if fail {
				return ErrNothingToContinue
			}
			return c10Waiting(ctx, p, sink)
		}))
		defer s.Shutdown(context.Background())
		c10Start(t, s, "a", "c", "user")
		s.Cancel("c")
		synctest.Wait()
		time.Sleep(59 * time.Second)
		fail = true
		p := runParams(nil)
		p.Caller.User.ID = "a"
		p.ConversationID = "c"
		if _, err := s.Start(context.Background(), p); !errors.Is(err, ErrNothingToContinue) {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if sub := s.Subscribe("c", 0); sub != nil {
			sub.Close()
			t.Fatal("恢复延长保留期")
		}
	})
}

func TestC10F4RestartCursor(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var previous uint64
		for i := 0; i < 2; i++ {
			base := uint64(time.Now().UnixMicro())
			s := NewSupervisor(context.Background(), c10Executor(c10Waiting))
			r := c10Start(t, s, "a", "c", "user")
			s.publish(r, Event{Type: EventText, Text: "字"})
			sub := r.Subscribe(0)
			seq := sub.Replay[0].Seq
			sub.Close()
			parsed, err := strconv.ParseUint(strconv.FormatUint(seq, 10), 10, 64)
			if err != nil || parsed != seq || seq <= base || seq <= previous || seq > 1<<53-1 {
				t.Fatal(seq, base, previous)
			}
			previous = seq
			s.Shutdown(context.Background())
			time.Sleep(time.Second)
		}
	})
}

func TestC10F5ChunkSuffixAndSnapshot(t *testing.T) {
	var log eventLog
	delta := strings.Repeat("字🙂", 100)
	const count = 10000
	for i := 1; i <= count; i++ {
		log.append(Event{Type: EventText, Seq: uint64(i), Step: 1, Text: delta})
	}
	all := log.after(0)
	if len(all) < 2 || all[0].Type != EventHistoryGap || all[0].Seq == 0 || all[0].Seq >= count || log.bytes > EventLogLimit {
		t.Fatal("未保留新后缀", len(all), log.bytes)
	}
	join := func(events []Event) string {
		var b strings.Builder
		for _, e := range events {
			if e.Type == EventText {
				b.WriteString(e.Text)
			}
		}
		return b.String()
	}
	if got := join(all); got != strings.Repeat(delta, count-int(all[0].Seq)) {
		t.Fatal("history_gap 游标与后缀不一致")
	}
	for _, after := range []uint64{all[0].Seq, all[1].Seq - 1, all[1].Seq, all[1].Seq + 1, count - 1, count} {
		if got := join(log.after(after)); got != strings.Repeat(delta, count-int(after)) || !utf8.ValidString(got) {
			t.Fatal("跨块游标错误", after)
		}
	}
	snapshot := log.after(count - 2)
	before := join(snapshot)
	log.append(Event{Type: EventText, Seq: count + 1, Step: 1, Text: "新"})
	if join(snapshot) != before {
		t.Fatal("快照被后续写入修改")
	}
	var tiny eventLog
	for i := 1; i <= 2050; i++ {
		tiny.append(Event{Type: EventText, Seq: uint64(i), Step: 1, Text: "🙂"})
	}
	if len(tiny.after(0)) < 3 || join(tiny.after(1023)) != strings.Repeat("🙂", 1027) {
		t.Fatal("增量边界分块错误")
	}
	log.append(Event{Type: EventToolFinished, Seq: count + 2, Text: strings.Repeat("x", EventLogLimit)})
	if log.omitted != count+2 || log.bytes > EventLogLimit {
		t.Fatal("非文本预算错误")
	}
}

func TestC10F5IndependentRunLocks(t *testing.T) {
	s := NewSupervisor(context.Background(), c10Executor(c10Waiting))
	defer s.Shutdown(context.Background())
	a := c10Start(t, s, "a", "a", "user")
	b := c10Start(t, s, "b", "b", "user")
	a.mu.Lock()
	defer a.mu.Unlock()
	blocked := make(chan struct{})
	go func() { s.publish(a, Event{Type: EventText, Text: "甲"}); close(blocked) }()
	done := make(chan struct{})
	go func() {
		s.publish(b, Event{Type: EventText, Text: "乙"})
		sub := s.Subscribe("b", 0)
		sub.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Error("另一运行发布或订阅被阻塞")
	}
	select {
	case <-blocked:
		t.Error("锁夹具未阻塞发布")
	default:
	}
}

type c10FailingMemories struct{}

func (c10FailingMemories) List(context.Context, string) (memory.List, error) {
	return memory.List{Items: []memory.Item{{Content: "合成敏感记忆"}}}, errors.New("合成敏感凭据错误原文")
}
func TestC10F8MemoryUnavailable(t *testing.T) {
	var logs bytes.Buffer
	p := &fakeProvider{next: func(_ context.Context, _ int, r provider.Request) ([]provider.Event, error) {
		if strings.Contains(r.System, "user_memory") || strings.Contains(r.System, "合成敏感") {
			t.Error("注入失败记忆")
		}
		return []provider.Event{modelDone(provider.FinishNormal)}, nil
	}}
	if err := NewRunner(&memoryRunStore{}, runtimeRegistry(t), slog.New(slog.NewJSONHandler(&logs, nil))).WithMemories(c10FailingMemories{}).Run(context.Background(), runParams(p), func(Event) {}); err != nil {
		t.Fatal(err)
	}
	if len(p.requests) != 1 || strings.Count(logs.String(), `"event":"steward.memory.unavailable"`) != 1 || !strings.Contains(logs.String(), `"level":"WARN"`) || strings.Contains(logs.String(), "合成敏感") {
		t.Fatal(logs.String())
	}
}

func TestC10F10FinishedCursor(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := NewSupervisor(context.Background(), c10Executor(c10Waiting))
		defer s.Shutdown(context.Background())
		r := c10Start(t, s, "a", "c", "user")
		s.publish(r, Event{Type: EventText, Text: "甲"})
		seq := s.sequence.Load()
		live := r.Subscribe(seq)
		if live == nil || len(live.Replay) != 0 {
			t.Fatal("运行中末尾未建立订阅")
		}
		live.Close()
		s.Cancel("c")
		synctest.Wait()
		last := s.sequence.Load()
		for _, after := range []uint64{last, last + 1} {
			if sub := s.Subscribe("c", after); sub != nil {
				sub.Close()
				t.Fatal("末尾仍重放")
			}
		}
		sub := s.Subscribe("c", seq)
		defer sub.Close()
		if len(sub.Replay) != 1 || sub.Replay[0].Type != EventDone {
			t.Fatal(sub.Replay)
		}
	})
}

func TestC10F3PreflightWithoutPrevious(t *testing.T) {
	s := NewSupervisor(context.Background(), c10Executor(func(context.Context, RunParams, EventSink) error { return ErrNothingToContinue }))
	defer s.Shutdown(context.Background())
	for i := 0; i < 4; i++ {
		p := runParams(nil)
		if _, err := s.Start(context.Background(), p); !errors.Is(err, ErrNothingToContinue) {
			t.Fatal(err)
		}
		if sub := s.Subscribe(p.ConversationID, 0); sub != nil {
			sub.Close()
			t.Fatal("前置失败创建新日志")
		}
	}
}

type c10CompletionFixture struct {
	memoryRunStore
	finished int
}

func (s *c10CompletionFixture) FinishConversation(ctx context.Context, u, id string) error {
	s.finished++
	return s.memoryRunStore.ReleaseConversation(ctx, u, id)
}
func TestC10F9CompletionStoreDispatch(t *testing.T) {
	for _, kind := range []string{"nothing", "query", "complete"} {
		t.Run(kind, func(t *testing.T) {
			store := &c10CompletionFixture{}
			p := runParams(&fakeProvider{next: func(context.Context, int, provider.Request) ([]provider.Event, error) {
				return []provider.Event{modelDone(provider.FinishNormal)}, nil
			}})
			if kind == "nothing" {
				p.Continue = true
			}
			if kind == "query" {
				store.fail = "get"
			}
			err := NewRunner(store, runtimeRegistry(t), slog.Default()).Run(context.Background(), p, func(Event) {})
			if kind == "nothing" && !errors.Is(err, ErrNothingToContinue) || kind == "query" && !errors.Is(err, syntheticStoreError) || kind == "complete" && err != nil {
				t.Fatal(err)
			}
			want := 0
			if kind == "complete" {
				want = 1
			}
			if store.finished != want || store.released != 1 || store.busy {
				t.Fatal(store.finished, store.released, store.busy)
			}
		})
	}
}
