package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"runtime/debug"
	"slices"
	"strings"
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
	memoryBlock   string
	summary       ConversationSummary
	summaryFailed bool
	*Runner
	params        RunParams
	sink          EventSink
	logger        *slog.Logger
	step          int
	inputTokens   int64
	outputTokens  int64
	countedSteps  int
	definitions   map[string]tool.Definition
	history       []Message
	conversation  Conversation
	reviewStarted bool
}

// Run 原子占用会话后同步执行多步对话；会话忙时返回 ErrConversationBusy 且不发送事件。
// 正常结束、限额、取消与模型错误通过 done 表达，返回 nil；存储错误返回调用方，
// 不冒充模型错误。所有退出路径均尝试释放已占用的会话，释放失败也通过返回值报告。
func (r *Runner) Run(ctx context.Context, params RunParams, sink EventSink) (err error) {
	params.Context, err = NormalizeContext(params.Context)
	if err != nil {
		return err
	}
	userID := params.Caller.User.ID
	if params.Decision != nil {
		plans, ok := r.store.(PlanStore)
		if !ok {
			return ErrInvalidInput
		}
		if err := plans.DecidePlan(ctx, userID, params.ConversationID, *params.Decision); err != nil {
			return err
		}
	} else if err := r.store.AcquireConversation(ctx, userID, params.ConversationID); err != nil {
		return err
	}
	runStarted := false
	notifyStarted := params.Started
	params.Started = func() {
		runStarted = true
		if notifyStarted != nil {
			notifyStarted()
		}
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
		// 前置读取或步骤间查库也可能被主动取消，不把它映射为数据库故障。
		if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
			err = nil
			end = RunEnd{Reason: EndCancelled}
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), finalizeTimeout)
		release := r.store.ReleaseConversation
		if completion, ok := r.store.(RunCompletionStore); ok && runStarted {
			release = completion.FinishConversation
		}
		releaseErr := release(cleanup, userID, params.ConversationID)
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
		if end.Reason != "" || (state.reviewStarted && err != nil) {
			sink(Event{Type: EventDone, Step: state.step, End: &end})
		}
	}()
	detail, err := r.store.GetConversation(ctx, userID, params.ConversationID)
	if err != nil {
		return err
	}
	if params.Background && detail.Conversation.Mode != "steward" {
		return ErrReviewNotApplicable
	}
	state.conversation = detail.Conversation
	state.summary = detail.Summary
	if r.memories != nil && !params.SkipMemory {
		remembered, readErr := r.memories.List(ctx, userID)
		if readErr != nil {
			state.logger.WarnContext(ctx, "管家记忆暂不可用", "event", "steward.memory.unavailable", "message", "管家记忆暂不可用")
		} else {
			state.memoryBlock = memoryPrompt(remembered.Items)
		}
	}
	if state.conversation.Mode == "" {
		state.conversation.Mode = "assistant"
	}
	state.params.Caller.Mode = state.conversation.Mode
	state.params.Caller.ConversationID = params.ConversationID
	state.params.Caller.SharedExecution = false
	state.params.Caller.PlanID = ""
	state.params.Caller.ReviewID = ""
	state.params.Caller.ReviewDecision = ""
	if params.ReviewPlanID != "" {
		if state.conversation.Mode != "steward" {
			return ErrReviewNotApplicable
		}
		var selected *Plan
		for i := range detail.Plans {
			if detail.Plans[i].ID == params.ReviewPlanID {
				selected = &detail.Plans[i]
				break
			}
		}
		if selected == nil {
			return ErrPlanNotFound
		}
		if err := checkPlanDecision(detail, PlanDecision{PlanID: selected.ID, Digest: selected.Digest}, params.Background); err != nil && !errors.Is(err, ErrConversationBusy) {
			return err
		}
		if params.Started != nil {
			params.Started()
			state.params.Started = nil
		}
		end, err = state.beforeStep(ctx)
		if err != nil || end.Reason != "" {
			return err
		}
		state.step = 1
		_, stop, reviewErr := state.reviewPlan(ctx, *selected)
		if reviewErr != nil {
			return reviewErr
		}
		if stop != "" {
			end = RunEnd{Reason: stop}
			return nil
		}
		if params.Selection != nil {
			end = RunEnd{Reason: EndCompleted}
			return nil
		}
		detail, err = r.store.GetConversation(ctx, userID, params.ConversationID)
		if err != nil {
			return err
		}
		state.history = detail.Messages
		state.params.Continue = true
		end, err = state.run(ctx)
		return err
	}
	if params.Continue {
		for _, plan := range detail.Plans {
			if plan.Status == "pending" {
				if params.Started != nil {
					params.Started()
				}
				pending := pendingPlan(plan)
				sink(Event{Type: EventPlan, Plan: &pending})
				end = RunEnd{Reason: EndAwaitingConfirmation}
				return nil
			}
		}
	}
	if plans, ok := r.store.(PlanStore); ok && (params.Continue || params.Decision != nil) {
		items, err := plans.PendingExecutions(ctx, userID, params.ConversationID)
		if err != nil {
			return err
		}
		if len(items) > 0 {
			if params.Started != nil {
				params.Started()
				params.Started = nil
			}
			if err := state.executePlanItems(ctx, plans, items); err != nil {
				if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
					end = RunEnd{Reason: EndCancelled}
					return nil
				}
				return err
			}
		}
	}
	if params.Continue {
		detail, err := r.store.GetConversation(ctx, userID, params.ConversationID)
		if err != nil {
			return err
		}
		if len(detail.Messages) == 0 {
			return ErrNothingToContinue
		}
		last := detail.Messages[len(detail.Messages)-1]
		if last.Role != string(provider.RoleUser) && last.Role != string(provider.RoleTool) {
			var content messageContent
			if err := json.Unmarshal(last.Content, &content); err != nil {
				return err
			}
			if content.FinishReason != provider.FinishPause {
				return ErrNothingToContinue
			}
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
		return s.prepareHistory(ctx, s.history)
	}
	content := textMessage(provider.RoleUser, s.params.Text)
	content.BackgroundItemID = s.params.BackgroundItemID
	content.BackgroundClaims = s.params.BackgroundClaims
	input, err := messageInput(content, provider.Usage{}, nil)
	if err != nil {
		return nil, err
	}
	input.Context = s.params.Context
	if _, err := s.store.AppendMessage(ctx, s.params.Caller.User.ID, s.params.ConversationID, input); err != nil {
		return nil, err
	}
	if s.params.Selection != nil {
		// 第一档只比较这一批的完整候选；历史仍在后台会话中供审计和重审读取。
		return []provider.Message{content.Message}, nil
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
	return s.prepareHistory(ctx, detail.Messages)
}

func (s *runState) beforeStep(ctx context.Context) (RunEnd, error) {
	if ctx.Err() != nil {
		return RunEnd{Reason: EndCancelled}, nil
	}
	if s.params.BeforeStep != nil {
		err := s.params.BeforeStep(ctx)
		return backgroundEnd(err)
	}
	return RunEnd{}, nil
}

func backgroundEnd(err error) (RunEnd, error) {
	switch {
	case errors.Is(err, ErrRunStopped):
		return RunEnd{Reason: EndCurationStopped}, nil
	case errors.Is(err, ErrItemCompleted):
		return RunEnd{Reason: EndCompleted}, nil
	case errors.Is(err, ErrItemLimit):
		return RunEnd{Reason: EndItemLimit}, nil
	default:
		return RunEnd{}, err
	}
}

func (s *runState) run(ctx context.Context) (RunEnd, error) {
	if s.params.OnToolCalls != nil {
		if end, err := s.beforeStep(ctx); err != nil || end.Reason != "" {
			return end, err
		}
	}
	messages, err := s.prepare(ctx)
	if err != nil {
		if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
			return RunEnd{Reason: EndCancelled}, nil
		}
		return RunEnd{}, err
	}
	var definitions []provider.ToolDefinition
	limit := maxSteps
	if s.params.Background && s.params.StepLimit > 0 {
		limit = s.params.StepLimit
	}
	for s.step < limit {
		if end, err := s.beforeStep(ctx); err != nil || end.Reason != "" {
			return end, err
		}
		if s.inputTokens+s.outputTokens >= maxRunTokens {
			return RunEnd{Reason: EndTokenLimit}, nil
		}
		if definitions == nil || s.params.Background {
			clear(s.definitions)
			listed := s.registry.ListContext(ctx, s.params.Caller)
			for _, d := range listed {
				s.definitions[d.Name] = d
			}
			definitions = modelTools(listed)
		}
		s.step++
		request := mainRequest(s.params, s.conversation.Mode, s.memoryBlock, messages, definitions)
		if budget := s.params.RequestBudget; budget > 0 {
			size, err := provider.RequestTokens(s.params.Protocol, s.params.Endpoint, request)
			if err != nil {
				return RunEnd{}, err
			}
			if size > budget {
				return RunEnd{Reason: EndProviderError, ProviderFailure: &provider.Error{Class: "content_too_long", Message: "内容太长"}}, nil
			}
		}
		step, err := s.readStep(ctx, request)
		s.recordUsage(ctx, UsageMain, step.usage)
		var check *requestCheckError
		if errors.As(err, &check) {
			return backgroundEnd(check.error)
		}
		var itemEnd RunEnd
		if s.params.OnToolCalls != nil && s.params.RemainingItemBudget == nil && (err == nil || s.params.RequestBudget == 0) {
			var countErr error
			itemEnd, countErr = backgroundEnd(s.params.OnToolCalls(ctx, len(step.calls)+nativeSearchCount(step.content.Searches)))
			if countErr != nil {
				return RunEnd{}, countErr
			}
		}
		if err != nil {
			// 中断时只保存已生成的文本，不留下没有结果的半截工具请求。
			var blocks []provider.ContentBlock
			for _, block := range step.content.Content {
				if block.Type == provider.BlockText {
					blocks = append(blocks, block)
				}
			}
			if ctx.Err() != nil && len(blocks) > 0 {
				step.content.Content = blocks
				step.content.FinishReason = provider.FinishPause
				input, encodeErr := messageInput(step.content, step.usage, nil)
				if encodeErr != nil {
					return RunEnd{}, encodeErr
				}
				cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), finalizeTimeout)
				_, saveErr := s.store.AppendMessage(cleanup, s.params.Caller.User.ID, s.params.ConversationID, input)
				cancel()
				if saveErr != nil {
					return RunEnd{}, saveErr
				}
			}
			return modelFailure(ctx, err), nil
		}
		// 选择模型只产生文本。保留原答复，把校验过的后台操作作为单独的执行记录。
		if s.params.Selection != nil {
			if len(step.calls) == 0 && step.finish.Reason == provider.FinishNormal {
				var reply strings.Builder
				for _, block := range step.content.Content {
					if block.Type == provider.BlockText {
						reply.WriteString(block.Text)
					}
				}
				actions, parseErr := s.params.Selection(reply.String())
				if parseErr == nil {
					step.content.BackendActions, step.calls = actions, actions
				}
			} else {
				itemEnd = RunEnd{Reason: EndCompleted}
			}
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
		stop := itemEnd.Reason
		if stop == "" && (s.inputTokens+s.outputTokens >= maxRunTokens || step.finish.Reason == provider.FinishOutputLimit) {
			stop = EndTokenLimit
		}
		if stop == "" && s.params.OnToolCalls != nil {
			end, err := s.beforeStep(ctx)
			if err != nil {
				return RunEnd{}, err
			}
			stop = end.Reason
		}
		toolMessages, stop, err := s.executeCalls(ctx, step.calls, stored.ToolCalls, stop)
		if err != nil {
			return RunEnd{Reason: stop}, err
		}
		if s.params.Selection != nil && stop == "" {
			stop = EndCompleted
		}
		if stop == "" && s.params.OnToolCalls != nil {
			if end, err := s.beforeStep(ctx); err != nil || end.Reason != "" {
				return end, err
			}
		}
		if stop == "" && s.step < limit && (len(step.calls) > 0 || step.finish.Reason == provider.FinishPause) && (s.conversation.Mode == "steward" || (!s.summaryFailed && step.usage.InputTokens != nil && *step.usage.InputTokens > summaryInputThreshold)) {
			detail, readErr := s.store.GetConversation(ctx, s.params.Caller.User.ID, s.params.ConversationID)
			if readErr != nil {
				return RunEnd{}, readErr
			}
			messages, err = s.prepareHistory(ctx, detail.Messages)
			if err != nil {
				return RunEnd{}, err
			}
		} else {
			messages = append(messages, toolMessages...)
		}
		if ctx.Err() != nil {
			return RunEnd{Reason: EndCancelled}, nil
		}
		if stop != "" {
			return RunEnd{Reason: stop}, nil
		}
		if len(step.calls) == 0 && step.finish.Reason != provider.FinishPause {
			return RunEnd{Reason: EndCompleted}, nil
		}
	}
	return RunEnd{Reason: EndStepLimit}, nil
}

