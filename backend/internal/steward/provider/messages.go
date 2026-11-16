package provider

import (
	"encoding/json"
	"strings"
)

type messagesWire struct {
	Role    Role              `json:"role"`
	Content []json.RawMessage `json:"content"`
}

func messagesRequest(request Request) (map[string]any, error) {
	messages := make([]messagesWire, 0, len(request.Messages))
	for _, message := range request.Messages {
		if err := validateMessage(message); err != nil {
			return nil, err
		}
		role := message.Role
		if role == RoleTool {
			role = RoleUser
		}
		content := make([]json.RawMessage, 0, len(message.Content))
		for _, block := range message.Content {
			var wire any
			switch block.Type {
			case BlockText:
				wire = map[string]any{"type": "text", "text": block.Text}
			case BlockToolCall:
				call := block.ToolCall
				args, err := normalizedArguments(string(call.Arguments))
				if err != nil {
					return nil, err
				}
				if len(args) == 0 || args[0] != '{' {
					return nil, failure(ErrorOther, 0, "Messages 工具参数必须是 JSON 对象", "")
				}
				wire = map[string]any{"type": "tool_use", "id": call.ID, "name": call.Name, "input": args}
			case BlockToolResult:
				result := block.ToolResult
				wire = map[string]any{"type": "tool_result", "tool_use_id": result.CallID, "content": result.Content, "is_error": result.IsError}
			case BlockOpaque:
				if block.Opaque.Protocol != ProtocolMessages {
					continue
				}
				content = append(content, block.Opaque.Data)
				continue
			}
			raw, err := json.Marshal(wire)
			if err != nil {
				return nil, failure(ErrorOther, 0, "无法编码 Messages 内容块", "")
			}
			content = append(content, raw)
		}
		if len(content) == 0 {
			continue
		}
		// 连续的 tool/user 消息属于同一用户轮次，合并所有并行结果，
		// 再稳定地把 tool_result 移到文本之前；assistant 块的顺序不改变。
		if role == RoleUser && len(messages) > 0 && messages[len(messages)-1].Role == RoleUser {
			last := &messages[len(messages)-1]
			last.Content = append(last.Content, content...)
		} else {
			messages = append(messages, messagesWire{Role: role, Content: content})
		}
	}
	for i := range messages {
		if messages[i].Role != RoleUser {
			continue
		}
		var results, rest []json.RawMessage
		for _, raw := range messages[i].Content {
			var block struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(raw, &block) != nil {
				return nil, invalidMessage()
			}
			if block.Type == "tool_result" {
				results = append(results, raw)
			} else {
				rest = append(rest, raw)
			}
		}
		messages[i].Content = append(results, rest...)
	}
	body := map[string]any{"model": request.Model, "max_tokens": request.MaxOutputTokens, "stream": true, "messages": messages}
	if request.System != "" {
		body["system"] = request.System
	}
	if len(request.Tools) > 0 {
		tools := make([]ToolDefinition, len(request.Tools))
		copy(tools, request.Tools)
		for i := range tools {
			if len(tools[i].InputSchema) == 0 {
				tools[i].InputSchema = json.RawMessage(`{"type":"object","properties":{}}`)
			}
		}
		body["tools"] = tools
	}
	return body, nil
}

type messageBlock struct {
	kind                              string
	raw                               json.RawMessage
	fields                            map[string]json.RawMessage
	id, name                          string
	arguments                         strings.Builder
	hasArguments                      bool
	thinking, signature               strings.Builder
	changedThinking, changedSignature bool
}

type messagesUsage struct {
	Input         *int64 `json:"input_tokens"`
	Output        *int64 `json:"output_tokens"`
	CacheCreation *int64 `json:"cache_creation_input_tokens"`
	CacheRead     *int64 `json:"cache_read_input_tokens"`
}

type messagesParser struct {
	incompleteArguments error
	blocks              map[int]*messageBlock
	usage               messagesUsage
	finish              string
}

func (p *messagesParser) parse(event sseEvent) ([]Event, bool, error) {
	if event.name == "ping" {
		return nil, false, nil
	}
	var frame struct {
		Type         string          `json:"type"`
		Index        int             `json:"index"`
		ContentBlock json.RawMessage `json:"content_block"`
		Delta        struct {
			Type        string  `json:"type"`
			Text        string  `json:"text"`
			PartialJSON string  `json:"partial_json"`
			Thinking    string  `json:"thinking"`
			Signature   string  `json:"signature"`
			StopReason  *string `json:"stop_reason"`
		} `json:"delta"`
		Message struct {
			Usage *messagesUsage `json:"usage"`
		} `json:"message"`
		Usage *messagesUsage `json:"usage"`
	}
	if json.Unmarshal([]byte(event.data), &frame) != nil || frame.Type == "" {
		return nil, false, failure(ErrorInvalidResponse, 0, "无法解析 Messages 流事件", "")
	}
	if event.name != "" && event.name != frame.Type {
		return nil, false, failure(ErrorInvalidResponse, 0, "Messages 事件名与类型不一致", "")
	}
	switch frame.Type {
	case "error":
		return nil, false, responseFailure(0, []byte(event.data), "")
	case "message_start":
		return p.updateUsage(frame.Message.Usage), false, nil
	case "message_delta":
		if frame.Delta.StopReason != nil {
			p.finish = *frame.Delta.StopReason
		}
		return p.updateUsage(frame.Usage), false, nil
	case "message_stop":
		if len(p.blocks) != 0 {
			return nil, false, failure(ErrorInvalidResponse, 0, "Messages 在内容块完成前结束", "")
		}
		if p.incompleteArguments != nil && finishEvent(ProtocolMessages, p.finish).Finish.Reason != FinishOutputLimit {
			return nil, false, p.incompleteArguments
		}
		return []Event{finishEvent(ProtocolMessages, p.finish)}, true, nil
	case "content_block_start":
		return p.startBlock(frame.Index, frame.ContentBlock)
	case "content_block_delta":
		block := p.blocks[frame.Index]
		if block == nil {
			return nil, false, failure(ErrorInvalidResponse, 0, "Messages 增量缺少对应内容块", "")
		}
		switch frame.Delta.Type {
		case "text_delta":
			if block.kind != "text" {
				return nil, false, invalidDelta()
			}
			return []Event{{Type: EventText, Text: frame.Delta.Text}}, false, nil
		case "input_json_delta":
			if block.kind != "tool_use" {
				return nil, false, invalidDelta()
			}
			block.hasArguments = true
			block.arguments.WriteString(frame.Delta.PartialJSON)
		case "thinking_delta":
			if block.kind != "thinking" {
				return nil, false, invalidDelta()
			}
			block.changedThinking = true
			block.thinking.WriteString(frame.Delta.Thinking)
		case "signature_delta":
			if block.kind != "thinking" {
				return nil, false, invalidDelta()
			}
			block.changedSignature = true
			block.signature.WriteString(frame.Delta.Signature)
		}
	case "content_block_stop":
		return p.stopBlock(frame.Index)
	}
	// 协议版本允许新增事件和 delta 类型；未核实的增量不解释。
	return nil, false, nil
}

