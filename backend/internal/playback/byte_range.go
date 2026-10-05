package playback

import (
	"strconv"
	"strings"
)

// RangeStatus 是对 Range 请求头的预判结果。真正的 206 响应、If-Range 与 Content-Range 由 http.ServeContent 处理；
// 这里只拒绝它不会按本项目合同处理的请求（多范围会让它生成 multipart，语法错误它只会写纯文本 416）。
type RangeStatus int

const (
	// RangeAbsent 表示没有 Range 头，整份内容照常响应。
	RangeAbsent RangeStatus = iota
	// RangeSingle 表示恰好一个语法正确且可满足的 bytes 范围，交给 ServeContent。
	RangeSingle
	// RangeInvalid 表示语法错误、不是 bytes 单位或有多个范围；应返回 416。
	RangeInvalid
	// RangeUnsatisfiable 表示语法正确，但没有任何字节落在内容之内；应返回 416 与 Content-Range: bytes */size。
	RangeUnsatisfiable
)

// CheckRange 预判 Range 头（空字符串表示没有该头）对大小为 size 的内容是否可以交给 ServeContent。
// 只接受一个 bytes 范围：`bytes=N-M`、`bytes=N-`、`bytes=-N`。
func CheckRange(header string, size int64) RangeStatus {
	if header == "" {
		return RangeAbsent
	}
	specifier, found := strings.CutPrefix(header, "bytes=")
	if !found || strings.Contains(specifier, ",") {
		return RangeInvalid
	}
	specifier = strings.TrimSpace(specifier)
	startText, endText, found := strings.Cut(specifier, "-")
	if !found {
		return RangeInvalid
	}
	startText, endText = strings.TrimSpace(startText), strings.TrimSpace(endText)
	if startText == "" {
		// 后缀范围：最后 N 个字节，N 必须是正数；内容为空时没有任何字节可取。
		length, err := parseByteCount(endText)
		if err != nil || length == 0 {
			return RangeInvalid
		}
		if size == 0 {
			return RangeUnsatisfiable
		}
		return RangeSingle
	}
	start, err := parseByteCount(startText)
	if err != nil {
		return RangeInvalid
	}
	if endText != "" {
		end, err := parseByteCount(endText)
		if err != nil || end < start {
			return RangeInvalid
		}
	}
	if start >= size {
		return RangeUnsatisfiable
	}
	return RangeSingle
}

// parseByteCount 只接受十进制数字（拒绝符号、空白与十六进制），防止 "+5"、"0x10" 这类 strconv 之外的变体被放过。
func parseByteCount(text string) (int64, error) {
	if text == "" {
		return 0, strconv.ErrSyntax
	}
	for _, character := range text {
		if character < '0' || character > '9' {
			return 0, strconv.ErrSyntax
		}
	}
	return strconv.ParseInt(text, 10, 64)
}
