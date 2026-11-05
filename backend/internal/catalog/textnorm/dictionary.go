package textnorm

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxDictionaryLineBytes = 4096

// loadCharacterTables 只采用 TS 第一候选；ST 仅补充未被 TS 覆盖且简体终值唯一的目标。
// 解析全部候选不意味着合并全部边：例如 鍊→炼，鏈→链，炼与链始终不同。
func loadCharacterTables(tsData, stData []byte) (characterTable, error) {
	ts, err := parseDictionary("TSCharacters.txt", tsData)
	if err != nil {
		return nil, err
	}
	st, err := parseDictionary("STCharacters.txt", stData)
	if err != nil {
		return nil, err
	}
	selected := make(characterTable, len(ts))
	for source, candidates := range ts {
		selected[source] = candidates[0]
	}
	resolvedTS, err := resolveCharacters(selected)
	if err != nil {
		return nil, fmt.Errorf("textnorm: TS 第一候选: %w", err)
	}
	additions := make(characterTable)
	conflicts := make(map[rune]bool)
	for simple, candidates := range st {
		canonical := simple
		if terminal, ok := resolvedTS[simple]; ok {
			canonical = terminal
		}
		for _, traditional := range candidates {
			if _, covered := ts[traditional]; covered {
				continue
			}
			if previous, exists := additions[traditional]; exists && previous != canonical {
				conflicts[traditional] = true
			}
			additions[traditional] = canonical
		}
	}
	for traditional, simple := range additions {
		if !conflicts[traditional] {
			selected[traditional] = simple
		}
	}
	table, err := resolveCharacters(selected)
	if err != nil {
		return nil, fmt.Errorf("textnorm: ST 无歧义补充: %w", err)
	}
	return table, nil
}

func parseDictionary(name string, data []byte) (map[rune][]rune, error) {
	if len(data) > maxEmbeddedFileBytes {
		return nil, fmt.Errorf("textnorm: %s 超出字表大小限制", name)
	}
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("textnorm: %s 不是有效 UTF-8", name)
	}
	entries := make(map[rune][]rune)
	for index, lineBytes := range bytes.Split(data, []byte{'\n'}) {
		line := strings.TrimSuffix(string(lineBytes), "\r")
		if len(line) > maxDictionaryLineBytes {
			return nil, dictionaryError(name, index, "行超出大小限制")
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.Count(line, "\t") != 1 {
			return nil, dictionaryError(name, index, "必须恰好一个 TAB 分隔符")
		}
		key, values, _ := strings.Cut(line, "\t")
		source, ok := dictionaryScalar(key)
		if !ok {
			return nil, dictionaryError(name, index, "键必须是单个非空白 Unicode scalar")
		}
		if _, exists := entries[source]; exists {
			return nil, dictionaryError(name, index, "重复键")
		}
		var candidates []rune
		for _, value := range strings.Split(values, " ") {
			target, ok := dictionaryScalar(value)
			if !ok {
				return nil, dictionaryError(name, index, "每个候选必须是单个非空白 Unicode scalar")
			}
			candidates = append(candidates, target)
		}
		entries[source] = candidates
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("textnorm: %s 不含字表条目", name)
	}
	return entries, nil
}

func dictionaryScalar(value string) (rune, bool) {
	r, size := utf8.DecodeRuneInString(value)
	return r, len(value) > 0 && size == len(value) && !unicode.IsSpace(r) && !unicode.IsControl(r)
}

func dictionaryError(name string, index int, reason string) error {
	// 只报告固定文件名、行号和原因，不回显源文本或整行内容。
	return fmt.Errorf("textnorm: %s 第 %d 行: %s", name, index+1, reason)
}

func resolveCharacters(selected characterTable) (characterTable, error) {
	keys := make([]rune, 0, len(selected))
	for key := range selected {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	resolved := make(characterTable, len(selected))
	visiting := make(map[rune]bool)
	for _, source := range keys {
		current := source
		var path []rune
		var terminal rune
		for {
			if known, ok := resolved[current]; ok {
				terminal = known
				break
			}
			next, mapped := selected[current]
			if !mapped || next == current {
				terminal = current
				break
			}
			if visiting[current] {
				return nil, fmt.Errorf("字表含非自映射循环（U+%04X）", current)
			}
			visiting[current] = true
			path = append(path, current)
			current = next
		}
		resolved[current] = terminal
		for _, step := range path {
			resolved[step] = terminal
		}
	}
	// 不将原本无映射的终点误计为 TS 覆盖项。
	result := make(characterTable, len(selected))
	for _, key := range keys {
		result[key] = resolved[key]
	}
	return result, nil
}
