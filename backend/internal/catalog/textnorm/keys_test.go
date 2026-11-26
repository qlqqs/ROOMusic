package textnorm

import "testing"

func TestMergeKeyGroupingContract(t *testing.T) {
	for _, input := range []string{"Beyoncé — Live", "BEYONCE\u00a0LIVE", "Beyonce\u0301\t.Li-ve"} {
		if got := MergeKey(input); got != "beyoncelive" {
			t.Errorf("MergeKey(%q) = %q，期望 beyoncelive", input, got)
		}
	}
	for _, pair := range [][2]string{
		{"繁體", "繁体"}, {"かな", "カナ"}, {"The Who", "Who The"}, {"Album (Deluxe)", "Album"},
	} {
		if a, b := MergeKey(pair[0]), MergeKey(pair[1]); a == b {
			t.Errorf("不同原名 %q 和 %q 得到相同键 %q", pair[0], pair[1], a)
		}
	}
	if got := MergeKey(" —\t . "); got != "" {
		t.Errorf("无文字的合并键 = %q", got)
	}
}
