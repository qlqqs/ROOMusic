package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// maxOutputBytes 是单个工具结果的 JSON 编码上限（64 KiB），不含 Result 的元数据。
const maxOutputBytes = 64 * 1024

type registeredTool struct {
	definition Definition
	input      *schema
}

// Registry 保存已注册工具；零值可用。
// 注册只在启动时串行完成，此后只读，List 和 Invoke 可并发调用；不支持运行中注册。
// 处理器的并发安全由所属业务包负责。
type Registry struct {
	tools map[string]registeredTool
}

// NewRegistry 创建空注册表。
func NewRegistry() *Registry { return &Registry{} }

// Register 校验并登记工具；失败不改变注册表，成功后持有 schema 的独立副本。
func (registry *Registry) Register(definition Definition) error {
	if strings.TrimSpace(definition.Name) == "" {
		return fmt.Errorf("tool name must not be empty")
	}
	if _, exists := registry.tools[definition.Name]; exists {
		return fmt.Errorf("tool %q is already registered", definition.Name)
	}
	if definition.MinRole != RoleMember && definition.MinRole != RoleAdmin {
		return fmt.Errorf("tool %q has an invalid minimum role", definition.Name)
	}
	if definition.Scope != ScopeRead && definition.Scope != ScopePersonal && definition.Scope != ScopeShared {
		return fmt.Errorf("tool %q has an invalid scope", definition.Name)
	}
	if definition.Handler == nil {
		return fmt.Errorf("tool %q has no handler", definition.Name)
	}
	if definition.MaxOutputBytes != 0 && (definition.Scope != ScopeRead || definition.MaxOutputBytes < maxOutputBytes) {
		return fmt.Errorf("tool %q has an invalid output limit", definition.Name)
	}
	input, err := compileSchema(definition.Input)
	if err != nil {
		return fmt.Errorf("tool %q input schema: %w", definition.Name, err)
	}
	if _, err := compileSchema(definition.Output); err != nil {
		return fmt.Errorf("tool %q output schema: %w", definition.Name, err)
	}
	definition = cloneDefinition(definition)
	if registry.tools == nil {
		registry.tools = make(map[string]registeredTool)
	}
	registry.tools[definition.Name] = registeredTool{definition: definition, input: input}
	return nil
}

// List 按名称排序返回当前用户有权调用的工具合同副本，不返回处理器。
// 此列表按身份和当前可用性过滤；共享写入仍由 Invoke 的审批门禁决定是否执行。
func (registry *Registry) List(caller Caller) []Definition {
	return registry.ListContext(context.Background(), caller)
}

// ListContext 在请求上下文中按身份与当前设置筛选工具。
func (registry *Registry) ListContext(ctx context.Context, caller Caller) []Definition {
	definitions := make([]Definition, 0, len(registry.tools))
	for _, registered := range registry.tools {
		if registered.definition.MinRole.Allows(caller.User) && (registered.definition.Available == nil || registered.definition.Available(ctx, caller)) {
			definition := cloneDefinition(registered.definition)
			definition.Handler = nil
			definition.Preview = nil
			definition.Available = nil
			definitions = append(definitions, definition)
		}
	}
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].Name < definitions[j].Name })
	return definitions
}