func nativeSearchCount(searches []provider.Search) int {
	seen := make(map[string]bool)
	n := 0
	for _, search := range searches {
		if search.SearchID == "" || !seen[search.SearchID] {
			n++
		}
		seen[search.SearchID] = true
	}
	return n
}

type modelStep struct {
	content messageContent
	calls   []provider.ToolCall
	usage   provider.Usage
	finish  provider.Finish
}

func modelTools(listed []tool.Definition) []provider.ToolDefinition {
	var tools []provider.ToolDefinition
	for _, d := range listed {
		if d.Scope == tool.ScopeRead || d.Scope == tool.ScopePersonal || d.Scope == tool.ScopeShared {
			tools = append(tools, provider.ToolDefinition{Name: d.Name, Description: d.Description, InputSchema: d.Input})
		}
	}
	return tools
}

func mainRequest(p RunParams, mode, memory string, messages []provider.Message, tools []provider.ToolDefinition) provider.Request {
	prompt := systemPromptWithMode(p.DisplayName, mode)
	if p.Background {
		prompt = systemPromptWithName(p.DisplayName)
	}
	if p.Selection != nil {
		tools = nil
	}
	output := maxOutputTokens
	if p.RequestBudget > 0 {
		output = int(min(maxOutputTokens, max(1, p.RequestBudget/8)))
	}
	return provider.Request{Model: p.Model, System: prompt + p.SystemSuffix + memory, Messages: messages, Tools: tools, MaxOutputTokens: output, NativeSearch: p.NativeSearch}
}

