package egress

import "errors"

// ErrDenied 表示出站请求被策略拒绝，可通过 errors.Is 识别。
var ErrDenied = errors.New("出站请求被策略拒绝")

// ErrorCode 是出站失败的稳定原因类别。
type ErrorCode string

const (
	// CodeAddressNotAllowed 表示实际连接地址不符合公网或放行规则。
	CodeAddressNotAllowed ErrorCode = "address_not_allowed"
	// CodeSchemeNotAllowed 表示请求协议不是 HTTP 或 HTTPS。
	CodeSchemeNotAllowed ErrorCode = "scheme_not_allowed"
	// CodeTooManyRedirects 表示重定向次数超过配置上限。
	CodeTooManyRedirects ErrorCode = "too_many_redirects"
	CodeProxyUnavailable ErrorCode = "proxy_unavailable"
	CodeProxyAuth        ErrorCode = "proxy_auth"
)

// Error 是可由 errors.As 识别的策略错误，不保存 URL、请求头或响应内容。
type Error struct {
	// Code 是拒绝原因类别。
	Code ErrorCode
}

// Error 返回不含请求数据的错误说明。
func (err *Error) Error() string {
	switch err.Code {
	case CodeProxyUnavailable:
		return "代理不可用"
	case CodeProxyAuth:
		return "代理认证失败"
	case CodeAddressNotAllowed:
		return "出站地址不允许"
	case CodeSchemeNotAllowed:
		return "出站协议不允许"
	case CodeTooManyRedirects:
		return "出站重定向过多"
	default:
		return ErrDenied.Error()
	}
}

// Unwrap 仅将地址与重定向策略错误标记为拒绝。
func (err *Error) Unwrap() error {
	if err.Code == CodeProxyUnavailable || err.Code == CodeProxyAuth {
		return nil
	}
	return ErrDenied
}
