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
// 此列表按身份过滤；非 read 工具仍由 Invoke 的范围门禁拒绝执行。
func (registry *Registry) List(caller Caller) []Definition {
	definitions := make([]Definition, 0, len(registry.tools))
	for _, registered := range registry.tools {
		if registered.definition.MinRole.Allows(caller.User) {
			definition := cloneDefinition(registered.definition)
			definition.Handler = nil
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
		return Result{}, &Error{Code: CodePermissionDenied, Tool: name, Message: "无权调用此工具"}
	}
	// C6 在此处接入共享写工具的确认路径；任何执行都必须先经过上面的鉴权。
	if definition.Scope != ScopeRead && definition.Scope != ScopePersonal {
		return Result{}, &Error{Code: CodeUnsupportedScope, Tool: name, Message: "此入口不支持该工具的影响范围"}
	}
	value, err := decodeJSON(input)
	if err != nil {
		return Result{}, &Error{Code: CodeInvalidInput, Tool: name, Message: "工具输入必须是合法 JSON"}
	}
	if err := registered.input.validate(value, "$"); err != nil {
		return Result{}, &Error{Code: CodeInvalidInput, Tool: name, Message: err.Error()}
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
	bounded, truncated := boundOutput(output)
	return Result{Output: bounded, Truncated: truncated, Untrusted: definition.Untrusted}, nil
}

func cloneDefinition(definition Definition) Definition {
	definition.Input = bytes.Clone(definition.Input)
	definition.Output = bytes.Clone(definition.Output)
	return definition
}

func boundOutput(output json.RawMessage) (json.RawMessage, bool) {
	if len(output) <= maxOutputBytes {
		return bytes.Clone(output), false
	}
	// 直接切 JSON 会得到无效文档；将文本前缀编码成字符串，且将转义开销计入上限。
	low, high := 0, maxOutputBytes-2
	var bounded []byte
	for low <= high {
		mid := low + (high-low)/2
		end := mid
		for end > 0 && !utf8.RuneStart(output[end]) {
			end--
		}
		encoded, _ := json.Marshal(string(output[:end])) // 字符串编码不会失败。
		if len(encoded) <= maxOutputBytes {
			bounded = encoded
			low = mid + 1
		} else {
			high = mid - 1
		}
	}
	return bounded, true
}
