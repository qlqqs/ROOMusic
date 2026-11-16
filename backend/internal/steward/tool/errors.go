package tool

// ErrorCode 是调用入口及工具处理器的稳定错误分类。
type ErrorCode string

const (
	// CodeTargetNotFound 表示工具读取的目标不存在。
	CodeTargetNotFound ErrorCode = "not_found"
	// CodeNotFound 表示工具没有注册。
	CodeNotFound ErrorCode = "tool_not_found"
	// CodePermissionDenied 表示当前用户不满足工具的最低身份。
	CodePermissionDenied ErrorCode = "permission_denied"
	// CodeUnsupportedScope 表示当前入口不支持工具的影响范围。
	CodeUnsupportedScope ErrorCode = "tool_scope_unsupported"
	// CodeInvalidInput 表示输入不是合法 JSON 或不符合输入 schema。
	CodeInvalidInput ErrorCode = "validation_failed"
)

// Error 是可由 errors.As 识别并按 Code 区分的调用错误。
type Error struct {
	// Code 是稳定分类。
	Code ErrorCode
	// Tool 是被调用的工具名。
	Tool string
	// Message 是可展示的简短失败原因，不包含输入输出或内部信息。
	Message string
}

// Error 返回调用失败的说明。
func (err *Error) Error() string { return err.Message }

// CodeApprovalRequired 表示共享修改等待用户确认。
const CodeApprovalRequired ErrorCode = "approval_required"
