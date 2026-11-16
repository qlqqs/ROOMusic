package provider

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
)

// 限制单个 SSE 事件和单次响应的累积数据，避免不可信端点无限占用内存。
const maxEventBytes = 8 << 20
const maxResponseBytes = 64 << 20

type sseEvent struct{ name, data string }

type streamParser interface {
	parse(sseEvent) ([]Event, bool, error)
}

type eventStream struct {
	parent    context.Context
	ctx       context.Context
	cancel    context.CancelFunc
	body      io.ReadCloser
	scanner   *bufio.Scanner
	parser    streamParser
	key       string
	status    int
	queue     []Event
	finished  bool
	readBytes int
	closeOnce sync.Once
	stopClose func() bool
}

func newStream(parent, ctx context.Context, cancel context.CancelFunc, response *http.Response, parser streamParser, key string) *eventStream {
	s := &eventStream{parent: parent, ctx: ctx, cancel: cancel, body: response.Body, parser: parser, key: key, status: response.StatusCode}
	s.scanner = bufio.NewScanner(response.Body)
	s.scanner.Buffer(make([]byte, 4096), maxEventBytes)
	s.stopClose = context.AfterFunc(ctx, s.closeBody)
	return s
}

func (s *eventStream) closeBody() { s.closeOnce.Do(func() { _ = s.body.Close() }) }

func (s *eventStream) Close() error {
	s.cancel()
	s.stopClose()
	s.closeBody()
	return nil
}

func (s *eventStream) Next() (Event, error) {
	for {
		if err := s.parent.Err(); err != nil {
			_ = s.Close()
			return Event{}, contextFailure(err, s.status)
		}
		if !s.finished {
			if err := s.ctx.Err(); err != nil {
				return Event{}, contextFailure(err, s.status)
			}
		}
		if len(s.queue) > 0 {
			event := s.queue[0]
			s.queue[0] = Event{}
			s.queue = s.queue[1:]
			return event, nil
		}
		if s.finished {
			return Event{}, io.EOF
		}
		frame, err := s.readEvent()
		if err == nil && (frame.name == "error" || hasError([]byte(frame.data))) {
			err = responseFailure(s.status, []byte(frame.data), s.key)
		}
		var events []Event
		var done bool
		if err == nil {
			events, done, err = s.parser.parse(frame)
		}
		if err != nil {
			s.finished = true
			cancelErr := s.ctx.Err()
			_ = s.Close()
			if cancelErr != nil {
				return Event{}, contextFailure(cancelErr, s.status)
			}
			if isTimeout(err) {
				return Event{}, failure(ErrorTimeout, s.status, "读取模型端点响应超时", "")
			}
			var typed *Error
			if errors.As(err, &typed) {
				if typed.Kind == ErrorInvalidResponse && frame.data != "" {
					return Event{}, failure(typed.Kind, s.status, typed.Message+": "+frame.data, s.key)
				}
				return Event{}, failure(typed.Kind, s.status, typed.Message, s.key)
			}
			return Event{}, failure(ErrorStreamInterrupted, s.status, "流在协议结束标志前中断", "")
		}
		s.queue = events
		if done {
			s.finished = true
			_ = s.Close()
		}
	}
}

func (s *eventStream) readEvent() (sseEvent, error) {
	var event sseEvent
	var data strings.Builder
	for s.scanner.Scan() {
		line := s.scanner.Text()
		s.readBytes += len(line) + 1
		if s.readBytes > maxResponseBytes || data.Len()+len(line) > maxEventBytes {
			return event, failure(ErrorInvalidResponse, s.status, "端点事件流超过读取上限", "")
		}
		if line == "" {
			if data.Len() == 0 && event.name == "" {
				continue
			}
			event.data = strings.TrimSuffix(data.String(), "\n")
			return event, nil
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, hasColon := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			event.name = value
		case "data":
			data.WriteString(value)
			data.WriteByte('\n')
		default:
			// id 和 retry 属于 SSE 元数据；裸 JSON 常见于以 200 返回的错误页。
			if strings.HasPrefix(strings.TrimSpace(line), "{") {
				if hasError([]byte(line)) {
					return event, responseFailure(s.status, []byte(line), s.key)
				}
				return event, failure(ErrorInvalidResponse, s.status, "端点未返回 SSE 数据: "+line, s.key)
			}
			if !hasColon && field != "id" && field != "retry" {
				return event, failure(ErrorInvalidResponse, s.status, "端点未返回 SSE 数据: "+line, s.key)
			}
		}
	}
	if err := s.scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return event, failure(ErrorInvalidResponse, s.status, "SSE 行超过读取上限", "")
		}
		return event, err
	}
	return event, io.ErrUnexpectedEOF
}