// Invoke 逐次鉴权、检查工具范围、校验输入后执行，并返回有界输出。
// 调用入口错误返回 *Error，处理器错误保留原始错误链；不缓存用户权限。
func (registry *Registry) Invoke(ctx context.Context, caller Caller, name string, input json.RawMessage) (Result, error) {
	registered, exists := registry.tools[name]
	if !exists {
		return Result{}, &Error{Code: CodeNotFound, Tool: name, Message: "工具不存在"}
	}
	definition := registered.definition
	if !definition.MinRole.Allows(caller.User) {
		return Result{}, definition.RoleDeniedError()
	}
	if definition.Available != nil && !definition.Available(ctx, caller) {
		return Result{}, &Error{Code: CodePermissionDenied, Tool: name, Message: "无权调用此工具"}
	}
	if definition.Scope == ScopeShared && !caller.SharedExecution {
		if definition.Preview == nil {
			return Result{}, &Error{Code: CodeUnsupportedScope, Tool: name, Message: "工具不支持确认预览"}
		}
		return Result{}, &Error{Code: CodeApprovalRequired, Tool: name, Message: "需要用户确认"}
	}
	inputCode := definition.InvalidInputCode
	if inputCode == "" {
		inputCode = CodeInvalidInput
	}
	value, err := decodeJSON(input)
	if err != nil {
		return Result{}, &Error{Code: inputCode, Tool: name, Message: "工具输入必须是合法 JSON"}
	}
	if err := registered.input.validate(value, "$"); err != nil {
		return Result{}, &Error{Code: inputCode, Tool: name, Message: err.Error()}
	}
	if definition.Scope == ScopeShared && containsNUL(value) {
		return Result{}, &Error{Code: inputCode, Tool: name, Message: "工具输入不能包含空字符"}
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	output, err := definition.Handler(ctx, caller, input)
	if err != nil {
		return Result{}, fmt.Errorf("invoke tool %q: %w", name, err)
	}
	if !json.Valid(output) {
		return Result{}, fmt.Errorf("tool %q returned invalid JSON", name)
	}
	limit := definition.MaxOutputBytes
	if limit == 0 {
		limit = maxOutputBytes
	}
	bounded, truncated := boundOutput(output, limit)
	return Result{Output: bounded, Truncated: truncated, Untrusted: definition.Untrusted}, nil
}

// BoundResult 为执行结果与已提交操作的对账结果应用同一输出上限。
func BoundResult(output json.RawMessage, untrusted bool) Result {
	bounded, truncated := boundOutput(output, maxOutputBytes)
	return Result{Output: bounded, Truncated: truncated, Untrusted: untrusted}
}

func cloneDefinition(definition Definition) Definition {
	definition.Input = bytes.Clone(definition.Input)
	definition.Output = bytes.Clone(definition.Output)
	return definition
}

func boundOutput(output json.RawMessage, limit int) (json.RawMessage, bool) {
	if len(output) <= limit {
		return bytes.Clone(output), false
	}
	// 直接切 JSON 会得到无效文档；将文本前缀编码成字符串，且将转义开销计入上限。
	low, high := 0, limit-2
	var bounded []byte
	for low <= high {
		mid := low + (high-low)/2
		end := mid
		for end > 0 && !utf8.RuneStart(output[end]) {
			end--
		}
		encoded, _ := json.Marshal(string(output[:end])) // 字符串编码不会失败。
		if len(encoded) <= limit {
			bounded = encoded
			low = mid + 1
		} else {
			high = mid - 1
		}
	}
	return bounded, true
}

// PreviewCall 在生成方案前逐次鉴权、校验输入并读取展示快照。
func (registry *Registry) PreviewCall(ctx context.Context, caller Caller, name string, input json.RawMessage) (Preview, error) {
	registered, exists := registry.tools[name]
	if !exists {
		return Preview{}, &Error{Code: CodeNotFound, Tool: name, Message: "工具不存在"}
	}
	if !registered.definition.MinRole.Allows(caller.User) {
		return Preview{}, registered.definition.RoleDeniedError()
	}
	if registered.definition.Available != nil && !registered.definition.Available(ctx, caller) {
		return Preview{}, &Error{Code: CodePermissionDenied, Tool: name, Message: "无权调用此工具"}
	}
	value, err := decodeJSON(input)
	if err != nil {
		return Preview{}, &Error{Code: CodeInvalidInput, Tool: name, Message: "工具输入必须是合法 JSON"}
	}
	if err := registered.input.validate(value, "$"); err != nil {
		return Preview{}, &Error{Code: CodeInvalidInput, Tool: name, Message: err.Error()}
	}
	if containsNUL(value) {
		return Preview{}, &Error{Code: CodeInvalidInput, Tool: name, Message: "工具输入不能包含空字符"}
	}
	if registered.definition.Preview == nil {
		return Preview{}, &Error{Code: CodeUnsupportedScope, Tool: name, Message: "工具不支持预览"}
	}
	return registered.definition.Preview(ctx, caller, input)
}

// 方案输入存为 jsonb；检查解码后的键和值，保留字面量反斜杠加 u0000 的合法文本。
func containsNUL(value any) bool {
	switch value := value.(type) {
	case string:
		return strings.ContainsRune(value, '\x00')
	case []any:
		for _, item := range value {
			if containsNUL(item) {
				return true
			}
		}
	case map[string]any:
		for key, item := range value {
			if strings.ContainsRune(key, '\x00') || containsNUL(item) {
				return true
			}
		}
	}
	return false
}
