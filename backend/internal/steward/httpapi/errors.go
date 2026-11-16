package httpapi

import (
	"context"
	"errors"
	"net"
	"net/http"

	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/steward/egress"
	"github.com/qlqq/roomusic/backend/internal/steward/memory"
	"github.com/qlqq/roomusic/backend/internal/steward/provider"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
)

type problem struct {
	status        int
	code, message string
}

var (
	notConfiguredProblem = problem{409, "steward_not_configured", "请先配置模型端点、协议与对话模型，或重新填写无法解密的密钥"}
	disabledProblem      = problem{403, "steward_disabled", "管理员已关闭成员使用管家"}
	busyProblem          = problem{409, "conversation_busy", "这个会话正在运行"}
	deniedProblem        = problem{403, "egress_denied", "出站地址不允许"}
	internalProblem      = problem{500, "internal", "管家暂时无法处理请求"}
	databaseProblem      = problem{503, "database_unavailable", "管家数据暂时不可用"}
)

func writeProblem(w http.ResponseWriter, r *http.Request, p problem) {
	httpserver.WriteError(w, r, p.status, p.code, p.message)
}

func storeProblem(err error) problem {
	switch {
	case errors.Is(err, memory.ErrInvalidInput):
		return problem{400, "invalid_input", "记忆内容须为 1 至 500 字，标识须为 UUID"}
	case errors.Is(err, memory.ErrLimitReached):
		return problem{409, "memory_limit_reached", "最多保存 50 条记忆"}
	case errors.Is(err, memory.ErrNotFound):
		return problem{404, "not_found", "记忆不存在"}
	case errors.Is(err, runtime.ErrRunLimit):
		return problem{409, "steward_run_limit", "最多同时运行 3 个会话，请等待或停止一个回答"}
	case errors.Is(err, runtime.ErrRunInvalidated):
		return problem{409, "steward_run_invalidated", "运行凭据已变化，请重新发起"}
	case errors.Is(err, runtime.ErrSupervisorClosed):
		return problem{503, "steward_shutting_down", "管家正在关闭"}
	case errors.Is(err, runtime.ErrReviewNotApplicable):
		return problem{409, "review_not_applicable", "当前会话模式不适用独立审查"}
	case errors.Is(err, runtime.ErrPlanNotFound):
		return problem{404, "plan_not_found", "方案不存在或已处理"}
	case errors.Is(err, runtime.ErrPlanExpired):
		return problem{409, "plan_expired", "方案已过期"}
	case errors.Is(err, runtime.ErrPlanDigestMismatch):
		return problem{409, "plan_digest_mismatch", "方案内容已变化"}
	case errors.Is(err, runtime.ErrNotFound):
		return problem{404, "not_found", "会话或放行项不存在"}
	case errors.Is(err, runtime.ErrConversationBusy):
		return busyProblem
	case errors.Is(err, runtime.ErrNothingToContinue):
		return problem{409, "nothing_to_continue", "这个会话没有需要继续的回答"}
	case errors.Is(err, runtime.ErrInvalidInput):
		return problem{400, "invalid_input", "请求参数无效"}
	default:
		return databaseProblem
	}
}

func providerProblem(kind provider.ErrorKind) problem {
	switch kind {
	case provider.ErrorTimeout:
		return problem{503, "provider_timeout", "模型端点请求超时"}
	case provider.ErrorEgressDenied:
		return deniedProblem
	case provider.ErrorToolUnsupported:
		return problem{422, "provider_no_tool_support", "模型不支持工具调用"}
	case provider.ErrorAuthentication:
		return problem{503, "provider_unreachable", "模型端点认证失败，请检查密钥"}
	case provider.ErrorModelNotFound:
		return problem{503, "provider_unreachable", "模型不存在，请检查模型名称"}
	case provider.ErrorRateLimit:
		return problem{503, "provider_unreachable", "模型端点限流或额度不足"}
	case provider.ErrorInvalidResponse:
		return problem{503, "provider_unreachable", "模型端点响应格式无效"}
	case provider.ErrorStreamInterrupted:
		return problem{503, "provider_unreachable", "模型端点响应中断"}
	case provider.ErrorUnreachable:
		return problem{503, "provider_unreachable", "无法连接模型端点，请检查接口地址"}
	default:
		return problem{503, "provider_unreachable", "无法连接模型端点或完成请求"}
	}
}

func outboundProblem(err error) problem {
	var timeout net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout()) {
		return providerProblem(provider.ErrorTimeout)
	}
	if errors.Is(err, egress.ErrDenied) {
		return deniedProblem
	}
	var failure *provider.Error
	if errors.As(err, &failure) {
		return providerProblem(failure.Kind)
	}
	return providerProblem(provider.ErrorUnreachable)
}

func (h *Handlers) writeStoreError(w http.ResponseWriter, r *http.Request, err error) {
	p := storeProblem(err)
	if p.status >= 500 {
		h.logFailure(r, p.code)
	}
	writeProblem(w, r, p)
}

func (h *Handlers) logFailure(r *http.Request, code string) {
	// 不输出原始错误，数据库错误可能携带请求字段或密文。
	h.logger.ErrorContext(r.Context(), "管家请求失败", "event", "steward.request.failed", "message", "管家请求失败",
		"module", "steward", "request_id", r.Header.Get("X-Request-ID"), "error_code", code)
}

type settingsOutboundError struct{ error }
type settingsSecretError struct{ error }

func (h *Handlers) writeSettingsError(w http.ResponseWriter, r *http.Request, err error) {
	var secret *settingsSecretError
	if errors.As(err, &secret) {
		h.logFailure(r, "internal")
		writeProblem(w, r, internalProblem)
		return
	}
	var outbound *settingsOutboundError
	if errors.As(err, &outbound) {
		writeProblem(w, r, outboundProblem(outbound.error))
		return
	}
	h.writeStoreError(w, r, err)
}