// EstimateBackground 与新会话首步共用提示、工具、协议编码和输出预留。
func (r *Runner) EstimateBackground(ctx context.Context, p RunParams) (int64, error) {
	p.Background = true
	tools := modelTools(r.registry.ListContext(ctx, p.Caller))
	messages := []provider.Message{textMessage(provider.RoleUser, p.Text).Message}
	return provider.RequestTokens(p.Protocol, p.Endpoint, mainRequest(p, "steward", "", messages, tools))
}

type requestCheckError struct{ error }

func (s *runState) readStep(ctx context.Context, request provider.Request) (modelStep, error) {
	if s.params.RequestBudget == 0 {
		return s.readModel(ctx, request)
	}
	ctx = provider.SingleAttempt(ctx)
	checkRequest := func() error {
		if s.params.BeforeRequest != nil {
			return s.params.BeforeRequest(ctx)
		}
		return nil
	}
	if err := checkRequest(); err != nil {
		return modelStep{}, &requestCheckError{err}
	}
	started := time.Now()
	for attempt := 1; ; attempt++ {
		var estimate int64
		if s.params.RemainingItemBudget != nil {
			calls, tokens := s.params.RemainingItemBudget()
			if tokens <= 0 || calls <= 0 {
				return modelStep{}, &requestCheckError{ErrItemLimit}
			}
			request.MaxNativeSearchCalls = calls
			size, err := provider.RequestTokens(s.params.Protocol, s.params.Endpoint, request)
			if err != nil {
				return modelStep{}, err
			}
			input := size - int64(request.MaxOutputTokens)
			if input >= tokens {
				return modelStep{}, &requestCheckError{ErrItemLimit}
			}
			request.MaxOutputTokens = int(min(int64(request.MaxOutputTokens), tokens-input))
			estimate = input + int64(request.MaxOutputTokens)
		}
		var events []Event
		sink := s.sink
		s.sink = func(e Event) { events = append(events, e) }
		step, err := s.readModel(ctx, request)
		s.sink = sink
		if estimate > 0 {
			if n := nativeSearchCount(step.content.Searches); n > 0 {
				if countErr := s.params.OnToolCalls(context.WithoutCancel(ctx), n); countErr != nil {
					return step, &requestCheckError{countErr}
				}
			}
		}
		if estimate > 0 && step.usage.InputTokens == nil && step.usage.OutputTokens == nil {
			if chargeErr := s.params.ChargeEstimatedUsage(context.WithoutCancel(ctx), estimate); chargeErr != nil {
				return step, chargeErr
			}
		}

		if err == nil {
			for _, e := range events {
				sink(e)
			}
			return step, nil
		}
		delay, retry := provider.BackgroundRetry(err, attempt, started)
		if !retry || ctx.Err() != nil {
			return step, err
		}
		select {
		case <-ctx.Done():
			return step, ctx.Err()
		case <-time.After(delay):
		}
		if s.params.Check != nil {
			if checkErr := s.params.Check(ctx); checkErr != nil {
				return step, &requestCheckError{checkErr}
			}
		}
		if time.Since(started) >= 10*time.Second {
			return step, err
		}
		s.recordUsage(ctx, UsageMain, step.usage)
		if checkErr := checkRequest(); checkErr != nil {
			return modelStep{}, &requestCheckError{checkErr}
		}
	}
}

