package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/qlqq/roomusic/backend/internal/steward/provider"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

func (s *runState) executeCalls(ctx context.Context, calls []provider.ToolCall, stored []ToolCall, stop EndReason) ([]provider.Message, EndReason, error) {
	messages := make([]provider.Message, 0, len(calls))
	pending := []PlanItem{}
	for i, call := range calls {
		if ctx.Err() != nil {
			stop = EndCancelled
		}
		started := stop == ""
		var output tool.Result
		var code string
		var duration int64
		if started {
			s.sink(Event{Type: EventToolStarted, Step: s.step,
				ToolStarted: &ToolStarted{CallID: call.ID, Name: call.Name, Input: call.Arguments}})
			begin := time.Now()
			if ctx.Err() == nil {
				var err error
				caller := s.params.Caller
				caller.IdempotencyKey = stored[i].IdempotencyKey
				caller.SharedExecution = s.sharedDirect(call.Name)
				if definition, ok := s.definitions[call.Name]; ok && definition.Scope == tool.ScopeShared && !caller.SharedExecution {
					var preview tool.Preview
					if s.params.Background && len(pending) >= 20 {
						err = &tool.Error{Code: "too_many_proposals", Message: "自动整理每步最多提出 20 项修改"}
					} else {
						preview, err = s.registry.PreviewCall(ctx, caller, call.Name, call.Arguments)
					}
					if err == nil && s.params.Background && preview.BackgroundDirect {
						caller.SharedExecution = true
					} else if err == nil {
						pending = append(pending, PlanItem{Tool: call.Name, CallID: call.ID, ToolCallID: stored[i].ID, Position: stored[i].Position, Input: call.Arguments, Preview: preview, Revertible: definition.Revertible, IdempotencyKey: stored[i].IdempotencyKey, Untrusted: stored[i].Untrusted})
						continue
					}
				}
				if err == nil {
					output, err = s.invokeWithReconciliation(ctx, caller, call.Name, call.Arguments)
					if s.definitions[call.Name].Scope == tool.ScopeShared && databaseUnavailable(err) && ctx.Err() == nil {
						// 原键对账后仍不确定，停止模型续跑，不保存确定失败的工具结果。
						return messages, stop, err
					}
				}
				if err != nil {
					code, output.Output = toolFailure(err)
				}
			}
			duration = time.Since(begin).Milliseconds()
			if ctx.Err() != nil {
				stop = EndCancelled
				// 已成功返回的结果仍然保存；尚无结果的调用明确记为取消。
				if output.Output == nil || code != "" {
					code = "cancelled"
					output.Output = errorOutput(code, "工具调用已取消")
				}
			}
		} else {
			// 这些调用未执行，不发送开始/结束事件；补齐历史中的结果，避免悬空调用。
			code = string(stop)
			output.Output = errorOutput(code, "运行已停止，工具未执行")
		}
		status := "succeeded"
		var errorCode *string
		if code != "" {
			status = "failed"
			errorCode = &code
		}
		// 可信度只取后端登记的合同；错误说明由运行时生成。
		output.Untrusted = stored[i].Untrusted
		content := messageContent{Message: provider.Message{Role: provider.RoleTool,
			Content: []provider.ContentBlock{{Type: provider.BlockToolResult,
				ToolResult: &provider.ToolResult{CallID: call.ID, Content: string(output.Output), IsError: code != ""}}}},
			Source: &toolSource{Name: call.Name, Untrusted: output.Untrusted, Truncated: output.Truncated}}
		input, err := messageInput(content, provider.Usage{}, nil)
		if err != nil {
			return messages, stop, err
		}
		result := ToolCallResult{Output: output.Output, Truncated: output.Truncated, Status: status,
			ErrorCode: errorCode}
		if started {
			result.DurationMS = &duration
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), finalizeTimeout)
		_, err = s.store.FinishToolCallMessage(cleanup, s.params.Caller.User.ID, s.params.ConversationID, stored[i].ID, result, input)
		cancel()
		if err != nil {
			return messages, stop, err
		}
		messages = append(messages, modelMessage(content))
		if started {
			// 模型可以请求任意名称；日志只保留已登记且当前用户可见的名称。
			name := "<unavailable>"
			if _, known := s.definitions[call.Name]; known {
				name = call.Name
			}
			s.logger.InfoContext(context.WithoutCancel(ctx), "管家工具结束", "event", "steward.tool.finished",
				"message", "管家工具结束", "tool", name, "status", status, "error_code", code, "duration_ms", duration)
			s.sink(Event{Type: EventToolFinished, Step: s.step, ToolFinished: &ToolFinished{CallID: call.ID,
				Status: status, Output: output.Output, Truncated: output.Truncated, Untrusted: output.Untrusted, ErrorCode: code}})
		}
	}
	if len(pending) > 0 {
		if ctx.Err() != nil {
			return messages, EndCancelled, s.cancelPendingCalls(ctx, pending)
		}
		plans, ok := s.store.(PlanStore)
		if !ok {
			return messages, stop, ErrInvalidInput
		}
		plan, err := plans.CreatePlan(ctx, s.params.Caller.User.ID, s.params.ConversationID, pending, s.params.Background)
		if err != nil {
			if ctx.Err() != nil {
				return messages, EndCancelled, s.cancelPendingCalls(ctx, pending)
			}
			return messages, stop, err
		}
		if s.conversation.Mode == "steward" {
			return s.reviewPlan(ctx, plan)
		}
		s.sink(Event{Type: EventPlan, Step: s.step, Plan: &plan})
		stop = EndAwaitingConfirmation
	}
	return messages, stop, nil
}

