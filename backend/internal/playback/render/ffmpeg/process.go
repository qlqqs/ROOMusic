package ffmpeg

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// StartOptions 的 Args 由渲染器构造，不经过 shell。Source 仅借用，作为 fd 3 传入。
// ReadTimeout 只用于解码器；编码器的输出停顿可能是合法背压。
type StartOptions struct {
	Args        []string
	Source      *os.File
	Stdin       bool
	ReadTimeout bool
}

// Process 的 stdout 只能有一个读者；写入、等待与关闭可并发。
// 使用完毕必须 Close；自然结束先 CloseInput、读到 EOF，再 Wait。
type Process struct {
	cmd           *exec.Cmd
	ctx           context.Context
	cancel        context.CancelCauseFunc
	stdout, stdin *os.File
	done          chan struct{}
	cleanupDone   chan struct{}
	waitErr       error
	closeOnce     sync.Once
	inputOnce     sync.Once
	inputErr      error
	readMu        sync.Mutex
	timed         bool
	newTimer      func(time.Duration) Timer
	watch         chan watchEvent
	watchDone     chan struct{}
	watchStop     chan struct{}
	watchOnce     sync.Once
	firstReady    chan struct{}
	firstByte     [1]byte
	firstN        int
	firstErr      error
	firstConsumed bool
}
type watchEvent struct {
	reading bool
	ack     chan struct{}
}

// Start 只取一个名额。成对开流请用 Reserve(2)，避免半占名额。
func (t *Tool) Start(ctx context.Context, options StartOptions) (*Process, error) {
	r, err := t.Reserve(ctx, 1)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return r.Start(ctx, options)
}

// Start 消耗一个预留名额；启动失败也立即释放该名额。
func (r *Reservation) Start(ctx context.Context, options StartOptions) (*Process, error) {
	r.mu.Lock()
	if r.remaining == 0 {
		r.mu.Unlock()
		return nil, errors.New("ffmpeg reservation is empty")
	}
	r.remaining--
	r.mu.Unlock()
	p, err := r.tool.start(ctx, options)
	if err != nil {
		r.tool.release(1)
	}
	return p, err
}

func (t *Tool) start(ctx context.Context, options StartOptions) (*Process, error) {
	childCtx, cancel := context.WithCancelCause(ctx)
	cmd := exec.CommandContext(childCtx, t.path, options.Args...)
	cmd.Env = []string{"LC_ALL=C"}
	configureProcess(cmd)
	if options.Source != nil {
		cmd.ExtraFiles = []*os.File{options.Source}
	}
	output, outputWriter, err := os.Pipe()
	if err != nil {
		cancel(err)
		return nil, err
	}
	cmd.Stdout = outputWriter
	var input, inputReader *os.File
	if options.Stdin {
		inputReader, input, err = os.Pipe()
		if err != nil {
			output.Close()
			outputWriter.Close()
			cancel(err)
			return nil, err
		}
		cmd.Stdin = inputReader
	}
	stderr := &tailBuffer{limit: 4096}
	cmd.Stderr = stderr
	if err = cmd.Start(); err != nil {
		output.Close()
		outputWriter.Close()
		if input != nil {
			input.Close()
			inputReader.Close()
		}
		cancel(err)
		return nil, fmt.Errorf("start ffmpeg: %w", err)
	}
	outputWriter.Close()
	if inputReader != nil {
		inputReader.Close()
	}
	p := &Process{cmd: cmd, ctx: childCtx, cancel: cancel, stdout: output, stdin: input, done: make(chan struct{}), cleanupDone: make(chan struct{}), timed: options.ReadTimeout, newTimer: t.newTimer,
		watch: make(chan watchEvent), watchDone: make(chan struct{}), watchStop: make(chan struct{}), firstReady: make(chan struct{})}
	if p.timed {
		// 提前读取一个字节，起始计时不依赖消费者何时首次读取，也不会形成无界预读。
		timer := t.newTimer(10 * time.Second)
		go p.watchReads(t.newTimer, timer)
		go func() {
			p.firstN, p.firstErr = p.stdout.Read(p.firstByte[:])
			p.setReading(false)
			if p.firstErr != nil {
				p.stopWatch()
			}
			close(p.firstReady)
		}()
	}
	go func() {
		err := cmd.Wait()
		if cause := context.Cause(childCtx); cause != nil {
			err = cause
		}
		if err != nil {
			p.waitErr = &ProcessError{Err: err, Stderr: stderr.String()}
		}
		t.release(1)
		close(p.done)
	}()
	// 生命周期由 Close 收束。即使组长已退出，取消仍须杀掉继承管道的后代，
	// 不能在 Wait 返回时就撤销取消监听，否则探测读取可能永远挂住。
	go func() {
		defer close(p.cleanupDone)
		<-childCtx.Done()
		_ = killProcess(p.cmd)
		p.closePipes()
		p.stopWatch()
	}()
	return p, nil
}

