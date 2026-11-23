package runtime

import (
	"context"
	"errors"
	"log/slog"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"
)

const (
	MaxUserRuns     = 3
	EventLogLimit   = 4 << 20
	EventRetention  = 60 * time.Second
	ShutdownTimeout = 10 * time.Second
)

var (
	ErrBackgroundBusy   = errors.New("已有后台整理正在运行")
	ErrRunStopped       = errors.New("已停止自动整理")
	ErrRunLimit         = errors.New("同时运行的会话已达上限")
	ErrSupervisorClosed = errors.New("管家正在关闭")
	ErrRunInvalidated   = errors.New("运行凭据已变化")
)

// RunExecutor 是 Supervisor 消费的运行入口，便于用独立执行器验证生命周期。
type RunExecutor interface {
	Run(context.Context, RunParams, EventSink) error
}

// Supervisor 拥有进程内运行、事件日志与订阅；请求只提供值，不拥有取消权。
type Supervisor struct {
	mu       sync.Mutex
	ctx      context.Context
	runner   RunExecutor
	runs     map[string]*Run
	sequence atomic.Uint64
	closed   bool
	wg       sync.WaitGroup
	logger   *slog.Logger
}

// Run 的日志与订阅由 mu 保护；finished 在写入时同时持有 Supervisor 和 Run 锁。
type Run struct {
	mu             sync.Mutex
	retainedUntil  time.Time // 由 Supervisor 锁保护。
	supervisor     *Supervisor
	ConversationID string
	userID         string
	member         bool
	background     bool
	end            RunEnd
	err            error
	cancel         context.CancelFunc
	done           chan struct{}
	finished       bool
	log            eventLog
	subscribers    map[*Subscription]struct{}
}

// Subscription 先消费 Replay，再消费 Events；Close 只退订，不取消运行。
type Subscription struct {
	Replay []Event
	Events <-chan Event
	events chan Event
	close  func()
}

func (s *Subscription) Close() { s.close() }

func NewSupervisor(ctx context.Context, runner RunExecutor, loggers ...*slog.Logger) *Supervisor {
	logger := slog.Default()
	if len(loggers) > 0 && loggers[0] != nil {
		logger = loggers[0]
	}
	s := &Supervisor{ctx: ctx, runner: runner, logger: logger, runs: make(map[string]*Run)}
	// Unix 微秒基数在当前时间范围内可由 JavaScript 安全整数精确表达。
	s.sequence.Store(uint64(time.Now().UnixMicro()))
	return s
}

// Start 在运行完成原子占用及前置校验后返回；后续执行不依赖请求取消。
func (s *Supervisor) Start(request context.Context, params RunParams) (*Run, error) {
	s.mu.Lock()
	if s.closed || s.ctx.Err() != nil {
		s.mu.Unlock()
		return nil, ErrSupervisorClosed
	}
	previous := s.runs[params.ConversationID]
	if previous != nil && !previous.finished {
		s.mu.Unlock()
		return nil, ErrConversationBusy
	}
	count := 0
	for _, run := range s.runs {
		if run.finished {
			continue
		}
		if params.Background && run.background {
			s.mu.Unlock()
			return nil, ErrBackgroundBusy
		}
		if !run.background && run.userID == params.Caller.User.ID {
			count++
		}
	}
	if !params.Background && count >= MaxUserRuns {
		s.mu.Unlock()
		return nil, ErrRunLimit
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(request))
	stopLifecycle := context.AfterFunc(s.ctx, cancel)
	run := &Run{supervisor: s, ConversationID: params.ConversationID, userID: params.Caller.User.ID, background: params.Background, member: params.Caller.User.Role != "admin", cancel: cancel, done: make(chan struct{}), subscribers: make(map[*Subscription]struct{})}
	s.runs[params.ConversationID] = run
	s.wg.Add(1)
	s.mu.Unlock()
	ready := make(chan struct{})
	result := make(chan error, 1)
	var once sync.Once
	params.Started = func() { once.Do(func() { close(ready) }) }
	go func() {
		defer s.wg.Done()
		defer cancel()
		defer stopLifecycle()
		if params.Finished != nil {
			defer params.Finished()
		}
		var terminal *Event
		// 凭据复查属于运行生命周期，断开连接后仍然有效。
		monitorDone := make(chan struct{})
		monitorStopped := make(chan struct{})
		if params.Check != nil {
			go func() {
				defer close(monitorStopped)
				ticker := time.NewTicker(60 * time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-monitorDone:
						return
					case <-ctx.Done():
						return
					case <-ticker.C:
						checkCtx, stop := context.WithTimeout(ctx, 5*time.Second)
						err := params.Check(checkCtx)
						stop()
						if errors.Is(err, ErrRunInvalidated) {
							if run.background {
								run.cancel()
							} else {
								s.CancelUser(run.userID)
							}
							return
						}
					}
				}
			}()
		} else {
			close(monitorStopped)
		}
		err := func() (err error) {
			defer func() {
				if recover() != nil {
					terminal = &Event{Type: EventDone, End: &RunEnd{Reason: EndInternalError, Message: "管家运行发生内部错误"}}
					s.logger.ErrorContext(context.WithoutCancel(ctx), "管家运行异常", "event", "steward.run.panicked", "module", "steward", "conversation_id", run.ConversationID, "stack", string(debug.Stack()))
				}
			}()
			if params.Check != nil {
				if err := params.Check(ctx); err != nil {
					return err
				}
			}
			return s.runner.Run(ctx, params, func(event Event) {
				if event.Type == EventDone {
					terminal = &event
					return
				}
				s.publish(run, event)
			})
		}()
		close(monitorDone)
		cancel()
		<-monitorStopped
		preflightFailed := false
		if err != nil {
			select {
			case <-ready:
			default:
				preflightFailed = true
			}
			if terminal == nil {
				terminal = &Event{Type: EventDone}
			}
			terminal.Failure = err
		}
		if terminal != nil && !preflightFailed {
			s.publish(run, *terminal)
		}
		run.mu.Lock()
		s.mu.Lock()
		run.finished = true
		if preflightFailed {
			delete(s.runs, run.ConversationID)
			if previous != nil && time.Now().Before(previous.retainedUntil) {
				s.runs[run.ConversationID] = previous
			}
			run.log = eventLog{}
		} else {
			run.retainedUntil = time.Now().Add(EventRetention)
		}
		s.mu.Unlock()
		for sub := range run.subscribers {
			close(sub.events)
			delete(run.subscribers, sub)
		}
		if terminal != nil && terminal.End != nil {
			run.end = *terminal.End
		}
		run.err = err
		close(run.done)
		run.mu.Unlock()
		if !preflightFailed {
			time.AfterFunc(EventRetention, func() {
				s.mu.Lock()
				if s.runs[run.ConversationID] == run {
					delete(s.runs, run.ConversationID)
				}
				s.mu.Unlock()
				run.mu.Lock()
				run.log = eventLog{}
				run.mu.Unlock()
			})
		}
		result <- err
	}()
	select {
	case <-ready:
		return run, nil
	case err := <-result:
		select {
		case <-ready:
			return run, nil
		default:
		}
		if err != nil {
			return nil, err
		}
		return run, nil
	}
}

