package runtime

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/qlqq/roomusic/backend/internal/steward/provider"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

const (
	maxSteps              = 24              // 一次运行最多发起 24 次模型请求。
	maxRunTokens    int64 = 400000          // 只累计输入、输出用量均已知的步骤，最多 400,000 token。
	maxOutputTokens       = 8192            // 每次模型请求最多输出 8,192 token。
	finalizeTimeout       = 5 * time.Second // 每次持久化收尾及释放会话最多等待五秒。
	titleCharacters       = 32              // 默认标题取第一条用户消息的前 32 个 Unicode 字符。
)

type runState struct {
	*Runner
	params       RunParams
	sink         EventSink
	logger       *slog.Logger
	step         int
	inputTokens  int64
	outputTokens int64
	countedSteps int
	definitions  map[string]tool.Definition
	history      []Message
}

// Run 原子占用会话后同步执行多步对话；会话忙时返回 ErrConversationBusy 且不发送事件。
// 正常结束、限额、取消与模型错误通过 done 表达，返回 nil；存储错误返回调用方，
// 不冒充模型错误。所有退出路径均尝试释放已占用的会话，释放失败也通过返回值报告。
func (r *Runner) Run(ctx context.Context, params RunParams, sink EventSink) (err error) {
	userID := params.Caller.User.ID
	if err := r.store.AcquireConversation(ctx, userID, params.ConversationID); err != nil {
		return err
	}
	started := time.Now()
	state := runState{Runner: r, params: params, sink: sink,
		logger:      r.logger.With("module", "steward", "conversation_id", params.ConversationID),
		definitions: make(map[string]tool.Definition)}
	var end RunEnd
	defer func() {
		if recover() != nil {
			end = RunEnd{Reason: EndInternalError, Message: "管家运行发生内部错误"}
			state.logger.ErrorContext(context.WithoutCancel(ctx), "管家运行异常", "event", "steward.run.panicked", "stack", string(debug.Stack()))
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), finalizeTimeout)
		releaseErr := r.store.ReleaseConversation(cleanup, userID, params.ConversationID)
		err = errors.Join(err, releaseErr)
		cancel()
		if errors.Is(err, ErrNothingToContinue) && releaseErr == nil {
			return
		}
		level := slog.LevelInfo
		if err != nil {
			level = slog.LevelError
		} else if end.Reason != EndCompleted {
			level = slog.LevelWarn
		}
		state.logger.LogAttrs(context.WithoutCancel(ctx), level, "管家运行结束",
			slog.String("event", "steward.run.finished"), slog.String("message", "管家运行结束"),
			slog.String("reason", string(end.Reason)), slog.String("error_kind", string(end.ErrorKind)), slog.Int("status_code", end.StatusCode),
			slog.Bool("storage_failed", err != nil), slog.Int("steps", state.step),
			slog.Int64("duration_ms", time.Since(started).Milliseconds()),
			slog.Int64("input_tokens", state.inputTokens), slog.Int64("output_tokens", state.outputTokens),
			slog.Int("usage_known_steps", state.countedSteps))
		if end.Reason != "" {
			sink(Event{Type: EventDone, Step: state.step, End: &end})
		}
	}()
	if params.Continue {
		detail, err := r.store.GetConversation(ctx, userID, params.ConversationID)
		if err != nil {
			return err
		}
		if len(detail.Messages) == 0 {
			return ErrNothingToContinue
		}
		lastRole := detail.Messages[len(detail.Messages)-1].Role
		if lastRole != string(provider.RoleUser) && lastRole != string(provider.RoleTool) {
			return ErrNothingToContinue
		}
		state.history = detail.Messages
	}
	state.logger.InfoContext(ctx, "管家运行开始", "event", "steward.run.started", "message", "管家运行开始")
	if params.Started != nil {
		params.Started()
	}
	end, err = state.run(ctx)
	return err
}

func (s *runState) prepare(ctx context.Context) ([]provider.Message, error) {
	if s.params.Continue {
		return replayMessages(s.history)
	}
	input, err := messageInput(textMessage(provider.RoleUser, s.params.Text), provider.Usage{}, nil)
	if err != nil {
		return nil, err
	}
	if _, err := s.store.AppendMessage(ctx, s.params.Caller.User.ID, s.params.ConversationID, input); err != nil {
		return nil, err
	}
	detail, err := s.store.GetConversation(ctx, s.params.Caller.User.ID, s.params.ConversationID)
	if err != nil {
		return nil, err
	}
	if detail.Conversation.Title == "" {
		title, err := firstTitle(detail.Messages)
		if err != nil {
			return nil, err
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), finalizeTimeout)
		err = s.store.RenameConversation(cleanup, s.params.Caller.User.ID, s.params.ConversationID, title)
		cancel()
		if err != nil {
			return nil, err
		}
	}
	return replayMessages(detail.Messages)
}