func invalidDelta() *Error {
	return failure(ErrorInvalidResponse, 0, "Messages 增量与内容块类型不一致", "")
}

func (p *messagesParser) startBlock(index int, raw json.RawMessage) ([]Event, bool, error) {
	var initial struct {
		Type      string `json:"type"`
		ID        string `json:"id"`
		Name      string `json:"name"`
		Text      string `json:"text"`
		Thinking  string `json:"thinking"`
		Signature string `json:"signature"`
	}
	if index < 0 || p.blocks[index] != nil || json.Unmarshal(raw, &initial) != nil || initial.Type == "" {
		return nil, false, failure(ErrorInvalidResponse, 0, "Messages 内容块无效", "")
	}
	block := &messageBlock{kind: initial.Type, raw: raw, id: initial.ID, name: initial.Name}
	if json.Unmarshal(raw, &block.fields) != nil {
		return nil, false, invalidDelta()
	}
	block.thinking.WriteString(initial.Thinking)
	block.signature.WriteString(initial.Signature)
	p.blocks[index] = block
	if initial.Type == "text" && initial.Text != "" {
		return []Event{{Type: EventText, Text: initial.Text}}, false, nil
	}
	return nil, false, nil
}

func (p *messagesParser) stopBlock(index int) ([]Event, bool, error) {
	block := p.blocks[index]
	if block == nil {
		return nil, false, failure(ErrorInvalidResponse, 0, "Messages 结束了不存在的内容块", "")
	}
	delete(p.blocks, index)
	switch block.kind {
	case "text":
		return nil, false, nil
	case "tool_use":
		if block.id == "" || block.name == "" {
			return nil, false, failure(ErrorInvalidResponse, 0, "Messages 工具调用缺少标识或名称", "")
		}
		input := block.arguments.String()
		if !block.hasArguments {
			input = string(block.fields["input"])
		}
		// 调研没有确认空 partial_json 的行为；按运行时合同规范为 {}。
		arguments, err := normalizedArguments(input)
		if err != nil {
			p.incompleteArguments = err
			return nil, false, nil
		}
		if arguments[0] != '{' {
			return nil, false, failure(ErrorInvalidResponse, 0, "Messages 工具参数不是 JSON 对象", "")
		}
		return []Event{{Type: EventToolCall, ToolCall: &ToolCall{ID: block.id, Name: block.name, Arguments: arguments}}}, false, nil
	case "thinking":
		// 保留初始块所有字段，只把已核实的 thinking/signature 增量还原到原字段。
		if block.changedThinking {
			block.fields["thinking"], _ = json.Marshal(block.thinking.String())
		}
		if block.changedSignature {
			block.fields["signature"], _ = json.Marshal(block.signature.String())
		}
		if block.changedThinking || block.changedSignature {
			var err error
			block.raw, err = json.Marshal(block.fields)
			if err != nil {
				return nil, false, failure(ErrorInvalidResponse, 0, "无法还原思考块", "")
			}
		}
	}
	return []Event{{Type: EventOpaque, Opaque: &OpaqueBlock{Protocol: ProtocolMessages, Data: block.raw}}}, false, nil
}

func (p *messagesParser) updateUsage(usage *messagesUsage) []Event {
	if usage == nil {
		return nil
	}
	if usage.Input != nil {
		p.usage.Input = usage.Input
	}
	if usage.Output != nil {
		p.usage.Output = usage.Output
	}
	if usage.CacheCreation != nil {
		p.usage.CacheCreation = usage.CacheCreation
	}
	if usage.CacheRead != nil {
		p.usage.CacheRead = usage.CacheRead
	}
	snapshot := &Usage{OutputTokens: p.usage.Output}
	if p.usage.Input != nil {
		total := *p.usage.Input
		if p.usage.CacheCreation != nil {
			total += *p.usage.CacheCreation
		}
		if p.usage.CacheRead != nil {
			total += *p.usage.CacheRead
		}
		snapshot.InputTokens = &total
	}
	return []Event{{Type: EventUsage, Usage: snapshot}}
}
