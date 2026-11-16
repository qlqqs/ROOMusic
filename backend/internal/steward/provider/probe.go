package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
)

// ToolSupport 是通过实际流式请求取得的工具能力结论。
type ToolSupport string

const (
	// ToolSupportSupported 表示完整流中调用了探测工具且结束原因要求执行工具。
	ToolSupportSupported ToolSupport = "supported"
	// ToolSupportUnsupported 表示端点明确报告不支持工具调用。
	ToolSupportUnsupported ToolSupport = "unsupported"
	// ToolSupportUnconfirmed 表示未探测、未调用工具或失败原因不足以判断能力。
	ToolSupportUnconfirmed ToolSupport = "unconfirmed"
)

// Model 是端点模型列表中的模型，不从列表推断客户端工具能力。
type Model struct {
	nativeSearch *bool
	// ID 是请求使用的模型名。
	ID string `json:"id"`
	// DisplayName 是 Messages 可选的展示名称。
	DisplayName string `json:"display_name,omitempty"`
}

// ProbeFailure 保存某一步的失败原因，成功后的早先协议尝试失败也会保留。
type ProbeFailure struct {
	// Step 是 endpoint、models 或 tools。
	Step string `json:"step"`
	// Protocol 是这次尝试的协议。
	Protocol Protocol `json:"protocol,omitempty"`
	// Error 是已去除敏感内容的失败。
	Error *Error `json:"error"`
}

// ProbeResult 是可持久化的探测报告，不包含端点密钥。
type ProbeResult struct {
	NativeSearch       NativeSearchSupport `json:"native_search"`
	ResponsesAvailable bool                `json:"responses_available"`
	// ProviderName 是地址预设识别的服务商名，未知时为空。
	ProviderName string `json:"provider_name,omitempty"`
	// Protocol 是模型列表请求成功的协议，全部失败时为空。
	Protocol Protocol `json:"protocol,omitempty"`
	// Models 是完整模型列表，Messages 自动翻页。
	Models []Model `json:"models"`
	// ToolSupport 是工具调用的三态结论。
	ToolSupport ToolSupport `json:"tool_support"`
	// Failures 保存每一步失败原因。
	Failures []ProbeFailure `json:"failures,omitempty"`
}

// Probe 先按已核实地址猜测协议，未知地址依次尝试 OpenAI、Messages 的模型列表。
// model 为空时只列模型；不为空时使用该模型做一次明确提示调用工具的流式探测。
// 无协议可用或 ctx 取消时返回错误；工具探测失败以报告中的三态和 Failures 表达。
func Probe(ctx context.Context, client *http.Client, endpoint, key, model string) (ProbeResult, error) {
	result := ProbeResult{Models: []Model{}, ToolSupport: ToolSupportUnconfirmed, NativeSearch: NativeSearchUnknown}
	u, err := parseEndpoint(endpoint)
	if err != nil {
		result.addFailure("endpoint", "", err)
		return result, err
	}
	protocols := []Protocol{ProtocolOpenAI, ProtocolMessages}
	if p := matchPreset(u); p != nil {
		result.ProviderName = p.name
		protocols = []Protocol{p.protocol}
		for _, protocol := range []Protocol{ProtocolOpenAI, ProtocolMessages} {
			if protocol != p.protocol {
				protocols = append(protocols, protocol)
			}
		}
	}
	var selected *adapter
	for _, protocol := range protocols {
		candidate, createErr := newAdapter(client, endpoint, key, protocol)
		if createErr != nil {
			result.addFailure("endpoint", protocol, createErr)
			return result, createErr
		}
		models, listErr := candidate.listModels(ctx)
		if listErr != nil {
			err = listErr
			result.addFailure("models", protocol, listErr)
			if ctx.Err() != nil {
				return result, contextFailure(ctx.Err(), 0)
			}
			continue
		}
		selected = candidate
		result.Protocol = protocol
		result.Models = models
		break
	}
	if selected == nil {
		return result, err
	}
	if model == "" {
		return result, nil
	}
	result.ToolSupport, err = selected.probeTools(ctx, model)
	if err != nil {
		result.addFailure("tools", result.Protocol, err)
	}
	if selected.protocol == ProtocolMessages {
		for _, candidate := range result.Models {
			if candidate.ID == model && candidate.nativeSearch != nil {
				result.NativeSearch = NativeSearchUnsupported
				if *candidate.nativeSearch {
					result.NativeSearch = NativeSearchSupported
				}
				break
			}
		}
	} else if ctx.Err() == nil {
		responses := *selected
		responses.protocol = ProtocolResponses
		responseErr := responses.probeResponses(ctx, model)
		if responseErr != nil {
			result.addFailure("responses", ProtocolResponses, responseErr)
		} else {
			result.ResponsesAvailable = true
			result.NativeSearch, responseErr = responses.probeNativeSearch(ctx, model)
			if responseErr != nil {
				result.addFailure("native_search", ProtocolResponses, responseErr)
			}
		}
	}
	if ctx.Err() != nil {
		return result, contextFailure(ctx.Err(), 0)
	}
	return result, nil
}

