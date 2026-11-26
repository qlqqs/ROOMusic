package metadata

import (
	"bytes"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
)

// LegacyDecodings 只检查带旧编码字节特征的 Latin-1 文本，保留可无损往返的不同读法。
func LegacyDecodings(raw []byte) []string {
	var values []string
	for _, c := range LegacyReadings(raw) {
		if !slices.Contains(values, c.Value) {
			values = append(values, c.Value)
		}
	}
	return values
}

type LegacyReading struct{ Encoding, Value string }

// SuspectLegacyText 判断是否需要旧编码复核，即使三个候选编码都无法往返也保留原字节。
func SuspectLegacyText(raw []byte) bool {
	for i, b := range raw {
		if b >= 0x80 && (b < 0xa0 || i > 0 && raw[i-1] >= 0x80) {
			return !looksWestern(raw)
		}
	}
	return false
}

func LegacyReadings(raw []byte) []LegacyReading {
	if !SuspectLegacyText(raw) {
		return nil
	}
	var values []LegacyReading
	for _, candidate := range []struct {
		name string
		enc  encoding.Encoding
	}{{"GBK", simplifiedchinese.GBK}, {"Big5", traditionalchinese.Big5}, {"Shift-JIS", japanese.ShiftJIS}} {
		enc := candidate.enc
		value, err := enc.NewDecoder().String(string(raw))
		if err != nil || strings.ContainsRune(value, utf8.RuneError) || strings.IndexFunc(value, unicode.IsControl) >= 0 {
			continue
		}
		if strings.IndexFunc(value, func(r rune) bool { return unicode.In(r, unicode.Han, unicode.Katakana, unicode.Hiragana) }) < 0 {
			continue
		}
		encoded, err := enc.NewEncoder().Bytes([]byte(value))
		if err == nil && bytes.Equal(encoded, raw) {
			values = append(values, LegacyReading{Encoding: candidate.name, Value: value})
		}
	}
	return values
}

// 含 ASCII 字母的西文标签常夹一两个重音字母或排版标点。
func looksWestern(raw []byte) bool {
	hasASCIILetter := false
	highRun := 0
	for _, b := range raw {
		if b < utf8.RuneSelf {
			hasASCIILetter = hasASCIILetter || unicode.IsLetter(rune(b))
			highRun = 0
			continue
		}
		highRun++
		r := charmap.Windows1252.DecodeByte(b)
		if highRun > 2 || !(unicode.Is(unicode.Latin, r) || unicode.IsPunct(r)) {
			return false
		}
	}
	return hasASCIILetter
}