func (s *runState) run(ctx context.Context) (RunEnd, error) {
	messages, err := s.prepare(ctx)
	if err != nil {
		if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
			return RunEnd{Reason: EndCancelled}, nil
		}
		return RunEnd{}, err
	}
	var definitions []provider.ToolDefinition
	for _, definition := range s.registry.List(s.params.Caller) {
		s.definitions[definition.Name] = definition
		if definition.Scope == tool.ScopeRead {
			definitions = append(definitions, provider.ToolDefinition{Name: definition.Name,
				Description: definition.Description, InputSchema: definition.Input})
		}
	}
	for s.step < maxSteps {
		if ctx.Err() != nil {
			return RunEnd{Reason: EndCancelled}, nil
		}
		s.step++
		step, err := s.readModel(ctx, provider.Request{Model: s.params.Model, System: systemPromptWithName(s.params.DisplayName),
			Messages: messages, Tools: definitions, MaxOutputTokens: maxOutputTokens})
		s.recordUsage(ctx, step.usage)
		if err != nil {
			return modelFailure(ctx, err), nil
		}
		inputCalls := make([]ToolCallInput, 0, len(step.calls))
		for _, call := range step.calls {
			inputCalls = append(inputCalls, ToolCallInput{Name: call.Name, Input: call.Arguments,
				Untrusted: s.definitions[call.Name].Untrusted})
		}
		input, err := messageInput(step.content, step.usage, inputCalls)
		if err != nil {
			return RunEnd{}, err
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), finalizeTimeout)
		stored, err := s.store.AppendMessage(cleanup, s.params.Caller.User.ID, s.params.ConversationID, input)
		cancel()
		if err != nil {
			return RunEnd{}, err
		}
		messages = append(messages, modelMessage(step.content))
		var stop EndReason
		if s.inputTokens+s.outputTokens >= maxRunTokens || step.finish.Reason == provider.FinishOutputLimit {
			stop = EndTokenLimit
		}
		toolMessages, stop, err := s.executeCalls(ctx, step.calls, stored.ToolCalls, stop)
		if err != nil {
			return RunEnd{Reason: stop}, err
		}
		messages = append(messages, toolMessages...)
		if ctx.Err() != nil {
			return RunEnd{Reason: EndCancelled}, nil
		}
		if stop != "" {
			return RunEnd{Reason: stop}, nil
		}
		if len(step.calls) == 0 {
			return RunEnd{Reason: EndCompleted}, nil
		}
	}
	return RunEnd{Reason: EndStepLimit}, nil
}

type modelStep struct {
	content messageContent
	calls   []provider.ToolCall
	usage   provider.Usage
	finish  provider.Finish
}

func (s *runState) readModel(ctx context.Context, request provider.Request) (step modelStep, err error) {
	step.content.Role = provider.RoleAssistant
	stream, err := s.params.Provider.Stream(ctx, request)
	if err != nil {
		return step, err
	}
	defer func() { err = errors.Join(err, stream.Close()) }()
	for {
		if err := ctx.Err(); err != nil {
			return step, err
		}
		event, err := stream.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = &provider.Error{Kind: provider.ErrorStreamInterrupted, Message: "模型流未完整结束"}
			}
			return step, err
		}
		if err := ctx.Err(); err != nil {
			return step, err
		}
		switch event.Type {
		case provider.EventText:
			blocks := step.content.Content
			if len(blocks) > 0 && blocks[len(blocks)-1].Type == provider.BlockText {
				blocks[len(blocks)-1].Text += event.Text
			} else {
				step.content.Content = append(blocks, provider.ContentBlock{Type: provider.BlockText, Text: event.Text})
			}
			s.sink(Event{Type: EventText, Step: s.step, Text: event.Text})
		case provider.EventToolCall:
			step.calls = append(step.calls, *event.ToolCall)
			step.content.Content = append(step.content.Content, provider.ContentBlock{Type: provider.BlockToolCall, ToolCall: event.ToolCall})
		case provider.EventOpaque:
			step.content.Content = append(step.content.Content, provider.ContentBlock{Type: provider.BlockOpaque, Opaque: event.Opaque})
		case provider.EventUsage:
			step.usage = *event.Usage
		case provider.EventDone:
			step.finish = *event.Finish
			return step, nil
		}
	}
}

func (s *runState) recordUsage(ctx context.Context, usage provider.Usage) {
	if usage.InputTokens == nil && usage.OutputTokens == nil {
		return
	}
	// Provider 的事件是累计快照；每步只统计最后一份，任一计数缺失都不估算。
	if usage.InputTokens != nil && usage.OutputTokens != nil {
		s.inputTokens += *usage.InputTokens
		s.outputTokens += *usage.OutputTokens
		s.countedSteps++
	}
	s.logger.InfoContext(ctx, "管家模型用量", "event", "steward.model.usage", "message", "管家模型用量",
		"step", s.step, "input_tokens", usage.InputTokens, "output_tokens", usage.OutputTokens)
	s.sink(Event{Type: EventUsage, Step: s.step, Usage: &usage})
}

func modelFailure(ctx context.Context, err error) RunEnd {
	if errors.Is(ctx.Err(), context.Canceled) {
		return RunEnd{Reason: EndCancelled}
	}
	var failure *provider.Error
	if errors.As(err, &failure) {
		return RunEnd{Reason: EndProviderError, ErrorKind: failure.Kind, Message: failure.Message, StatusCode: failure.StatusCode}
	}
	return RunEnd{Reason: EndProviderError, ErrorKind: provider.ErrorOther, Message: "模型调用失败"}
}
