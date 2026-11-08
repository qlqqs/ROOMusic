package metadata

import (
	"reflect"
	"strings"
	"testing"
)

func TestCreditRoleTableVersionAndOrder(t *testing.T) {
	if CreditRoleTableVersion != 1 {
		t.Fatalf("署名表版本 = %d，期望 1", CreditRoleTableVersion)
	}
	for index, role := range []string{"lyricist", "composer", "arranger", "conductor", "performer", "producer"} {
		if got := CreditRoleOrder(role); got != index {
			t.Errorf("角色 %s 的顺序 = %d，期望 %d", role, got, index)
		}
	}
}

func TestCreditRoleTableMappingsAreUniqueAndValid(t *testing.T) {
	table := []struct {
		aliases string
		want    []RoleAssignment
	}{
		{"lyricist|lyrics|words|text|作词|词|填词", []RoleAssignment{{Role: "lyricist"}}},
		{"librettist|libretto", []RoleAssignment{{Role: "lyricist", Detail: "libretto"}}},
		{"composer|composed|music|作曲|曲", []RoleAssignment{{Role: "composer"}}},
		{"songwriter|written|writer|written-by|composerlyricist|词曲|作词作曲|词曲创作", []RoleAssignment{{Role: "lyricist"}, {Role: "composer"}}},
		{"arranger|arranged|arrangement|orchestrator|orchestration|orchestrated|编曲|配器", []RoleAssignment{{Role: "arranger"}}},
		{"conductor|conducted|指挥", []RoleAssignment{{Role: "conductor"}}},
		{"producer|produced|co-producer|executive producer|制作人|制作|监制", []RoleAssignment{{Role: "producer"}}},
		{"mix|mixer|mixing|mixed|mixing engineer|混音|混音师|混音工程师", []RoleAssignment{{Role: "producer", Detail: "mix"}}},
		{"mastering|mastered|mastering engineer|母带|母带处理|母带工程师", []RoleAssignment{{Role: "producer", Detail: "mastering"}}},
		{"engineer|recording engineer|recording|recorded|录音|录音师|录音工程师", []RoleAssignment{{Role: "producer", Detail: "recording"}}},
		{"remixer|remix|remixed|重混", []RoleAssignment{{Role: "producer", Detail: "remix"}}},
		{"dj-mix|dj mix|djmixer", []RoleAssignment{{Role: "producer", Detail: "dj_mix"}}},
		{"programming|programmed|编程", []RoleAssignment{{Role: "producer", Detail: "programming"}}},
		{"performer|soloist|演奏", []RoleAssignment{{Role: "performer"}}},
		{"vocals|vocal|vocalist|lead vocals|演唱|主唱", []RoleAssignment{{Role: "performer", Detail: "vocals"}}},
		{"backing vocals|background vocals|和声|和音", []RoleAssignment{{Role: "performer", Detail: "backing_vocals"}}},
	}
	validRoles := map[string]bool{"lyricist": true, "composer": true, "arranger": true, "conductor": true, "performer": true, "producer": true}
	seen := make(map[string]bool)
	for _, row := range table {
		for _, alias := range strings.Split(row.aliases, "|") {
			if key := creditRoleKey(alias); seen[key] {
				t.Fatalf("角色表中出现重复键 %q", key)
			} else {
				seen[key] = true
			}
			t.Run(alias, func(t *testing.T) {
				got := MapCreditRole(alias)
				if !reflect.DeepEqual(got, row.want) {
					t.Fatalf("MapCreditRole(%q) = %+v，期望 %+v", alias, got, row.want)
				}
				for _, assignment := range got {
					if !validRoles[assignment.Role] {
						t.Errorf("角色表目标非法：%+v", assignment)
					}
				}
			})
		}
	}
	for _, raw := range []string{" Arranged BY ", "\tARRANGED   by\n", "arranged\u3000by"} {
		if got, want := MapCreditRole(raw), []RoleAssignment{{Role: "arranger"}}; !reflect.DeepEqual(got, want) {
			t.Errorf("角色写法归一失败：%q → %+v，期望 %+v", raw, got, want)
		}
	}
	if got, want := MapCreditRole(" MIXING\t\tENGINEER By "), []RoleAssignment{{Role: "producer", Detail: "mix"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("多词工种归一失败：%+v，期望 %+v", got, want)
	}
}

func TestInstrumentTableMappingsAreUniqueAndValid(t *testing.T) {
	table := []struct{ detail, aliases string }{
		{"violin", "violin|violins|小提琴"},
		{"viola", "viola|中提琴"},
		{"cello", "cello|violoncello|大提琴"},
		{"double_bass", "double bass|contrabass|upright bass|低音提琴"},
		{"bass", "bass|bass guitar|electric bass|贝斯|贝司"},
		{"guitar", "guitar|acoustic guitar|electric guitar|classical guitar|吉他"},
		{"piano", "piano|pianoforte|钢琴"},
		{"keyboards", "keyboard|keyboards|键盘"},
		{"organ", "organ|pipe organ|管风琴"},
		{"harpsichord", "harpsichord|羽管键琴|大键琴"},
		{"synthesizer", "synthesizer|synth|合成器"},
		{"drums", "drums|drum kit|drum set|鼓|架子鼓"},
		{"percussion", "percussion|打击乐"},
		{"flute", "flute|长笛"},
		{"clarinet", "clarinet|单簧管"},
		{"oboe", "oboe|双簧管"},
		{"bassoon", "bassoon|巴松管|大管"},
		{"saxophone", "saxophone|sax|alto saxophone|tenor saxophone|萨克斯|萨克斯风"},
		{"trumpet", "trumpet|小号"},
		{"trombone", "trombone|长号"},
		{"horn", "horn|french horn|圆号"},
		{"harp", "harp|竖琴"},
		{"erhu", "erhu|二胡"},
		{"pipa", "pipa|琵琶"},
		{"guzheng", "guzheng|古筝"},
		{"dizi", "dizi|笛子|竹笛"},
		{"vocals", "vocals|vocal|voice|singer|演唱|主唱"},
		{"backing_vocals", "backing vocals|background vocals|和声|和音"},
		{"choir", "choir|chorus|合唱"},
		{"orchestra", "orchestra|ensemble|乐团"},
	}
	seen := make(map[string]bool)
	for _, row := range table {
		for _, alias := range strings.Split(row.aliases, "|") {
			if key := creditRoleKey(alias); seen[key] {
				t.Fatalf("乐器表中出现重复键 %q", key)
			} else {
				seen[key] = true
			}
			t.Run(alias, func(t *testing.T) {
				want := []RoleAssignment{{Role: "performer", Detail: row.detail}}
				got := MapInstrument("  " + strings.ToUpper(strings.ReplaceAll(alias, " ", "\t  ")) + "  ")
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("MapInstrument(%q) = %+v，期望 %+v", alias, got, want)
				}
			})
		}
	}
}

