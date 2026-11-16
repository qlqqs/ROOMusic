// Package tool 定义管家工具合同、启动时注册表与逐次鉴权的调用入口。
package tool

import (
	"context"
	"encoding/json"

	"github.com/qlqq/roomusic/backend/internal/identity"
)

// Role 是工具要求的最低身份。
type Role string

const (
	// RoleMember 允许成员和管理员；成员在 identity 中的角色值是 user。
	RoleMember Role = "user"
	// RoleAdmin 只允许管理员。
	RoleAdmin Role = "admin"
)

// Allows 判断当前已认证用户是否满足最低身份，不保存此前调用的鉴权结果。
func (role Role) Allows(user identity.User) bool {
	switch role {
	case RoleMember:
		return user.Role == "user" || user.Role == "admin"
	case RoleAdmin:
		return user.Role == "admin"
	default:
		return false
	}
}

// Scope 表示工具的影响范围。
type Scope string

const (
	// ScopeRead 表示只读工具。
	ScopeRead Scope = "read"
	// ScopePersonal 表示只影响调用者自己的工具。
	ScopePersonal Scope = "personal"
	// ScopeShared 表示影响共享曲库或配置的工具。
	ScopeShared Scope = "shared"
)

// SourceChat 表示由聊天入口发起调用。
const SourceChat = "chat"

// SourceMCP 表示由 MCP 入口发起调用。
const SourceMCP = "mcp"

// Caller 携带入口提供的当前已认证用户与调用来源。
// 入口负责取得当前用户，工具处理器仍须按 User 限定具体资源的可见范围。
type Caller struct {
	// 以下字段只由已认证的后端入口与运行时设置，不从工具参数读取。
	SharedExecution bool
	ConversationID  string
	PlanID          string
	Mode            string
	TokenID         string
	IdempotencyKey  string
	RequestID       string

	// User 是本次调用的当前用户。
	User identity.User
	// Source 是发起调用的入口。
	Source string
}

// Handler 执行已经通过身份、范围和输入校验的工具，沿用调用方的取消上下文。
// 返回值必须是合法 JSON，且由业务工具移除 ADR 0005 禁止发给模型的字段。
type Handler func(ctx context.Context, caller Caller, input json.RawMessage) (json.RawMessage, error)

// Change 是确认卡片中的字段变化。
type Change struct {
	Field  string `json:"field"`
	Before any    `json:"before"`
	After  any    `json:"after"`
}

// Preview 是只读计算的展示快照，执行器仍负责最终校验。
type Preview struct {
	Target  string   `json:"target"`
	Changes []Change `json:"changes"`
	Summary string   `json:"summary"`
}

// Basis 只说明模型提供的依据，不授予执行权限。
type Basis struct {
	Source string `json:"source"`
	Note   string `json:"note"`
}

// Definition 是业务包提供并由 app 注册的工具合同。
// 两个 schema 都仅支持 type、properties、required、additionalProperties: false、
// enum、items、minimum、maximum、minLength、maxLength；不支持的关键字在注册时拒绝。
type Definition struct {
	Preview func(context.Context, Caller, json.RawMessage) (Preview, error)

	// Name 是唯一工具名。
	Name string
	// Description 是展示给模型和客户端的说明。
	Description string
	// Input 是输入的 JSON Schema 对象。
	Input json.RawMessage
	// Output 是输出的 JSON Schema 对象；处理器负责满足此合同。
	Output json.RawMessage
	// MinRole 是最低身份。
	MinRole Role
	// Scope 是影响范围。
	Scope Scope
	// Revertible 表示操作是否可撤销。
	Revertible bool
	// Untrusted 表示输出是否含标签、歌词、文件名等不可信文本。
	Untrusted bool
	// Handler 是经调用入口校验后执行的处理器。
	Handler Handler
}

// Result 是一次成功调用的有界结果。
type Result struct {
	// Output 是完整 JSON；截断时改为承载原始 JSON 文本前缀的 JSON 字符串。
	// 截断结果不再符合工具声明的输出 schema，读取方必须同时检查 Truncated。
	Output json.RawMessage `json:"output"`
	// Truncated 表示输出是否因大小上限而截断。
	Truncated bool `json:"truncated"`
	// Untrusted 沿用工具声明；固定来源标记由运行时包装。
	Untrusted bool `json:"untrusted"`
}
