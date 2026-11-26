package metadata

import (
	"regexp"
	"strings"
)

const (
	formatWord      = `(?i:flac|mp3|alac|wav|ape|web(?:-dl)?|cd[- ]rip|[0-9]+\s*bit)`
	formatRate      = `(?i:[0-9]+\s*kbps|(?:[0-9]+\s*bit[ -]+)?[0-9]+(?:\.[0-9]+)?\s*khz|(?:16|24|32)-(?:44(?:\.1)?|48|88(?:\.2)?|96|176(?:\.4)?|192))`
	formatToken     = `(?:` + formatRate + `|` + formatWord + `)`
	formatSeparator = `[\s/,_-]+`
	formatList      = formatToken + `(?:` + formatSeparator + formatToken + `)*`
)

const enclosedFormat = `(?:\[\s*` + formatList + `\s*\]|\(\s*` + formatList + `\s*\))`
const adjacentFormat = `(?:` + enclosedFormat + `|` + formatToken + `)`

var bracketFormat = regexp.MustCompile(enclosedFormat)
var plainFormat = regexp.MustCompile(`(?:^|\s+)(?:-\s+)?(?:` + adjacentFormat + `(?:` + formatSeparator + adjacentFormat + `)+|` + formatRate + `)(?:$|\s+)`)

// StripFormatNoise 清除独立的格式、介质和码率标记，保留标题内部文字和版本说明。
func StripFormatNoise(value string) string {
	original := value
	for plainFormat.MatchString(value) {
		value = plainFormat.ReplaceAllString(value, " ")
	}
	value = bracketFormat.ReplaceAllString(value, " ")
	if value == original {
		return NormalizeValue(value)
	}
	return NormalizeValue(strings.TrimSuffix(strings.TrimSpace(value), " -"))
}