func TestCreditRoleAndInstrumentUnknownReturnNil(t *testing.T) {
	for _, raw := range []string{"", "\t ", "art direction", "theremin", "engineer recording", "composer [guest]"} {
		if got := MapCreditRole(raw); got != nil {
			t.Errorf("未知工种 %q 应交给调用方兜底，实际 %+v", raw, got)
		}
		if got := MapInstrument(raw); got != nil {
			t.Errorf("未知乐器 %q 应交给调用方兜底，实际 %+v", raw, got)
		}
	}
}

func TestCreditRoleMappingsReturnIndependentAssignments(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mapValue func(string) []RoleAssignment
		raw      string
		want     []RoleAssignment
	}{
		{"工种", MapCreditRole, "mix", []RoleAssignment{{Role: "producer", Detail: "mix"}}},
		{"词曲", MapCreditRole, "writer", []RoleAssignment{{Role: "lyricist"}, {Role: "composer"}}},
		{"乐器", MapInstrument, "violin", []RoleAssignment{{Role: "performer", Detail: "violin"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first := tc.mapValue(tc.raw)
			first[0] = RoleAssignment{Role: "producer", Detail: "歌词原写法"}
			if got := tc.mapValue(tc.raw); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("调用方修改返回值污染了映射表：%+v，期望 %+v", got, tc.want)
			}
		})
	}
}

func TestCanonicalCreditsSortDeduplicateAndDefaultTagSource(t *testing.T) {
	input := []Credit{
		{Role: "producer", Name: "Z", Detail: "mix", Source: "tag"},
		{Role: "performer", Name: "Z", Detail: "violin", Source: "lyrics"},
		{Role: "composer", Name: "B"},
		{Role: "lyricist", Name: "L", Source: "lyrics"},
		{Role: "arranger", Name: "A"},
		{Role: "conductor", Name: "C"},
		{Role: "performer", Name: "A", Detail: "violin"},
		{Role: "performer", Name: "Z", Detail: "cello"},
		{Role: "performer", Name: "Z"},
		{Role: " COMPOSER ", Name: " A  Name ", Source: "lyrics"},
		{Role: "composer", Name: "A Name"},
		{Role: "producer", Name: "Z"},
		{Role: "producer", Name: "Z", Detail: " mix ", Source: "lyrics"},
		{Role: "", Name: "无角色"}, {Role: "composer", Name: " "},
	}
	want := []Credit{
		{Role: "lyricist", Name: "L", Source: "lyrics"},
		{Role: "composer", Name: "A Name", Source: "tag"},
		{Role: "composer", Name: "B", Source: "tag"},
		{Role: "arranger", Name: "A", Source: "tag"},
		{Role: "conductor", Name: "C", Source: "tag"},
		{Role: "performer", Name: "Z", Source: "tag"},
		{Role: "performer", Name: "Z", Detail: "cello", Source: "tag"},
		{Role: "performer", Name: "A", Detail: "violin", Source: "tag"},
		{Role: "performer", Name: "Z", Detail: "violin", Source: "lyrics"},
		{Role: "producer", Name: "Z", Source: "tag"},
		{Role: "producer", Name: "Z", Detail: "mix", Source: "tag"},
	}
	if got := CanonicalCredits(input); !reflect.DeepEqual(got, want) {
		t.Fatalf("署名规范化 = %+v，期望 %+v", got, want)
	}
}

func TestCreditSplitExplicitArtistValuesOnlySplitsExplicitSeparators(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		want      []string
	}{
		{"显式多值", " A (violin); B (cello)\nC\r\nD\x00E；F / G、H feat. I ft. J with K ", []string{"A (violin)", "B (cello)", "C", "D", "E", "F", "G", "H", "I", "J", "K"}},
		{"紧凑标点留给后续姓名规则", "A/B,C&D;E", []string{"A/B,C&D;E"}},
		{"乐器括号内不拆", "Yo-Yo Ma (cello, conductor)", []string{"Yo-Yo Ma (cello, conductor)"}},
		{"固定组合与姓前名", "Simon & Garfunkel (guitar); Bach, Johann (piano)", []string{"Simon & Garfunkel (guitar)", "Bach, Johann (piano)"}},
		{"空值", " \r\n\x00 ", []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := SplitExplicitArtistValues(tc.raw); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("显式拆分 %q = %#v，期望 %#v", tc.raw, got, tc.want)
			}
		})
	}
}
