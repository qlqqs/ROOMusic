package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

type c10Executor func(context.Context, RunParams, EventSink) error

func (f c10Executor) Run(ctx context.Context, p RunParams, s EventSink) error { return f(ctx, p, s) }
func c10Start(t *testing.T, s *Supervisor, user, id, role string) *Run {
	t.Helper()
	p := runParams(nil)
	p.Caller.User.ID = user
	p.Caller.User.Role = role
	p.ConversationID = id
	r, err := s.Start(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func c10Waiting(ctx context.Context, p RunParams, s EventSink) error {
	p.Started()
	<-ctx.Done()
	s(Event{Type: EventDone, End: &RunEnd{Reason: EndCancelled}})
	return nil
}
func c10Finished(t *testing.T, r *Run, want bool) {
	t.Helper()
	select {
	case <-r.done:
		if !want {
			t.Fatal("运行意外结束")
		}
	default:
		if want {
			t.Fatal("运行未结束")
		}
	}
}

func TestC10SupervisorLimitsAndCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := NewSupervisor(context.Background(), c10Executor(c10Waiting))
		defer s.Shutdown(context.Background())
		a := c10Start(t, s, "a", "a1", "user")
		b := c10Start(t, s, "a", "a2", "user")
		c := c10Start(t, s, "a", "a3", "user")
		p := runParams(nil)
		p.Caller.User.ID = "a"
		p.ConversationID = "a4"
		if _, err := s.Start(context.Background(), p); !errors.Is(err, ErrRunLimit) {
			t.Fatal(err)
		}
		p.ConversationID = "a1"
		if _, err := s.Start(context.Background(), p); !errors.Is(err, ErrConversationBusy) {
			t.Fatal(err)
		}
		other := c10Start(t, s, "b", "b1", "user")
		admin := c10Start(t, s, "admin", "admin1", "admin")
		if !s.Cancel("a1") || s.Cancel("missing") {
			t.Fatal("Cancel 结果错误")
		}
		synctest.Wait()
		c10Finished(t, a, true)
		c10Finished(t, b, false)
		replacement := c10Start(t, s, "a", "a4", "user")
		s.CancelUser("a")
		synctest.Wait()
		for _, r := range []*Run{b, c, replacement} {
			c10Finished(t, r, true)
		}
		c10Finished(t, other, false)
		c10Finished(t, admin, false)
		s.CancelMembers()
		synctest.Wait()
		c10Finished(t, other, true)
		c10Finished(t, admin, false)
	})
}
func TestC10SupervisorDetachedReplayAndSlowSubscriber(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		request, cancel := context.WithCancel(context.WithValue(context.Background(), c10ContextKey{}, "request-value"))
		s := NewSupervisor(context.Background(), c10Executor(func(ctx context.Context, p RunParams, sink EventSink) error {
			if ctx.Value(c10ContextKey{}) != "request-value" {
				t.Error("请求值丢失")
			}
			return c10Waiting(ctx, p, sink)
		}))
		defer s.Shutdown(context.Background())
		p := runParams(nil)
		r, err := s.Start(request, p)
		if err != nil {
			t.Fatal(err)
		}
		s.publish(r, Event{Type: EventText, Step: 1, Text: "甲"})
		first := s.sequence.Load()
		s.publish(r, Event{Type: EventText, Step: 1, Text: "乙"})
		second := s.sequence.Load()
		sub := r.Subscribe(first)
		defer sub.Close()
		if len(sub.Replay) != 1 || sub.Replay[0].Text != "乙" || sub.Replay[0].Seq != second {
			t.Fatal(sub.Replay)
		}
		cancel()
		synctest.Wait()
		c10Finished(t, r, false)
		s.publish(r, Event{Type: EventText, Step: 1, Text: "丙"})
		e := <-sub.Events
		if e.Seq <= second || e.Text != "丙" {
			t.Fatal(e)
		}
		sub.Close()
		c10Finished(t, r, false)
		slow := r.Subscribe(e.Seq)
		for i := 0; i < 70; i++ {
			s.publish(r, Event{Type: EventText, Step: 2, Text: "x"})
		}
		count := 0
		for range slow.Events {
			count++
		}
		if count != 64 {
			t.Fatal(count)
		}
		replay := r.Subscribe(e.Seq)
		defer replay.Close()
		if len(replay.Replay) != 1 || replay.Replay[0].Text != strings.Repeat("x", 70) {
			t.Fatal(replay.Replay)
		}
	})
}

type c10ContextKey struct{}