func (s *runState) readModel(ctx context.Context, request provider.Request) (step modelStep, err error) {
	step.content.Role = provider.RoleAssistant
	step.content.Origin = providerOrigin{Protocol: s.params.Protocol, Endpoint: s.params.Endpoint, Model: s.params.Model}
	started := time.Now()
	defer func() {
		if len(step.content.Searches) > 0 {
			failed := 0
			for _, search := range step.content.Searches {
				if search.Status == "failed" {
					failed++
				}
			}
			code := ""
			if failed > 0 {
				code = "search_failed"
			}
			s.logger.InfoContext(ctx, "管家模型搜索结束", "event", "steward.search.finished", "message", "管家模型搜索结束", "module", "steward",
				"search_count", len(step.content.Searches), "failed_count", failed, "duration_ms", time.Since(started).Milliseconds(), "error_code", code)
		}
	}()
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
		case provider.EventSearch:
			search := provider.CleanSearch(*event.Search)
			step.content.Searches = append(step.content.Searches, search)
			s.sink(Event{Type: EventSearch, Step: s.step, Search: &search})
		case provider.EventCitations:
			step.content.Citations = provider.CleanCitations(append(step.content.Citations, event.Citations...))
		case provider.EventUsage:
			step.usage = *event.Usage
		case provider.EventDone:
			step.finish = *event.Finish
			step.content.FinishReason = step.finish.Reason
			step.content.ProviderBlocks = event.ProviderBlocks
			if len(step.content.Citations) > 0 {
				s.sink(Event{Type: EventCitations, Step: s.step, Citations: step.content.Citations})
			}
			return step, nil
		}
	}
}