func (result *ProbeResult) addFailure(step string, protocol Protocol, err error) {
	var typed *Error
	if !errors.As(err, &typed) {
		typed = failure(ErrorOther, 0, err.Error(), "")
	}
	result.Failures = append(result.Failures, ProbeFailure{Step: step, Protocol: protocol, Error: typed})
}

func (a *adapter) listModels(ctx context.Context) ([]Model, error) {
	address, err := endpointURL(a.endpoint, a.protocol, "models")
	if err != nil {
		return nil, err
	}
	models := []Model{}
	remaining := int64(maxResponseBytes)
	seenCursors := map[string]bool{}
	for {
		response, err := a.do(ctx, http.MethodGet, address, nil)
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(response.Body, remaining+1))
		_ = response.Body.Close()
		if ctx.Err() != nil {
			return nil, contextFailure(ctx.Err(), response.StatusCode)
		}
		if isTimeout(err) {
			return nil, failure(ErrorTimeout, response.StatusCode, "读取模型列表超时", "")
		}
		if err != nil {
			return nil, failure(ErrorUnreachable, response.StatusCode, "模型列表读取中断", "")
		}
		remaining -= int64(len(data))
		if remaining < 0 {
			return nil, failure(ErrorInvalidResponse, response.StatusCode, "模型列表超过读取上限", "")
		}
		if hasError(data) {
			return nil, responseFailure(response.StatusCode, data, a.key)
		}
		var page struct {
			Data    []Model `json:"data"`
			HasMore *bool   `json:"has_more"`
			LastID  string  `json:"last_id"`
		}
		if json.Unmarshal(data, &page) != nil || page.Data == nil {
			return nil, failure(ErrorInvalidResponse, response.StatusCode, "无法解析模型列表: "+string(data), a.key)
		}
		// 两种列表都使用 data，但 has_more 是已核实的 Messages 分页标记。
		// 不把这类响应误当作无分页的 OpenAI 列表而静默丢掉后续模型。
		if a.protocol == ProtocolOpenAI && page.HasMore != nil {
			return nil, failure(ErrorInvalidResponse, response.StatusCode, "模型列表带 Messages 分页标记", "")
		}
		for _, model := range page.Data {
			if model.ID == "" {
				return nil, failure(ErrorInvalidResponse, response.StatusCode, "模型列表缺少模型标识", "")
			}
		}
		models = append(models, page.Data...)
		if a.protocol != ProtocolMessages || page.HasMore == nil || !*page.HasMore {
			return models, nil
		}
		if page.LastID == "" || seenCursors[page.LastID] {
			return nil, failure(ErrorInvalidResponse, response.StatusCode, "模型列表分页游标缺失或重复", "")
		}
		seenCursors[page.LastID] = true
		u, _ := url.Parse(address)
		query := u.Query()
		query.Set("after_id", page.LastID)
		u.RawQuery = query.Encode()
		address = u.String()
	}
}