func TestC10SupervisorReplayLiveJoin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := NewSupervisor(context.Background(), c10Executor(c10Waiting))
		defer s.Shutdown(context.Background())
		r := c10Start(t, s, "a", "c", "user")
		// 并发发布与订阅，以事件序号核对回放和实时队列的原子交接。
		go func() {
			for i := 0; i < 50; i++ {
				s.publish(r, Event{Type: EventUsage, Step: i})
			}
		}()
		sub := r.Subscribe(0)
		synctest.Wait()
		events := append([]Event{}, sub.Replay...)
		sub.Close()
		for e := range sub.Events {
			events = append(events, e)
		}
		if len(events) != 50 {
			t.Fatal(len(events))
		}
		for i, e := range events {
			if e.Seq <= uint64(time.Now().UnixMicro()) || e.Step != i || (i > 0 && e.Seq <= events[i-1].Seq) {
				t.Fatal(i, e)
			}
		}
	})
}
func TestC10SupervisorRetentionAndReplacement(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := NewSupervisor(context.Background(), c10Executor(c10Waiting))
		defer s.Shutdown(context.Background())
		r := c10Start(t, s, "a", "c", "user")
		s.Cancel("c")
		synctest.Wait()
		time.Sleep(59 * time.Second)
		sub := s.Subscribe("c", 0)
		if sub == nil || len(sub.Replay) != 1 {
			t.Fatal("日志提前释放")
		}
		sub.Close()
		time.Sleep(time.Second)
		synctest.Wait()
		if s.Subscribe("c", 0) != nil || len(r.log.entries) != 0 {
			t.Fatal("60 秒后未释放")
		}
		old := c10Start(t, s, "a", "c", "user")
		s.Cancel("c")
		synctest.Wait()
		newer := c10Start(t, s, "a", "c", "user")
		time.Sleep(EventRetention)
		if s.runs["c"] != newer || old == newer {
			t.Fatal("旧定时器移除了新运行")
		}
	})
}
func TestC10SupervisorShutdownBoundAndLifecycle(t *testing.T) {
	t.Run("deadline", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			release := make(chan struct{})
			s := NewSupervisor(context.Background(), c10Executor(func(ctx context.Context, p RunParams, _ EventSink) error { p.Started(); <-release; return nil }))
			c10Start(t, s, "a", "c", "user")
			start := time.Now()
			if err := s.Shutdown(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
			if time.Since(start) != 10*time.Second {
				t.Fatal(time.Since(start))
			}
			if _, err := s.Start(context.Background(), runParams(nil)); !errors.Is(err, ErrSupervisorClosed) {
				t.Fatal(err)
			}
			close(release)
			synctest.Wait()
		})
	})
	t.Run("lifecycle", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			s := NewSupervisor(ctx, c10Executor(c10Waiting))
			r := c10Start(t, s, "a", "c", "user")
			cancel()
			synctest.Wait()
			c10Finished(t, r, true)
			if err := s.Shutdown(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	})
}
func TestC10SupervisorPeriodicCredentialCheck(t *testing.T) {
	for _, databaseError := range []bool{false, true} {
		t.Run(fmt.Sprint(databaseError), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := NewSupervisor(context.Background(), c10Executor(c10Waiting))
				defer s.Shutdown(context.Background())
				checks := 0
				p := runParams(nil)
				p.Check = func(ctx context.Context) error {
					checks++
					if checks == 1 {
						return nil
					}
					deadline, ok := ctx.Deadline()
					if !ok || time.Until(deadline) != 5*time.Second {
						t.Error("复查缺少 5 秒期限")
					}
					if databaseError {
						return errors.New("合成数据库错误")
					}
					return ErrRunInvalidated
				}
				r, err := s.Start(context.Background(), p)
				if err != nil {
					t.Fatal(err)
				}
				sibling := c10Start(t, s, p.Caller.User.ID, "sibling", "user")
				time.Sleep(60 * time.Second)
				synctest.Wait()
				if checks != 2 {
					t.Fatal(checks)
				}
				c10Finished(t, r, !databaseError)
				c10Finished(t, sibling, !databaseError)
			})
		})
	}
}
func TestC10EventLogBudgetAndSuffix(t *testing.T) {
	var log eventLog
	for i, text := range []string{"甲", "<&", "🙂"} {
		log.append(Event{Type: EventText, Seq: uint64(i + 1), Step: 1, Text: text})
	}
	for after, want := range map[uint64]string{0: "甲<&🙂", 1: "<&🙂", 2: "🙂", 3: ""} {
		events := log.after(after)
		if want == "" {
			if len(events) != 0 {
				t.Fatal(events)
			}
		} else if len(events) != 1 || events[0].Text != want {
			t.Fatal(after, events)
		}
	}
	log.append(Event{Type: EventText, Seq: 4, Step: 2, Text: strings.Repeat("x", EventLogLimit)})
	if log.bytes > EventLogLimit {
		t.Fatal(log.bytes)
	}
	events := log.after(0)
	if len(events) != 1 || events[0].Type != EventHistoryGap || events[0].Seq != 4 {
		t.Fatal(events)
	}
	log.append(Event{Type: EventDone, Seq: 5})
	events = log.after(4)
	if len(events) != 1 || events[0].Type != EventDone {
		t.Fatal(events)
	}
	log.append(Event{Type: EventToolFinished, Seq: 6, Text: strings.Repeat("x", EventLogLimit)})
	if log.bytes > EventLogLimit || log.omitted != 6 {
		t.Fatal("非文本载荷绕过预算")
	}
}
