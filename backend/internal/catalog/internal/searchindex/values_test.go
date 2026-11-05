package searchindex

import (
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
)

func TestSharedSearchValuesPreserveSourcesAndBudgets(t *testing.T) {
	if err := textnorm.LoadEmbedded(); err != nil {
		t.Fatal(err)
	}
	values := appendValue(nil, "track_artist", "", "Zulu / Alpha / Alpha", 8)
	if len(values) != 2 || values[0].Original != "Zulu" || values[0].Position != 1 || values[1].Original != "Alpha" || values[1].Position != 2 {
		t.Fatalf("多名原文顺序/去重语义改变：%+v", values)
	}
	var err error
	values, err = appendCredit(values, "composer", " 作曲者甲 / 作曲者乙 ", 7)
	if err != nil || values[2].Original != " 作曲者甲 / 作曲者乙 " || values[2].Position != 7 {
		t.Fatalf("credit原文被拆分或修剪：%+v %v", values, err)
	}
	values = appendValue(values, "track_title", "", "繁體 ＡＢＣ", 1)
	values = appendValue(values, "catalog_number", "", "SRCL-1234", 1)
	title, err := normalizeValues(values)
	if err != nil || title != "繁体 abc" || values[4].Normalized != "srcl1234" {
		t.Fatalf("规范化或标题键改变：%q %+v %v", title, values, err)
	}
	for _, raw := range []string{strings.Repeat("x", 4097), string([]byte{0xff})} {
		if _, err := normalizeValues([]value{{Original: raw}}); err == nil {
			t.Fatal("原文预算/UTF-8校验放松")
		}
	}
	if _, err := appendCredit(nil, "unsupported", "姓名", 1); err == nil {
		t.Fatal("放宽了credit角色")
	}
}
