package provider

import (
	"encoding/json"
	"strings"
)

func responsesRequest(request Request) (map[string]any, error) {
	input := make([]any, 0, len(request.Messages))
	for _, message := range request.Messages {
		if err := validateMessage(message); err != nil {
			return nil, err
		}
		if blocks := protocolBlocks(message, ProtocolResponses); len(blocks) > 0 {
			for _, block := range blocks {
				input = append(input, block)
			}
			continue
		}
		for _, block := range message.Content {
			switch block.Type {
			case BlockText:
				input = append(input, map[string]any{"role": message.Role, "content": block.Text})
			case BlockToolCall:
				call := block.ToolCall
				arguments, err := normalizedArguments(string(call.Arguments))
				if err != nil {
					return nil, err
				}
				input = append(input, map[string]any{"type": "function_call", "call_id": call.ID, "name": call.Name, "arguments": string(arguments)})
			case BlockToolResult:
				input = append(input, map[string]any{"type": "function_call_output", "call_id": block.ToolResult.CallID, "output": block.ToolResult.Content})
			case BlockOpaque:
				if block.Opaque.Protocol == ProtocolResponses {
					input = append(input, block.Opaque.Data)
				}
			}
		}
	}
	// 本地保存会话历史；加密推理项随历史回传，不依赖上游保存的 response_id。
	body := map[string]any{"model": request.Model, "input": input, "stream": true, "store": false}
	include := []string{"reasoning.encrypted_content"}
	if request.System != "" {
		body["instructions"] = request.System
	}
	if request.MaxOutputTokens > 0 {
		body["max_output_tokens"] = request.MaxOutputTokens
	}
	tools := make([]any, 0, len(request.Tools)+1)
	for _, definition := range request.Tools {
		schema := definition.InputSchema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		// 本地工具允许可选字段，校验权威仍在注册表，不让上游自动改成全字段必填。
		tools = append(tools, map[string]any{"type": "function", "name": definition.Name, "description": definition.Description, "parameters": schema, "strict": false})
	}
	if request.NativeSearch && request.ForceTool == "" {
		tools = append(tools, map[string]any{"type": "web_search"})
		include = append(include, "web_search_call.action.sources")
	}
	body["include"] = include
	if len(tools) > 0 {
		body["tools"] = tools
	}
	if request.ForceTool != "" {
		body["tool_choice"] = map[string]any{"type": "function", "name": request.ForceTool}
		body["parallel_tool_calls"] = false
	}
	return body, nil
}

type responsesParser struct {
	pending             map[int]bool
	blocks              map[int]json.RawMessage
	called              bool
	incompleteArguments error
}

func (p *responsesParser) parse(event sseEvent) ([]Event, bool, error) {
	var frame struct {
		Type        string          `json:"type"`
		Delta       string          `json:"delta"`
		OutputIndex int             `json:"output_index"`
		Item        json.RawMessage `json:"item"`
		Response    struct {
			Status            string          `json:"status"`
			Usage             *Usage          `json:"usage"`
			Error             json.RawMessage `json:"error"`
			IncompleteDetails struct {
				Reason string `json:"reason"`
			} `json:"incomplete_details"`
		} `json:"response"`
	}
	if json.Unmarshal([]byte(event.data), &frame) != nil || frame.Type == "" || (event.name != "" && event.name != frame.Type) {
		return nil, false, failure(ErrorInvalidResponse, 0, "无法解析 Responses 流事件", "")
	}
	switch frame.Type {
	case "response.output_text.delta", "response.refusal.delta":
		return []Event{{Type: EventText, Text: frame.Delta}}, false, nil
	case "response.output_item.added":
		if frame.OutputIndex < 0 || p.pending[frame.OutputIndex] {
			return nil, false, invalidDelta()
		}
		if p.pending == nil {
			p.pending = map[int]bool{}
		}
		p.pending[frame.OutputIndex] = true
	case "response.output_item.done":
		delete(p.pending, frame.OutputIndex)
		if frame.OutputIndex < 0 {
			return nil, false, invalidDelta()
		}
		if p.blocks == nil {
			p.blocks = map[int]json.RawMessage{}
		}
		if _, exists := p.blocks[frame.OutputIndex]; exists {
			return nil, false, invalidDelta()
		}
		p.blocks[frame.OutputIndex] = frame.Item
		events, err := p.completeItem(frame.Item)
		if len(events) == 0 && p.incompleteArguments != nil {
			var item struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(frame.Item, &item) == nil && item.Type == "function_call" {
				delete(p.blocks, frame.OutputIndex)
			}
		}
		return events, false, err
	case "response.failed":
		raw, _ := json.Marshal(map[string]any{"error": frame.Response.Error})
		return nil, false, responseFailure(0, raw, "")
	case "error":
		return nil, false, responseFailure(0, []byte(event.data), "")
	case "response.completed", "response.incomplete":
		if len(p.pending) != 0 {
			return nil, false, failure(ErrorInvalidResponse, 0, "Responses 在输出项完成前结束", "")
		}
		finish := Finish{Reason: FinishNormal, RawReason: frame.Response.Status}
		if frame.Type == "response.incomplete" {
			finish.Reason = FinishOther
			finish.RawReason = frame.Response.IncompleteDetails.Reason
			if finish.RawReason == "max_output_tokens" {
				finish.Reason = FinishOutputLimit
			}
		} else if p.called {
			finish.Reason = FinishToolCalls
		}
		if p.incompleteArguments != nil && finish.Reason != FinishOutputLimit {
			return nil, false, p.incompleteArguments
		}
		var events []Event
		if frame.Response.Usage != nil {
			events = append(events, Event{Type: EventUsage, Usage: frame.Response.Usage})
		}
		events = append(events, Event{Type: EventDone, Finish: &finish, ProviderBlocks: orderedBlocks(ProtocolResponses, p.blocks)})
		return events, true, nil
	}
	// 参数增量仅用于传输；output_item.done 含完整参数与加密内容，是回传的权威版本。
	return nil, false, nil
}

