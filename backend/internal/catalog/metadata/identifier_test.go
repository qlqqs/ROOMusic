package metadata

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestAppendIdentifierValuesISRC(t *testing.T) {
	raw := "us-x1z-25-00001\x00USX1Z2500002/GB-A1B-25-00003;bad, JPABC2500004\tde-1a2-25-00005\n" +
		"1SX1Z2500001 USX1Z25A0001 USX1Z250000 USX1Z25000015 usx1z2500001"
	var want []Identifier
	for _, value := range []string{"USX1Z2500001", "USX1Z2500002", "GBA1B2500003", "JPABC2500004", "DE1A22500005"} {
		want = append(want, Identifier{Kind: "isrc", Value: value})
	}
	if got := AppendIdentifierValues(nil, "isrc", raw); !slices.Equal(got, want) {
		t.Fatalf("ISRC = %+v，期望 %+v", got, want)
	}
}

func TestAppendIdentifierValuesDiscID(t *testing.T) {
	const valid = "AbCdEf0123456789._-abcdefgh-"
	for _, tc := range []struct {
		name, raw, want string
	}{
		{"保留大小写和合法标点", valid, valid},
		{"去除首尾空白和NUL", "\x00\t" + valid + " \x00", valid},
		{"缺少末尾连字符", valid[:27], ""},
		{"末尾不是连字符", valid[:27] + "a", ""},
		{"过短", valid[1:], ""},
		{"过长", valid + "a", ""},
		{"非法字符", "+" + valid[1:], ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var want []Identifier
			if tc.want != "" {
				want = []Identifier{{Kind: "mb_disc", Value: tc.want}}
			}
			if got := AppendIdentifierValues(nil, "mb_disc", tc.raw); !slices.Equal(got, want) {
				t.Fatalf("disc ID = %+v，期望 %+v", got, want)
			}
		})
	}
}

func TestAppendIdentifierValuesLimitPerKind(t *testing.T) {
	for _, kind := range []string{
		"mb_release", "mb_release_group", "mb_recording", "mb_track", "mb_artist",
		"mb_album_artist", "mb_work", "mb_disc", "acoustid", "isrc",
	} {
		t.Run(kind, func(t *testing.T) {
			other := Identifier{Kind: "mb_work", Value: "abcdef01-0000-3000-8000-000000000000"}
			if kind == other.Kind {
				other.Kind = "mb_artist"
			}
			got, want := []Identifier{other}, []Identifier{other}
			var rawValues []string
			for i := 40; i >= 1; i-- {
				value := fmt.Sprintf("abcdef00-0000-3000-8000-%012d", i)
				switch kind {
				case "mb_disc":
					value = fmt.Sprintf("%027d-", i)
				case "isrc":
					value = fmt.Sprintf("USA1B25%05d", i)
				}
				rawValues = append(rawValues, value, value)
				got = AppendIdentifierValues(got, kind, value)
				got = AppendIdentifierValues(got, kind, value)
				if i > 8 {
					want = append(want, Identifier{Kind: kind, Value: value})
				}
			}
			if !slices.Equal(got, want) {
				t.Errorf("分次追加的前 32 个不同值或顺序不符，实际 %+v，期望 %+v", got, want)
			}
			if kind != "mb_disc" {
				got := AppendIdentifierValues([]Identifier{other}, kind, strings.Join(rawValues, "; "))
				if !slices.Equal(got, want) {
					t.Errorf("单个多值标签的前 32 个不同值或顺序不符，实际 %+v，期望 %+v", got, want)
				}
			}
		})
	}
}