func (s *runState) recordUsage(ctx context.Context, kind UsageKind, usage provider.Usage) {
	if usage.InputTokens == nil && usage.OutputTokens == nil {
		return
	}
	s.reportUsage(ctx, kind, usage)
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

// 日用量按已知的一侧累加，主对话的 SSE 仍保留未知计数。
func (s *runState) reportUsage(ctx context.Context, kind UsageKind, usage provider.Usage) {
	if s.params.Usage == nil || (usage.InputTokens == nil && usage.OutputTokens == nil) {
		return
	}
	if usage.InputTokens == nil {
		usage.InputTokens = new(int64)
	}
	if usage.OutputTokens == nil {
		usage.OutputTokens = new(int64)
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), finalizeTimeout)
	defer cancel()
	s.params.Usage(cleanup, kind, usage)
}

func modelFailure(ctx context.Context, err error) RunEnd {
	if errors.Is(ctx.Err(), context.Canceled) {
		return RunEnd{Reason: EndCancelled}
	}
	var failure *provider.Error
	if errors.As(err, &failure) {
		return RunEnd{ProviderFailure: failure, Reason: EndProviderError, ErrorKind: failure.Kind, Message: failure.Message, StatusCode: failure.StatusCode}
	}
	return RunEnd{Reason: EndProviderError, ErrorKind: provider.ErrorOther, Message: "模型调用失败"}
}

func (s *runState) sharedDirect(name string) bool {
	if s.params.Background {
		return false
	}
	return s.conversation.Mode == "operator" || (s.conversation.Mode == "assistant" && s.definitions[name].Revertible && slices.Contains(s.conversation.AutoApprovedTools, name))
}
