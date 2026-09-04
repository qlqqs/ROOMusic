package main

import (
	"sort"
	"strings"
)

// artistSeparatorRules 与已确认的 V0 scanner 保持同一组保守分隔语义。
// 它只解释明确的 credit 分隔符；已知固定组合名会在紧凑分隔规则之前保留整体。
var artistSeparatorRules = []string{"; ", "；", " / ", " feat. ", " ft. ", " with ", "、"}

var knownFixedArtistGroups = map[string]bool{
	"simon & garfunkel":  true,
	"earth, wind & fire": true,
	"ac/dc":              true,
}

type officialArtistAliasEntry struct {
	canonical string
	aliases   []string
}

// officialArtistAliasEntries 只包含 V0 已固化的高确定性官方/准官方别名。
// 罗马字名或不同文字形式不能靠字符串相似度推断。
var officialArtistAliasEntries = []officialArtistAliasEntry{
	{canonical: "周杰伦", aliases: []string{"周杰伦", "周杰倫", "Jay Chou"}},
}

var officialArtistAliasIndex = func() map[string]string {
	index := make(map[string]string)
	for _, entry := range officialArtistAliasEntries {
		for _, alias := range entry.aliases {
			if key := artistLookupKey(alias); key != "" {
				index[key] = entry.canonical
			}
		}
	}
	return index
}()

func splitArtistNames(raw string) []string {
	raw = boundedAudioText(raw)
	if raw == "" {
		return nil
	}
	for _, separator := range artistSeparatorRules {
		if parts := splitArtistBySeparator(raw, separator); len(parts) > 1 {
			return flattenArtistNames(parts)
		}
	}

	if index := strings.Index(raw, " & "); index > 0 {
		left := strings.TrimSpace(raw[:index])
		right := strings.TrimSpace(raw[index+3:])
		if len(left) >= 2 && len(right) >= 2 && strings.ContainsRune(left, ' ') && strings.ContainsRune(right, ' ') {
			return flattenArtistNames([]string{left, right})
		}
	}
	if isKnownFixedArtistGroup(raw) {
		return []string{raw}
	}
	for _, separator := range []string{"/", ",", "&"} {
		parts := splitArtistBySeparator(raw, separator)
		if len(parts) <= 1 {
			continue
		}
		if separator == "&" && isLikelyFixedAmpersandGroup(parts) {
			return []string{raw}
		}
		return flattenArtistNames(parts)
	}
	return []string{raw}
}

func canonicalArtistNames(raw string) []string {
	parts := splitArtistNames(raw)
	result := make([]string, 0, len(parts))
	seen := make(map[string]bool, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if alias, ok := officialArtistAliasIndex[artistLookupKey(name)]; ok {
			name = alias
		}
		key := artistLookupKey(name)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, name)
	}
	return result
}

func canonicalTrackArtist(raw string) string {
	names := canonicalArtistNames(raw)
	sort.Strings(names)
	return strings.Join(names, " / ")
}

func flattenArtistNames(parts []string) []string {
	flattened := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if isKnownFixedArtistGroup(part) {
			flattened = append(flattened, part)
			continue
		}
		nested := splitNestedArtistName(part)
		if len(nested) <= 1 {
			flattened = append(flattened, part)
			continue
		}
		flattened = append(flattened, flattenArtistNames(nested)...)
	}
	if len(flattened) == 0 {
		return parts
	}
	return flattened
}

func splitNestedArtistName(raw string) []string {
	if isKnownFixedArtistGroup(raw) {
		return []string{raw}
	}
	for _, separator := range artistSeparatorRules {
		if parts := splitArtistBySeparator(raw, separator); len(parts) > 1 {
			return parts
		}
	}
	for _, separator := range []string{"/", ",", "&"} {
		parts := splitArtistBySeparator(raw, separator)
		if len(parts) <= 1 {
			continue
		}
		if separator == "&" && isLikelyFixedAmpersandGroup(parts) {
			return []string{raw}
		}
		return parts
	}
	return []string{raw}
}

func splitArtistBySeparator(raw, separator string) []string {
	if !strings.Contains(raw, separator) {
		return nil
	}
	parts := strings.Split(raw, separator)
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	if len(result) <= 1 {
		return nil
	}
	return result
}

func isLikelyFixedAmpersandGroup(parts []string) bool {
	if len(parts) != 2 {
		return false
	}
	left, right := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	return left != "" && right != "" && isKnownFixedArtistGroup(left+" & "+right)
}

func isKnownFixedArtistGroup(raw string) bool {
	return knownFixedArtistGroups[strings.ToLower(strings.TrimSpace(raw))]
}

func artistLookupKey(raw string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(raw)), " "))
}
