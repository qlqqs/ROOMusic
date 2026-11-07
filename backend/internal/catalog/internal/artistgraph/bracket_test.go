package artistgraph

import (
	"reflect"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
)

func TestBracketAliases(t *testing.T) {
	if err := textnorm.LoadEmbedded(); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		want []alias
	}{
		{"杨璐（羊鹿鹿）", []alias{{text: "杨璐", part: "outer"}, {text: "羊鹿鹿", part: "inner"}}},
		{"杨璐 (羊鹿鹿)", []alias{{text: "杨璐", part: "outer"}, {text: "羊鹿鹿", part: "inner"}}},
		{"  楊璐 （ 羊鹿鹿 ） ", []alias{{text: "杨璐", part: "outer"}, {text: "羊鹿鹿", part: "inner"}}},
		{"ALPHA (BÉTA)", []alias{{text: "alpha", part: "outer"}, {text: "beta", part: "inner"}}},
		{"甲 (Olive)", []alias{{text: "甲", part: "outer"}, {text: "olive", part: "inner"}}},
		{"甲 (Mixer)", []alias{{text: "甲", part: "outer"}, {text: "mixer", part: "inner"}}},
		{"Jin (BTS)", []alias{{text: "jin", part: "outer"}, {text: "bts", part: "inner"}}},
		{"甲 (乙 / 乙)", []alias{{text: "甲", part: "outer"}, {text: "乙 / 乙", part: "inner"}}},
		{"甲 (AC/DC)", []alias{{text: "甲", part: "outer"}, {text: "ac/dc", part: "inner"}}},
		{"", nil},
		{"甲", nil},
		{"甲(乙", nil},
		{"甲乙)", nil},
		{"甲 (乙) 丙", nil},
		{"甲(乙)(丙)", nil},
		{"甲((乙))", nil},
		{"甲)乙(丙)", nil},
		{"(G)I-DLE", nil},
		{"(乙)", nil},
		{"甲()", nil},
		{"甲 ( )", nil},
		{"甲 (甲)", nil},
		{"ALPHA (alpha)", nil},
		{"甲 (2019)", nil},
		{"甲 (#１２３ - ４５)", nil},
		{"甲 (…！)", nil},
		{"甲 (乙 / 丙)", nil},
		{"甲 (乙、丙)", nil},
		{"甲 (乙; 丙)", nil},
		{"甲 (乙,丙)", nil},
		{"甲 (feat. 乙)", nil},
		{"甲 (feat乙)", nil},
		{"甲 (ft. 乙)", nil},
		{"甲 (ft 乙)", nil},
		{"甲 (featuring 乙)", nil},
		{"甲 (with 乙)", nil},
		{"甲 (vs. 乙)", nil},
		{"甲 (x 乙)", nil},
		{"甲 (prod. 乙)", nil},
		{"甲 (cv. 乙)", nil},
		{"甲 (ＣＶ：乙)", nil},
		{"甲 (Live)", nil},
		{"甲 (2019 LIVE!)", nil},
		{"甲 (remix)", nil},
		{"甲 (mix)", nil},
		{"甲 (edit)", nil},
		{"甲 (version)", nil},
		{"甲 (ver.)", nil},
		{"甲 (demo)", nil},
		{"甲 (acoustic)", nil},
		{"甲 (instrumental)", nil},
		{"甲 (inst)", nil},
		{"甲 (remaster)", nil},
		{"甲 (remastered)", nil},
		{"甲 (cover)", nil},
		{"甲 (karaoke)", nil},
		{"甲 (bonus)", nil},
		{"甲（现场版）", nil},
		{"甲 (現場)", nil},
		{"甲 (伴奏)", nil},
		{"甲 (演唱会)", nil},
		{"甲 (合唱)", nil},
		{"甲 (对唱)", nil},
		{"甲 (翻唱)", nil},
		{"甲 (原唱)", nil},
		{"甲 (纯音乐)", nil},
		{"甲 (混音)", nil},
		{"甲 (演奏)", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := textnorm.Text(tt.name)
			if got := bracketAliases(key); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("bracketAliases(%q) = %#v，期望 %#v", key, got, tt.want)
			}
		})
	}
}