func (s *runState) cancelPendingCalls(ctx context.Context, items []PlanItem) error {
	for _, item := range items {
		code := "cancelled"
		output := errorOutput(code, "工具调用已取消")
		input, err := planResultMessage(item, tool.Result{Output: output}, code)
		if err != nil {
			return err
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), finalizeTimeout)
		_, err = s.store.FinishToolCallMessage(cleanup, s.params.Caller.User.ID, s.params.ConversationID, item.ToolCallID,
			ToolCallResult{Output: output, Status: "failed", ErrorCode: &code}, input)
		cancel()
		if err != nil {
			return err
		}
		s.sink(Event{Type: EventToolFinished, Step: s.step, ToolFinished: &ToolFinished{CallID: item.CallID, Status: "failed", Output: output, Untrusted: item.Untrusted, ErrorCode: code}})
	}
	return nil
}

func databaseUnavailable(err error) bool {
	var failure *tool.Error
	return errors.As(err, &failure) && failure.Code == "database_unavailable"
}

// 提交结果不确定时只用原幂等键再调用一次；operations.Run 会优先返回已提交记录。
func (s *runState) invokeWithReconciliation(ctx context.Context, caller tool.Caller, name string, input json.RawMessage) (tool.Result, error) {
	output, err := s.registry.Invoke(ctx, caller, name, input)
	if definition, ok := s.definitions[name]; ok && definition.Scope == tool.ScopeShared && databaseUnavailable(err) && ctx.Err() == nil {
		return s.registry.Invoke(ctx, caller, name, input)
	}
	return output, err
}

func toolFailure(err error) (string, json.RawMessage) {
	code, message := "tool_execution_failed", "工具执行失败"
	var failure *tool.Error
	if errors.As(err, &failure) {
		// 输入校验与业务类型化错误保留工具提供的说明；鉴权等入口错误使用固定说明。
		switch failure.Code {
		case tool.CodeTargetNotFound:
			code, message = string(failure.Code), "目标不存在"
		case tool.CodeNotFound:
			code, message = string(failure.Code), "工具不存在"
		case tool.CodePermissionDenied:
			code, message = string(failure.Code), "无权调用此工具"
		case tool.CodeUnsupportedScope:
			code, message = string(failure.Code), "此入口不支持该工具的影响范围"
		case tool.CodeInvalidInput:
			code, message = string(failure.Code), failure.Message
		default:
			code, message = string(failure.Code), failure.Message
		}
	}
	return code, errorOutput(code, message)
}

