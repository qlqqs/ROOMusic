package metadata

import (
	"slices"
	"testing"
)

func TestLegacyDecodingsPreservesWesternText(t *testing.T) {
	for _, input := range []struct {
		name string
		raw  string
	}{
		{"Don’t Stop", "Don\x92t Stop"},
		{"I’m Not in Love", "I\x92m Not in Love"},
		{"Cœur de pirate", "C\x9cur de pirate"},
		{"Créée", "Cr\xe9\xe9e"},
		{"RÉÉDITION", "R\xc9\xc9DITION"},
	} {
		t.Run(input.name, func(t *testing.T) {
			if got := LegacyDecodings([]byte(input.raw)); len(got) != 0 {
				t.Fatalf("西文标签 %q 被识别为旧编码候选 %q", input.raw, got)
			}
		})
	}
}

func TestLegacyDecodingsKeepsGBKCandidate(t *testing.T) {
	if got := LegacyDecodings([]byte("\xcd\xf5\xb7\xc6")); !slices.Contains(got, "王菲") {
		t.Fatalf("GBK 解码候选 %q 缺少 %q", got, "王菲")
	}
}
