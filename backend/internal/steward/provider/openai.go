package provider

import (
	"encoding/json"
	"sort"
	"strings"
)

func (a *adapter) openAIRequest(request Request) (map[string]any, error) {
	messages := make([]any, 0, len(request.Messages)+1)
	if request.System != "" {
		messages = append(messages, map[string]any{"role": "system", "content": request.System})
	}
	for _, message := range request.Messages {
		if err := validateMessage(message); err != nil {
			return nil, err
		}
		if message.Role == RoleTool {
			for _, block := range message.Content {
				if block.Type != BlockToolResult {
					return nil, invalidMessage()
				}
				result := block.ToolResult
				messages = append(messages, map[string]any{"role": "tool", "tool_call_id": result.CallID, "content": result.Content})
			}
			continue
		}
		wire := map[string]any{"role": message.Role}
		var content strings.Builder
		var calls []any
		for _, block := range message.Content {
			switch block.Type {
			case BlockText:
				content.WriteString(block.Text)
			case BlockToolCall:
				call := block.ToolCall
				args, err := normalizedArguments(string(call.Arguments))
				if err != nil {
					return nil, err
				}
				calls = append(calls, map[string]any{"id": call.ID, "type": "function", "function": map[string]any{"name": call.Name, "arguments": string(args)}})
			case BlockOpaque:
				if block.Opaque.Protocol != ProtocolOpenAI {
					continue
				}
				var fields map[string]json.RawMessage
				if json.Unmarshal(block.Opaque.Data, &fields) != nil || fields == nil {
					return nil, invalidMessage()
				}
				for name, value := range fields {
					if name == "role" || name == "content" || name == "tool_calls" {
						return nil, invalidMessage()
					}
					wire[name] = value
				}
			default:
				return nil, invalidMessage()
			}
		}
		wire["content"] = content.String()
		if len(calls) > 0 {
			wire["tool_calls"] = calls
		}
		messages = append(messages, wire)
	}
	body := map[string]any{"model": request.Model, "messages": messages, "stream": true, "stream_options": map[string]bool{"include_usage": true}}
	if request.MaxOutputTokens > 0 {
		field := "max_tokens"
		u, _ := parseEndpoint(a.endpoint)
		if p := matchPreset(u); p != nil && p.name == "OpenAI" {
			field = "max_completion_tokens"
		}
		body[field] = request.MaxOutputTokens
	}
	if len(request.Tools) > 0 {
		tools := make([]any, 0, len(request.Tools))
		for _, tool := range request.Tools {
			function := map[string]any{"name": tool.Name, "description": tool.Description}
			if len(tool.InputSchema) > 0 {
				function["parameters"] = tool.InputSchema
			}
			tools = append(tools, map[string]any{"type": "function", "function": function})
		}
		body["tools"] = tools
	}
	if request.ForceTool != "" {
		body["tool_choice"] = map[string]any{"type": "function", "function": map[string]string{"name": request.ForceTool}}
		body["parallel_tool_calls"] = false
	}
	return body, nil
}

func invalidMessage() *Error { return failure(ErrorOther, 0, "消息角色或内容块无效", "") }

func validateMessage(message Message) error {
	if message.Role != RoleUser && message.Role != RoleAssistant && message.Role != RoleTool {
		return invalidMessage()
	}
	for _, block := range message.Content {
		switch block.Type {
		case BlockText:
		case BlockToolCall:
			if message.Role != RoleAssistant || block.ToolCall == nil || block.ToolCall.ID == "" || block.ToolCall.Name == "" {
				return invalidMessage()
			}
		case BlockToolResult:
			if message.Role == RoleAssistant || block.ToolResult == nil || block.ToolResult.CallID == "" {
				return invalidMessage()
			}
		case BlockOpaque:
			if block.Opaque == nil || !json.Valid(block.Opaque.Data) {
				return invalidMessage()
			}
		default:
			return invalidMessage()
		}
	}
	return nil
}

type callFragments struct{ id, name, arguments strings.Builder }

type openAIParser struct {
	calls         map[int]*callFragments
	finish        string
	refusal       strings.Builder
	finishedCalls bool
}

func (p *openAIParser) parse(event sseEvent) ([]Event, bool, error) {
	if event.data == "[DONE]" {
		events, err := p.completeCalls()
		if err != nil {
			return nil, false, err
		}
		if p.refusal.Len() > 0 {
			raw, err := json.Marshal(map[string]string{"refusal": p.refusal.String()})
			if err != nil {
				return nil, false, err
			}
			events = append(events, Event{Type: EventOpaque, Opaque: &OpaqueBlock{Protocol: ProtocolOpenAI, Data: raw}})
		}
		return append(events, finishEvent(ProtocolOpenAI, p.finish)), true, nil
	}
	var chunk struct {
		Choices []struct {
			Index int `json:"index"`
			Delta struct {
				Content   string `json:"content"`
				Refusal   string `json:"refusal"`
				ToolCalls []struct {
					Index    int    `json:"index"`
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
		Usage *struct {
			Prompt     *int64 `json:"prompt_tokens"`
			Completion *int64 `json:"completion_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal([]byte(event.data), &chunk) != nil || chunk.Choices == nil {
		return nil, false, failure(ErrorInvalidResponse, 0, "无法解析 Chat Completions 流事件", "")
	}
	var events []Event
	for _, choice := range chunk.Choices {
		if choice.Index != 0 {
			continue
		} // 请求未设置 n，只消费唯一候选。
		if choice.Delta.Content != "" {
			events = append(events, Event{Type: EventText, Text: choice.Delta.Content})
		}
		p.refusal.WriteString(choice.Delta.Refusal)
		for _, delta := range choice.Delta.ToolCalls {
			if p.finishedCalls || delta.Index < 0 {
				return nil, false, failure(ErrorInvalidResponse, 0, "工具调用增量顺序无效", "")
			}
			call := p.calls[delta.Index]
			if call == nil {
				call = &callFragments{}
				p.calls[delta.Index] = call
			}
			// 同时覆盖官方参数分片、LM Studio 名称分片与 Ollama 整块参数。
			call.id.WriteString(delta.ID)
			call.name.WriteString(delta.Function.Name)
			call.arguments.WriteString(delta.Function.Arguments)
		}
		if choice.FinishReason != nil {
			p.finish = *choice.FinishReason
			calls, err := p.completeCalls()
			if err != nil {
				return nil, false, err
			}
			events = append(events, calls...)
		}
	}
	if chunk.Usage != nil {
		events = append(events, Event{Type: EventUsage, Usage: &Usage{InputTokens: chunk.Usage.Prompt, OutputTokens: chunk.Usage.Completion}})
	}
	return events, false, nil
}

func (p *openAIParser) completeCalls() ([]Event, error) {
	if p.finishedCalls {
		return nil, nil
	}
	p.finishedCalls = true
	indices := make([]int, 0, len(p.calls))
	for index := range p.calls {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	events := make([]Event, 0, len(indices))
	for _, index := range indices {
		fragments := p.calls[index]
		if p.finish == "length" && !json.Valid([]byte(fragments.arguments.String())) {
			continue
		}
		if fragments.id.Len() == 0 || fragments.name.Len() == 0 {
			return nil, failure(ErrorInvalidResponse, 0, "工具调用缺少标识或名称", "")
		}
		arguments, err := normalizedArguments(fragments.arguments.String())
		if err != nil {
			return nil, err
		}
		events = append(events, Event{Type: EventToolCall, ToolCall: &ToolCall{ID: fragments.id.String(), Name: fragments.name.String(), Arguments: arguments}})
	}
	p.calls = nil
	return events, nil
}