func (a *adapter) probeTools(ctx context.Context, model string) (ToolSupport, error) {
	const name = "roomusic_probe"
	// 1024 沿用调研中的最小示例，并非已验证的所有模型最低值；达到上限仍为未确认。
	stream, err := a.Stream(ctx, Request{
		Model: model, MaxOutputTokens: 1024,
		Messages: []Message{{Role: RoleUser, Content: []ContentBlock{{Type: BlockText, Text: "请立即调用给定的 roomusic_probe 工具一次，参数使用空对象 {}。不要用文本回答。"}}}},
		Tools:    []ToolDefinition{{Name: name, Description: "确认当前端点支持客户端工具调用，不执行任何操作。", InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)}},
	})
	if err != nil {
		return toolFailure(err)
	}
	defer stream.Close()
	called := false
	for {
		event, err := stream.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return ToolSupportUnconfirmed, failure(ErrorOther, 0, "探测流没有工具调用结束事件", "")
			}
			return toolFailure(err)
		}
		if event.Type == EventToolCall && event.ToolCall.Name == name {
			called = true
		}
		if event.Type == EventDone {
			if called && event.Finish.Reason == FinishToolCalls {
				return ToolSupportSupported, nil
			}
			return ToolSupportUnconfirmed, nil
		}
	}
}

func toolFailure(err error) (ToolSupport, error) {
	var typed *Error
	if errors.As(err, &typed) && typed.Kind == ErrorToolUnsupported {
		return ToolSupportUnsupported, err
	}
	return ToolSupportUnconfirmed, err
}

// NativeSearchSupport 是模型自带搜索的能力结论。
type NativeSearchSupport string

const (
	NativeSearchSupported   NativeSearchSupport = "supported"
	NativeSearchUnsupported NativeSearchSupport = "unsupported"
	NativeSearchUnknown     NativeSearchSupport = "unknown"
)

// ValidNativeSearch 校验设置覆盖项，不从模型名称推断能力。
func ValidNativeSearch(value NativeSearchSupport) bool {
	return value == NativeSearchSupported || value == NativeSearchUnsupported || value == NativeSearchUnknown
}

// UnmarshalJSON 只提取当前模型的搜索能力，不将供应方能力字典扩展成设置接口。
func (model *Model) UnmarshalJSON(data []byte) error {
	var wire struct {
		ID           string `json:"id"`
		DisplayName  string `json:"display_name"`
		Capabilities struct {
			ServerTools struct {
				WebSearch struct {
					Supported *bool `json:"supported"`
				} `json:"web_search"`
			} `json:"server_tools"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	model.ID, model.DisplayName, model.nativeSearch = wire.ID, wire.DisplayName, wire.Capabilities.ServerTools.WebSearch.Supported
	return nil
}

func (a *adapter) probeNativeSearch(ctx context.Context, model string) (NativeSearchSupport, error) {
	stream, err := a.Stream(ctx, Request{Model: model, MaxOutputTokens: 1024, NativeSearch: true,
		Messages: []Message{{Role: RoleUser, Content: []ContentBlock{{Type: BlockText, Text: "请使用网页搜索工具搜索 MusicBrainz 官方网站，仅回复其地址。"}}}}})
	if err != nil {
		return nativeSearchFailure(err)
	}
	defer stream.Close()
	supported := false
	failed := false
	for {
		event, err := stream.Next()
		if err != nil {
			return nativeSearchFailure(err)
		}
		if event.Type == EventSearch {
			if event.Search.Status == "completed" {
				supported = true
			} else {
				failed = true
			}
		}
		if event.Type == EventDone {
			if supported {
				return NativeSearchSupported, nil
			}
			if failed {
				return NativeSearchUnknown, failure(ErrorOther, 0, "搜索探测未完成", "")
			}
			return NativeSearchUnknown, nil
		}
	}
}

func nativeSearchFailure(err error) (NativeSearchSupport, error) {
	var typed *Error
	if errors.As(err, &typed) && typed.Kind == ErrorToolUnsupported {
		return NativeSearchUnsupported, err
	}
	return NativeSearchUnknown, err
}

func (a *adapter) probeResponses(ctx context.Context, model string) error {
	stream, err := a.Stream(ctx, Request{Model: model, MaxOutputTokens: 1024,
		Messages: []Message{{Role: RoleUser, Content: []ContentBlock{{Type: BlockText, Text: "请只回复 OK。"}}}}})
	if err != nil {
		return err
	}
	defer stream.Close()
	for {
		event, err := stream.Next()
		if err != nil {
			return err
		}
		if event.Type == EventDone {
			return nil
		}
	}
}