func (s *Supervisor) publish(run *Run, event Event) {
	run.mu.Lock()
	defer run.mu.Unlock()
	event.Seq = s.sequence.Add(1)
	run.log.append(event)
	for sub := range run.subscribers {
		select {
		case sub.events <- event:
		default:
			close(sub.events)
			delete(run.subscribers, sub)
		}
	}
}

// Subscribe 原子取得日志快照并登记实时订阅，避免回放与实时之间漏事件。
// 所有权由调用入口在订阅前核对；nil 表示无日志或已结束且游标已到末尾。
func (s *Supervisor) Subscribe(conversationID string, afterSeq uint64) *Subscription {
	s.mu.Lock()
	run := s.runs[conversationID]
	s.mu.Unlock()
	if run == nil {
		return nil
	}
	return run.Subscribe(afterSeq)
}

// Subscribe 将发起请求绑定到 Start 返回的这次运行，避免很快开始的下一次运行替换订阅目标。
func (run *Run) Subscribe(afterSeq uint64) *Subscription {
	run.mu.Lock()
	defer run.mu.Unlock()
	return run.supervisor.subscribe(run, afterSeq)
}

// subscribe 的调用方持有 Run 锁；复制回放正文不占用 Supervisor 全局锁。
func (s *Supervisor) subscribe(run *Run, afterSeq uint64) *Subscription {
	if run == nil {
		return nil
	}
	replay := run.log.after(afterSeq)
	if run.finished && len(replay) == 0 {
		return nil
	}
	events := make(chan Event, 64)
	sub := &Subscription{Replay: replay, Events: events, events: events}
	sub.close = func() {
		run.mu.Lock()
		defer run.mu.Unlock()
		if _, ok := run.subscribers[sub]; ok {
			delete(run.subscribers, sub)
			close(events)
		}
	}
	if run.finished {
		close(events)
	} else {
		run.subscribers[sub] = struct{}{}
	}
	return sub
}

func (s *Supervisor) Cancel(conversationID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.runs[conversationID]
	if run == nil || run.finished {
		return false
	}
	run.cancel()
	return true
}

// Wait 等待本次运行持久化收尾；等待者的取消不影响运行。
func (run *Run) Wait(ctx context.Context) (RunEnd, error) {
	select {
	case <-run.done:
		return run.end, run.err
	case <-ctx.Done():
		return RunEnd{}, ctx.Err()
	}
}

func (s *Supervisor) CancelBackground(userID string) {
	s.cancelUserRuns(userID, true)
}

func (s *Supervisor) CancelUser(userID string) {
	s.cancelUserRuns(userID, false)
}

func (s *Supervisor) cancelUserRuns(userID string, background bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, run := range s.runs {
		if run.background == background && run.userID == userID && !run.finished {
			run.cancel()
		}
	}
}
func (s *Supervisor) CancelMembers() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, run := range s.runs {
		if !run.background && run.member && !run.finished {
			run.cancel()
		}
	}
}

// Shutdown 先关闭准入，再取消全部运行，最多等待十秒完成持久化收尾。
func (s *Supervisor) Shutdown(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, ShutdownTimeout)
	defer cancel()
	s.mu.Lock()
	s.closed = true
	for _, run := range s.runs {
		if !run.finished {
			run.cancel()
		}
	}
	s.mu.Unlock()
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