func (p *responsesParser) completeItem(raw json.RawMessage) ([]Event, error) {
	var item struct {
		Type      string `json:"type"`
		ID        string `json:"id"`
		CallID    string `json:"call_id"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
		Status    string `json:"status"`
		Content   []struct {
			Type        string `json:"type"`
			Annotations []struct {
				Type string `json:"type"`
				Citation
			} `json:"annotations"`
		} `json:"content"`
		Action struct {
			Type    string     `json:"type"`
			Query   string     `json:"query"`
			Queries []string   `json:"queries"`
			URL     string     `json:"url"`
			Pattern string     `json:"pattern"`
			Sources []Citation `json:"sources"`
		} `json:"action"`
	}
	if json.Unmarshal(raw, &item) != nil || item.Type == "" {
		return nil, failure(ErrorInvalidResponse, 0, "Responses 输出项无效", "")
	}
	switch item.Type {
	case "function_call":
		if item.CallID == "" || item.Name == "" {
			return nil, failure(ErrorInvalidResponse, 0, "Responses 工具调用缺少标识或名称", "")
		}
		if item.Status == "incomplete" {
			p.incompleteArguments = failure(ErrorInvalidResponse, 0, "Responses 工具调用未完成", "")
			return nil, nil
		}
		arguments, err := normalizedArguments(item.Arguments)
		if err != nil {
			p.incompleteArguments = err
			return nil, nil
		}
		p.called = true
		return []Event{{Type: EventToolCall, ToolCall: &ToolCall{ID: item.CallID, Name: item.Name, Arguments: arguments}}}, nil
	case "web_search_call":
		if item.ID == "" {
			return nil, failure(ErrorInvalidResponse, 0, "Responses 搜索缺少标识", "")
		}
		search := &Search{SearchID: item.ID, Query: item.Action.Query, Status: "completed", Results: CleanCitations(item.Action.Sources)}
		if search.Query == "" {
			search.Query = strings.Join(item.Action.Queries, "\n")
		}
		if item.Action.Type == "open_page" || item.Action.Type == "find_in_page" {
			search.Query = item.Action.Pattern
			search.Results = CleanCitations([]Citation{{URL: item.Action.URL}})
		}
		if item.Status != "completed" {
			search.Status, search.ErrorCode = "failed", "search_failed"
		}
		*search = CleanSearch(*search)
		return []Event{{Type: EventSearch, Search: search}}, nil
	case "message":
		var citations []Citation
		for _, content := range item.Content {
			for _, annotation := range content.Annotations {
				if annotation.Type == "url_citation" {
					citations = append(citations, annotation.Citation)
				}
			}
		}
		if citations = CleanCitations(citations); len(citations) > 0 {
			return []Event{{Type: EventCitations, Citations: citations}}, nil
		}
		return nil, nil
	default:
		return []Event{{Type: EventOpaque, Opaque: &OpaqueBlock{Protocol: ProtocolResponses, Data: raw}}}, nil
	}
}
