package catalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// ArtistRef 的 Name 是当前位置的署名原文，DisplayName 才是艺人的有效主显示名。
// 同名不代表同一身份；作用域拆分后的 ArtistID 由有效署名关系给出。
type ArtistRef struct {
	ArtistID    string `json:"artist_id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
}

const maxArtistRefs = 100

func decodeArtistRefs(data []byte) ([]ArtistRef, error) {
	// 姓名上限 4096 字节；JSON 转义最坏可放大为六倍，再加固定字段预算。
	if len(data) == 0 || len(data) > maxArtistRefs*(2*4096*6+256) || !utf8.Valid(data) {
		return nil, errors.New("invalid artist refs projection size or encoding")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var result []ArtistRef
	if err := decoder.Decode(&result); err != nil {
		return nil, fmt.Errorf("decode artist refs: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("trailing artist refs projection")
	}
	if result == nil || len(result) > maxArtistRefs {
		return nil, errors.New("artist refs projection is null or exceeds bound")
	}
	seen := make(map[string]bool, len(result))
	for _, ref := range result {
		if !uuid.Valid(ref.ArtistID) || !validArtistRefName(ref.Name) || !validArtistRefName(ref.DisplayName) {
			return nil, errors.New("invalid artist ref identity or name")
		}
		key := ref.ArtistID + "\x00" + ref.Name
		if seen[key] {
			return nil, errors.New("duplicate artist ref projection")
		}
		seen[key] = true
	}
	return result, nil
}

func validArtistRefName(name string) bool {
	return strings.TrimSpace(name) != "" && len(name) <= 4096 && utf8.ValidString(name) && strings.IndexFunc(name, unicode.IsControl) < 0
}
