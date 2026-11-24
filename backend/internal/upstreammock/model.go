package upstreammock

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
)

func (s *Server) models(r *http.Request, empty bool) response {
	anthropic := strings.HasPrefix(r.URL.Path, "/anthropic/")
	models := []object{}
	for _, name := range s.script.Models {
		if empty {
			break
		}
		model := object{"id": name, "object": "model"}
		if anthropic {
			model = object{"id": name, "type": "model", "display_name": name, "capabilities": object{"server_tools": object{"web_search": object{"supported": false}}}}
		}
		models = append(models, model)
	}
	if anthropic {
		return ok(object{"data": models, "has_more": false})
	}
	return ok(object{"object": "list", "data": models})
}

type frame struct {
	name, data string
	content    bool
}
type stream []frame

func (events *stream) add(name string, body object, content bool) {
	if name != "" {
		body["type"] = name
	}
	raw, _ := json.Marshal(body)
	*events = append(*events, frame{name, string(raw), content})
}

var ids atomic.Uint64

func nextCallID() string { return fmt.Sprintf("call_mock_%d", ids.Add(1)) }

func (s *Server) model(r *http.Request, empty bool) response {
	var input modelInput
	if err := decodeJSON(r.Body, &input); err != nil {
		return problem(400, "模型请求 JSON 无效")
	}
	if empty || !slices.Contains(s.script.Models, input.Model) {
		return missing()
	}
	t := input.turn()
	if t.nativeSearch {
		return response{status: 400, json: object{"error": object{"code": "tool_unsupported", "message": "web_search is not supported"}}}
	}
	reply := s.script.reply(t)
	var events stream
	switch {
	case strings.HasSuffix(r.URL.Path, "/responses"):
		events = responses(reply)
	case strings.HasSuffix(r.URL.Path, "/messages"):
		events = messages(reply)
	default:
		events = chat(reply)
	}
	return response{status: 200, events: events}
}
func responses(rep reply) stream {
	var events stream
	if rep.Text != "" {
		item := object{"type": "message", "role": "assistant", "content": []object{{"type": "output_text", "text": rep.Text}}}
		events.add("response.output_text.delta", object{"delta": rep.Text, "output_index": 0}, true)
		events.add("response.output_item.done", object{"output_index": 0, "item": item}, false)
	}
	for i, c := range rep.Calls {
		args, _ := json.Marshal(c.Arguments)
		item := object{"type": "function_call", "call_id": nextCallID(), "name": c.Name, "arguments": ""}
		events.add("response.output_item.added", object{"output_index": i, "item": item}, false)
		events.add("response.function_call_arguments.delta", object{"output_index": i, "delta": string(args)}, true)
		item["arguments"], item["status"] = string(args), "completed"
		events.add("response.output_item.done", object{"output_index": i, "item": item}, false)
	}
	events.add("response.completed", object{"response": object{"status": "completed", "usage": object{"input_tokens": 12, "output_tokens": 8}}}, false)
	return events
}
func chat(rep reply) stream {
	var events stream
	delta := object{"content": rep.Text}
	reason := "stop"
	if len(rep.Calls) > 0 {
		calls := []object{}
		for i, c := range rep.Calls {
			args, _ := json.Marshal(c.Arguments)
			calls = append(calls, object{"index": i, "type": "function", "id": nextCallID(), "function": object{"name": c.Name, "arguments": string(args)}})
		}
		delta = object{"tool_calls": calls}
		reason = "tool_calls"
	}
	events.add("", object{"choices": []object{{"index": 0, "delta": delta}}}, true)
	events.add("", object{"choices": []object{{"index": 0, "delta": object{}, "finish_reason": reason}}}, false)
	events.add("", object{"choices": []object{}, "usage": object{"prompt_tokens": 12, "completion_tokens": 8}}, false)
	return append(events, frame{data: "[DONE]"})
}
func messages(rep reply) stream {
	var events stream
	events.add("message_start", object{"message": object{"type": "message", "role": "assistant", "content": []object{}, "usage": object{"input_tokens": 12, "output_tokens": 0}}}, false)
	reason := "end_turn"
	block := func(i int, start, delta object) {
		events.add("content_block_start", object{"index": i, "content_block": start}, false)
		events.add("content_block_delta", object{"index": i, "delta": delta}, true)
		events.add("content_block_stop", object{"index": i}, false)
	}
	if rep.Text != "" {
		block(0, object{"type": "text", "text": ""}, object{"type": "text_delta", "text": rep.Text})
	}
	for i, c := range rep.Calls {
		args, _ := json.Marshal(c.Arguments)
		block(i, object{"type": "tool_use", "id": nextCallID(), "name": c.Name, "input": object{}}, object{"type": "input_json_delta", "partial_json": string(args)})
		reason = "tool_use"
	}
	events.add("message_delta", object{"delta": object{"stop_reason": reason}, "usage": object{"output_tokens": 8}}, false)
	events.add("message_stop", object{}, false)
	return events
}
