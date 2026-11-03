package parser

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/transform"
)

func decodeCueText(data []byte) (string, string, error) {
	if bytes.HasPrefix(data, []byte{0x00, 0x00, 0xfe, 0xff}) || bytes.HasPrefix(data, []byte{0xff, 0xfe, 0x00, 0x00}) {
		return "", "", fmt.Errorf("unsupported UTF-32 CUE encoding")
	}
	if bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) {
		if !utf8.Valid(data[3:]) {
			return "", "", fmt.Errorf("unsupported CUE encoding")
		}
		return validatedCueText(string(data[3:]), "utf-8-bom")
	}
	if bytes.HasPrefix(data, []byte{0xff, 0xfe}) || bytes.HasPrefix(data, []byte{0xfe, 0xff}) {
		if len(data[2:])%2 != 0 {
			return "", "", fmt.Errorf("invalid UTF-16 CUE encoding")
		}
		littleEndian := data[0] == 0xff
		units := make([]uint16, (len(data)-2)/2)
		for index := range units {
			if littleEndian {
				units[index] = binary.LittleEndian.Uint16(data[2+index*2:])
			} else {
				units[index] = binary.BigEndian.Uint16(data[2+index*2:])
			}
		}
		if !validUTF16Units(units) {
			return "", "", fmt.Errorf("invalid UTF-16 CUE encoding")
		}
		return validatedCueText(string(utf16.Decode(units)), "utf-16")
	}
	if utf8.Valid(data) {
		return validatedCueText(string(data), "utf-8")
	}
	// 同时尝试现有资料库常见的 GBK、Shift-JIS 和 Big5，并用文本质量稳定裁决；
	// 解码错误不会通过替换字符静默吞掉。
	bestContent := ""
	bestEncoding := ""
	bestScore := math.MinInt
	for candidateIndex, candidate := range []struct {
		name string
		enc  transform.Transformer
	}{
		{name: "gbk", enc: simplifiedchinese.GBK.NewDecoder()},
		{name: "shift-jis", enc: japanese.ShiftJIS.NewDecoder()},
		{name: "big5", enc: traditionalchinese.Big5.NewDecoder()},
	} {
		decoded, _, decodeErr := transform.Bytes(candidate.enc, data)
		if decodeErr != nil || !utf8.Valid(decoded) || strings.ContainsRune(string(decoded), utf8.RuneError) || !validCueText(string(decoded)) {
			continue
		}
		content := string(decoded)
		score := cueDecodedTextScore(content)*10 - candidateIndex
		if score > bestScore {
			bestContent = content
			bestEncoding = candidate.name
			bestScore = score
		}
	}
	if bestContent != "" && bestScore > -1000 {
		return bestContent, bestEncoding, nil
	}
	return "", "", fmt.Errorf("unsupported CUE encoding")
}

func validatedCueText(value, encoding string) (string, string, error) {
	if !validCueText(value) {
		return "", "", fmt.Errorf("CUE contains unsupported control characters")
	}
	return value, encoding, nil
}

func validCueText(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) && character != '\n' && character != '\r' && character != '\t' {
			return false
		}
	}
	return true
}

func validUTF16Units(units []uint16) bool {
	for index := 0; index < len(units); index++ {
		unit := units[index]
		switch {
		case unit >= 0xd800 && unit <= 0xdbff:
			if index+1 >= len(units) || units[index+1] < 0xdc00 || units[index+1] > 0xdfff {
				return false
			}
			index++
		case unit >= 0xdc00 && unit <= 0xdfff:
			return false
		}
	}
	return true
}

func cueDecodedTextScore(value string) int {
	score := 0
	for _, character := range value {
		switch {
		case character == utf8.RuneError:
			score -= 200
		case unicode.IsControl(character) && character != '\n' && character != '\r' && character != '\t':
			score -= 100
		case character >= 0xff61 && character <= 0xff9f:
			// 普通 CUE 中的半角片假名常见于把 GBK 或 Big5 误解成 Shift-JIS。
			score -= 20
		case unicode.In(character, unicode.Hiragana, unicode.Katakana):
			score += 24
		case unicode.In(character, unicode.Han):
			score += 8
			if strings.ContainsRune("專輯藝術樂體國語錄聲華萬與為來時對門風龍學會", character) {
				score += 6
			}
			if strings.ContainsRune("专辑艺术乐体国语录声华万与为来时对门风龙学会", character) {
				score += 6
			}
		case unicode.IsLetter(character) || unicode.IsDigit(character):
			score += 3
		case unicode.IsSpace(character) || unicode.IsPunct(character) || unicode.IsSymbol(character):
			score++
		}
	}
	return score
}

func safeRuneLimit(value string, maxBytes int) int {
	bytesUsed := 0
	runesUsed := 0
	for _, character := range value {
		runeBytes := utf8.RuneLen(character)
		if runeBytes < 0 || bytesUsed+runeBytes > maxBytes {
			break
		}
		bytesUsed += runeBytes
		runesUsed++
	}
	return runesUsed
}

func truncateUTF8Bytes(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	return string([]rune(value)[:safeRuneLimit(value, maxBytes)])
}
