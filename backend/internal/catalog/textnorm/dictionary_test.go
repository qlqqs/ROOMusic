package textnorm

import (
	"strings"
	"testing"
)

func TestParseDictionary(t *testing.T) {
	t.Parallel()
	entries, err := parseDictionary("fixture", []byte("# 夹具\r\n\n㑮\t𫝈 㑮\r\n甲\t甲\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || string(entries['㑮']) != "𫝈㑮" || string(entries['甲']) != "甲" {
		t.Fatalf("全部候选/补充平面解析不完整: %v", entries)
	}
}

func TestRejectMalformedDictionary(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, text, reason string }{
		{"empty", "", "不含字表条目"}, {"comments_only", "# 夹具\n\n", "不含字表条目"},
		{"missing_tab", "甲 乙", "TAB"}, {"extra_tab", "甲\t乙\t丙", "TAB"},
		{"missing_key", "\t乙", "键必须"}, {"multiple_key_scalars", "甲乙\t丙", "键必须"},
		{"whitespace_key", " \t乙", "键必须"}, {"control_key", "\x00\t乙", "键必须"},
		{"missing_value", "甲\t", "候选必须"}, {"phrase_value", "甲\t乙丙", "候选必须"},
		{"bad_second_candidate", "甲\t乙 丙丁", "候选必须"},
		{"leading_separator", "甲\t 乙", "候选必须"}, {"trailing_separator", "甲\t乙 ", "候选必须"},
		{"empty_candidate", "甲\t乙  丙", "候选必须"}, {"whitespace_candidate", "甲\t\u00a0", "候选必须"},
		{"control_candidate", "甲\t\x00", "候选必须"}, {"duplicate_identical", "甲\t乙\n甲\t乙", "重复键"},
		{"duplicate_different", "甲\t乙\n甲\t丙", "重复键"},
		{"invalid_utf8_key", "\xff\t乙", "UTF-8"}, {"invalid_utf8_candidate", "甲\t乙 \xff", "UTF-8"},
		{"surrogate", "甲\t\xed\xa0\x80", "UTF-8"}, {"invalid_comment_utf8", "#\xff\n甲\t乙", "UTF-8"},
		{"line_budget", "#" + strings.Repeat("x", maxDictionaryLineBytes), "行超出"},
		{"file_budget", strings.Repeat("\n", maxEmbeddedFileBytes+1), "字表大小"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			for _, side := range []string{"ts", "st"} {
				ts, st := []byte("甲\t甲"), []byte("乙\t乙")
				if side == "ts" {
					ts = []byte(test.text)
				} else {
					st = []byte(test.text)
				}
				table, err := loadCharacterTables(ts, st)
				if err == nil || !strings.Contains(err.Error(), test.reason) || table != nil {
					t.Errorf("%s: table=%v err=%v, want %q", side, table, err, test.reason)
				}
			}
		})
	}
}

func TestCharacterSelection(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, ts, st string
		want         map[rune]rune
		absent       string
	}{
		{"first_candidate_not_closure", "鍊\t炼 链 𫔀\n鏈\t链", "炼\t煉 鍊\n链\t鏈 鍊", map[rune]rune{'鍊': '炼', '鏈': '链', '煉': '炼'}, "炼链𫔀"},
		{"ts_wins", "甲\t乙", "丙\t甲", map[rune]rune{'甲': '乙'}, "丙"},
		{"self_mapping_wins", "甲\t甲", "乙\t甲", map[rune]rune{'甲': '甲'}, "乙"},
		{"ts_converges_before_st", "甲\t乙\n乙\t丙", "甲\t丁\n乙\t丁", map[rune]rune{'甲': '丙', '乙': '丙', '丁': '丙'}, ""},
		{"ambiguous_st_unchanged", "甲\t甲", "乙\t丁\n丙\t丁", map[rune]rune{'甲': '甲'}, "丁"},
		{"ambiguity_not_last_wins", "甲\t甲", "乙\t丁\n丙\t丁\n戊\t丁", map[rune]rune{'甲': '甲'}, "丁"},
		{"ambiguity_reverse_order", "甲\t甲", "戊\t丁\n丙\t丁\n乙\t丁", map[rune]rune{'甲': '甲'}, "丁"},
		{"supplement_chain_resolved", "甲\t乙", "丙\t乙\n丁\t丙", map[rune]rune{'甲': '丁', '乙': '丁', '丙': '丁'}, ""},
		{"self_mapping_valid", "甲\t甲", "乙\t乙", map[rune]rune{'甲': '甲', '乙': '乙'}, ""},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			for range 20 {
				table, err := loadCharacterTables([]byte(test.ts), []byte(test.st))
				if err != nil {
					t.Fatal(err)
				}
				for source, want := range test.want {
					if got, ok := table[source]; !ok || got != want {
						t.Errorf("%c→%c, exists=%t, want %c", source, got, ok, want)
					}
				}
				for _, source := range test.absent {
					if got, exists := table[source]; exists {
						t.Errorf("歧义/未选中项不应添加: %c→%c", source, got)
					}
				}
				for source, target := range table {
					if terminal, mapped := table[target]; mapped && terminal != target {
						t.Errorf("没有保存终值: %c→%c→%c", source, target, terminal)
					}
				}
			}
		})
	}
}

func TestRejectCharacterCycles(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, ts, st string }{
		{"ts_two_nodes", "甲\t乙\n乙\t甲", "丙\t丙"},
		{"ts_three_nodes", "甲\t乙\n乙\t丙\n丙\t甲", "丁\t丁"},
		{"st_cycle", "丁\t丁", "甲\t乙\n乙\t甲"},
		{"combined_cycle", "甲\t乙", "丙\t乙\n乙\t丙"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			result, err := loadCharacterTables([]byte(test.ts), []byte(test.st))
			if err == nil || !strings.Contains(err.Error(), "循环") || result != nil {
				t.Fatalf("循环未拒绝: table=%v err=%v", result, err)
			}
		})
	}
}

func TestResolveLongCharacterChain(t *testing.T) {
	t.Parallel()
	const first, count = rune(0x10000), 30000
	selected := make(characterTable, count)
	for offset := range count {
		selected[first+rune(offset)] = first + rune(offset) + 1
	}
	result, err := resolveCharacters(selected)
	if err != nil {
		t.Fatal(err)
	}
	for source, target := range result {
		if target != first+count {
			t.Fatalf("长链未收敛: U+%X→U+%X", source, target)
		}
	}
	selected[first+count] = first
	if result, err := resolveCharacters(selected); err == nil || result != nil {
		t.Fatal("长链循环没有拒绝")
	}
}
