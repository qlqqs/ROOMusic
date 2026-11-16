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
				output, err = s.registry.Invoke(ctx, s.params.Caller, call.Name, call.Arguments)
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
	return messages, stop, nil
}

func toolFailure(err error) (string, json.RawMessage) {
	code, message := "tool_execution_failed", "工具执行失败"
	var failure *tool.Error
	if errors.As(err, &failure) {
		// 入口校验使用固定说明；处理器的类型化错误按工具合同提供可展示的简短说明。
		switch failure.Code {
		case tool.CodeTargetNotFound:
			code, message = string(failure.Code), "目标不存在"
		case tool.CodeNotFound:
			code, message = string(failure.Code), "工具不存在"
		case tool.CodePermissionDenied:
			code, message = string(failure.Code), "无权调用此工具"
		case tool.CodeUnsupportedScope:
			code, message = string(failure.Code), "此入口只支持只读工具"
		case tool.CodeInvalidInput:
			code, message = string(failure.Code), "工具输入不符合要求"
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
