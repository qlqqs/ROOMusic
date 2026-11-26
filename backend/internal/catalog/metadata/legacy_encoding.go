package metadata

import (
	"bytes"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
)

// LegacyDecodings 只检查带旧编码字节特征的 Latin-1 文本，保留可无损往返的不同读法。
func LegacyDecodings(raw []byte) []string {
	suspect := false
	for i, b := range raw {
		if b >= 0x80 && (b < 0xa0 || i > 0 && raw[i-1] >= 0x80) {
			suspect = true
			break
		}
	}
	if !suspect {
		return nil
	}
	var values []string
	for _, enc := range []encoding.Encoding{simplifiedchinese.GBK, traditionalchinese.Big5, japanese.ShiftJIS} {
		value, err := enc.NewDecoder().String(string(raw))
		if err != nil || strings.ContainsRune(value, utf8.RuneError) || strings.IndexFunc(value, unicode.IsControl) >= 0 {
			continue
		}
		if strings.IndexFunc(value, func(r rune) bool { return unicode.In(r, unicode.Han, unicode.Katakana, unicode.Hiragana) }) < 0 {
			continue
		}
		encoded, err := enc.NewEncoder().Bytes([]byte(value))
		if err == nil && bytes.Equal(encoded, raw) && !slices.Contains(values, value) {
			values = append(values, value)
		}
	}
	return values
}