func errorOutput(code, message string) json.RawMessage {
	output, _ := json.Marshal(struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}{Code: code, Message: message}) // 字符串结构编码不会失败。
	return output
}

// executePlanItems 执行批准项并收尾拒绝项；每项沿用自己的幂等键，拒绝前先只读对账。
func (s *runState) executePlanItems(ctx context.Context, plans PlanStore, items []PlanItem) error {
	var unsettled error
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return err
		}
		output := tool.Result{}
		code := ""
		started := time.Now()
		s.sink(Event{Type: EventToolStarted, ToolStarted: &ToolStarted{CallID: item.CallID, Name: item.Tool, Input: item.Input}})
		if item.Decision == "accepted" {
			caller := s.params.Caller
			caller.SharedExecution = true
			caller.PlanID = item.PlanID
			caller.ReviewID, caller.ReviewDecision = item.ReviewID, item.ReviewDecision
			caller.IdempotencyKey = item.IdempotencyKey
			var err error
			output, err = s.registry.Invoke(ctx, caller, item.Tool, item.Input)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				code, output.Output = toolFailure(err)
				if code == "database_unavailable" {
					// 提交结果不确定时保留未完成项，下次继续用原键向 operations 对账。
					unsettled = errors.Join(unsettled, err)
					continue
				}
			}
		} else {
			reader, ok := s.store.(OperationResultReader)
			if !ok {
				return ErrInvalidInput
			}
			recorded, err := reader.LookupOperationResult(ctx, s.params.Caller.User.ID, item.IdempotencyKey)
			if err != nil {
				return err
			}
			if recorded.Output != nil {
				output = tool.BoundResult(recorded.Output, item.Untrusted)
				item.Decision, item.OperationID = "accepted", &recorded.ID
			} else if item.Decision == "escalated" {
				code = "escalated_to_human"
				output.Output, _ = json.Marshal(map[string]any{"code": code, "exception_id": item.ExceptionID, "reasons": item.ReviewReasons})
			} else if item.Decision == "rejected_by_review" {
				code = "rejected_by_review"
				result := map[string]any{"code": code, "reasons": item.ReviewReasons, "review_id": item.ReviewID}
				if item.ExceptionID != "" {
					result["exception_id"] = item.ExceptionID
				}
				output.Output, _ = json.Marshal(result)
			} else {
				code = "declined_by_user"
				output.Output = errorOutput(code, "用户未选择这项修改")
			}
		}
		status := "succeeded"
		var errorCode *string
		if code != "" {
			status = "failed"
			errorCode = &code
		}
		input, err := planResultMessage(item, output, code)
		if err != nil {
			return err
		}
		duration := time.Since(started).Milliseconds()
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), finalizeTimeout)
		err = plans.FinishPlanItem(cleanup, s.params.Caller.User.ID, s.params.ConversationID, item, ToolCallResult{Output: output.Output, Truncated: output.Truncated, Status: status, ErrorCode: errorCode, DurationMS: &duration}, input)
		cancel()
		if err != nil {
			return err
		}
		s.logger.InfoContext(context.WithoutCancel(ctx), "管家工具结束", "event", "steward.tool.finished", "message", "管家工具结束", "tool", item.Tool, "status", status, "error_code", code, "duration_ms", duration)
		s.sink(Event{Type: EventToolFinished, ToolFinished: &ToolFinished{CallID: item.CallID, Status: status, Output: output.Output, Truncated: output.Truncated, Untrusted: item.Untrusted, ErrorCode: code}})
	}
	return unsettled
}
