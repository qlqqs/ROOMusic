package metadata

import (
	"regexp"
	"strings"
)

const formatToken = `(?i:flac|mp3|alac|wav|ape|web(?:-dl)?|cd[- ]rip|[0-9]+\s*kbps|[0-9]+\s*bit(?:[ -]+[0-9]+(?:\.[0-9]+)?\s*khz)?|[0-9]+(?:\.[0-9]+)?\s*khz)`

var bracketFormat = regexp.MustCompile(`\[\s*` + formatToken + `(?:[ /,_-]+` + formatToken + `)*\s*\]`)
var plainFormat = regexp.MustCompile(`(?:^|\s+)(?:-\s+)?` + formatToken + `(?:$|\s+)`)

// StripFormatNoise 清除独立的格式、介质和码率标记，保留标题内部文字和版本说明。
func StripFormatNoise(value string) string {
	original := value
	value = bracketFormat.ReplaceAllString(value, " ")
	for plainFormat.MatchString(value) {
		value = plainFormat.ReplaceAllString(value, " ")
	}
	if value == original {
		return NormalizeValue(value)
	}
	return NormalizeValue(strings.TrimSuffix(strings.TrimSpace(value), " -"))
}