// ProcessError 把有界诊断与可公开的错误文本分开，不把媒体路径泄漏到日志或响应。
type ProcessError struct {
	Err    error
	Stderr string
}

func (e *ProcessError) Error() string { return "ffmpeg process failed" }
func (e *ProcessError) Unwrap() error { return e.Err }

func (p *Process) watchReads(newTimer func(time.Duration) Timer, timer Timer) {
	defer close(p.watchDone)
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		var tick <-chan time.Time
		if timer != nil {
			tick = timer.C()
		}
		select {
		case <-p.watchStop:
			return
		case event := <-p.watch:
			if timer != nil {
				timer.Stop()
				timer = nil
			}
			if event.reading {
				timer = newTimer(10 * time.Second)
			}
			close(event.ack)
		case <-tick:
			p.cancel(ErrReadTimeout)
			return
		}
	}
}
func (p *Process) setReading(reading bool) {
	if !p.timed {
		return
	}
	event := watchEvent{reading: reading, ack: make(chan struct{})}
	select {
	case p.watch <- event:
		select {
		case <-event.ack:
		case <-p.watchDone:
		}
	case <-p.watchDone:
	}
}
func (p *Process) stopWatch() { p.watchOnce.Do(func() { close(p.watchStop) }) }

// Read 只在实际等待 stdout 字节期间计停滞超时。两次 Read 之间的环满等待不计时。
// stdout EOF 后另设退出等待上限，编码器也适用；这不限制正常输出背压。
func (p *Process) Read(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	p.readMu.Lock()
	defer p.readMu.Unlock()
	var n int
	var err error
	if p.timed && !p.firstConsumed {
		<-p.firstReady
		p.firstConsumed = true
		n = copy(data, p.firstByte[:p.firstN])
		err = p.firstErr
	} else {
		p.setReading(true)
		n, err = p.stdout.Read(data)
		p.setReading(false)
	}
	if cause := context.Cause(p.ctx); cause != nil {
		return n, cause
	}
	if err != nil {
		p.stopWatch()
		if errors.Is(err, io.EOF) {
			if waitErr := p.waitAfterEOF(); waitErr != nil {
				return n, waitErr
			}
		}
	}
	return n, err
}

// waitAfterEOF 不依赖读取监控：stdout 已关闭但进程未退出时，WaitDelay 尚未启动。
// 超时沿用注入计时器与读取超时分类，取消后由 Close 收束进程、管道和监控协程。
func (p *Process) waitAfterEOF() error {
	select {
	case <-p.done:
		return p.waitErr
	default:
	}
	timer := p.newTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case <-p.done:
		return p.waitErr
	case <-p.ctx.Done():
	case <-timer.C():
		p.cancel(ErrReadTimeout)
	}
	_ = p.Close()
	return context.Cause(p.ctx)
}

func (p *Process) Stdout() io.ReadCloser { return p }
func (p *Process) Write(data []byte) (int, error) {
	if p.stdin == nil {
		return 0, errors.New("ffmpeg stdin is not enabled")
	}
	n, err := p.stdin.Write(data)
	if cause := context.Cause(p.ctx); cause != nil {
		return n, cause
	}
	return n, err
}
func (p *Process) CloseInput() error {
	p.inputOnce.Do(func() {
		if p.stdin != nil {
			p.inputErr = p.stdin.Close()
		}
	})
	return p.inputErr
}
func (p *Process) closePipes() { _ = p.stdout.Close(); _ = p.CloseInput() }

// Wait 可重复调用，名额只在底层进程完成回收后归还。
func (p *Process) Wait() error {
	<-p.done
	return p.waitErr
}

// Close 幂等，先关闭读端、终止进程组，再等待回收。
func (p *Process) Close() error {
	p.closeOnce.Do(func() {
		p.closePipes()
		p.cancel(context.Canceled)
		p.stopWatch()
		<-p.cleanupDone
		<-p.done
		if p.timed {
			<-p.firstReady
			<-p.watchDone
		}
	})
	return nil
}

// tailBuffer 始终消耗全部 stderr，但只保留末尾的有界诊断。
type tailBuffer struct {
	limit int
	data  []byte
}

func (b *tailBuffer) Write(data []byte) (int, error) {
	n := len(data)
	if n >= b.limit {
		b.data = append(b.data[:0], data[n-b.limit:]...)
		return n, nil
	}
	if excess := len(b.data) + n - b.limit; excess > 0 {
		copy(b.data, b.data[excess:])
		b.data = b.data[:len(b.data)-excess]
	}
	b.data = append(b.data, data...)
	return n, nil
}
func (b *tailBuffer) String() string { return string(b.data) }
