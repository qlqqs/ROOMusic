package releasesql

import (
	"database/sql"
	"encoding/json"
	"fmt"
	pathpkg "path"
	"strings"
	"unicode/utf8"
)

const (
	MaxEvidenceCandidates  = 20
	MaxEvidenceStringBytes = 500
)

func DecodeBoundedStringArray(raw string, limit int, sourceRefs bool) ([]string, bool, error) {
	var decoded []string
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil, false, fmt.Errorf("decode evidence string array: %w", err)
	}
	result := make([]string, 0, min(len(decoded), limit))
	truncated := len(decoded) > limit
	for _, value := range decoded {
		if len(result) == limit {
			truncated = true
			break
		}
		if sourceRefs {
			safe, ok := SafeRelativeSourceRef(value)
			if !ok {
				truncated = true
				continue
			}
			normalizedInput := strings.TrimSpace(strings.ReplaceAll(value, `\`, "/"))
			truncated = truncated || safe != normalizedInput
			result = append(result, safe)
			continue
		}
		bounded := BoundedDisplayString(value)
		if bounded == "" {
			truncated = true
			continue
		}
		truncated = truncated || bounded != value
		result = append(result, bounded)
	}
	return result, truncated, nil
}

func SafeRelativeSourceRef(value string) (string, bool) {
	value = strings.TrimSpace(strings.ReplaceAll(value, `\`, "/"))
	value = BoundedDisplayString(value)
	if value == "" || strings.ContainsRune(value, 0) || strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return "", false
	}
	firstSegment := strings.SplitN(value, "/", 2)[0]
	if strings.Contains(firstSegment, ":") {
		return "", false
	}
	cleaned := pathpkg.Clean(value)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", false
	}
	return cleaned, cleaned != ""
}

func SplitBoundedReasonCodes(raw string) ([]string, bool) {
	if strings.TrimSpace(raw) == "" {
		return []string{}, false
	}
	parts := strings.Split(raw, ";")
	result := make([]string, 0, min(len(parts), MaxEvidenceCandidates))
	truncated := len(parts) > MaxEvidenceCandidates
	for _, part := range parts {
		if len(result) == MaxEvidenceCandidates {
			break
		}
		part = BoundedDisplayString(strings.TrimSpace(part))
		if part != "" {
			result = append(result, part)
		}
	}
	return result, truncated
}

func BoundedDisplayString(value string) string {
	value = strings.TrimSpace(strings.Map(func(character rune) rune {
		if character < 0x20 || character == 0x7f {
			return -1
		}
		return character
	}, value))
	if len(value) <= MaxEvidenceStringBytes {
		return value
	}
	value = value[:MaxEvidenceStringBytes]
	for value != "" && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func SafeSourceLabel(relativePath string) string {
	relativePath = strings.ReplaceAll(relativePath, `\`, "/")
	label := BoundedDisplayString(pathpkg.Base(relativePath))
	if label == "" || label == "." || label == "/" {
		return "未知来源"
	}
	return label
}

func StringPointerFromNull(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}

func IntPointerFromNull(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}
	result := int(value.Int64)
	return &result
}
