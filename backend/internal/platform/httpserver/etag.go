package httpserver

import (
	"net/http"
	"strings"
)

// EntityTag 返回强校验值的头部形式（带双引号）。opaque 只能含可见 ASCII 且不含双引号，由调用方保证。
func EntityTag(opaque string) string { return `"` + opaque + `"` }

// IfNoneMatchHits 报告请求的 If-None-Match 是否命中 opaque 标识的当前表示。按 RFC 9110 的弱比较：
// 忽略 W/ 前缀，逐个比较逗号分隔的标签（区分大小写）；"*" 命中任何当前存在的表示，所以调用方要在确认资源存在之后再调用。
// 无法解析的值一律不命中，让请求按正常的 200 处理。
func IfNoneMatchHits(request *http.Request, opaque string) bool {
	for _, value := range request.Header.Values("If-None-Match") {
		if entityTagListHits(value, opaque) {
			return true
		}
	}
	return false
}

func entityTagListHits(value, opaque string) bool {
	value = strings.TrimSpace(value)
	if value == "*" {
		return true
	}
	for {
		value = strings.TrimLeft(value, " \t,")
		if value == "" {
			return false
		}
		value = strings.TrimPrefix(value, "W/")
		if !strings.HasPrefix(value, `"`) {
			return false
		}
		end := strings.IndexByte(value[1:], '"')
		if end < 0 {
			return false
		}
		if value[1:1+end] == opaque {
			return true
		}
		value = value[end+2:]
	}
}
