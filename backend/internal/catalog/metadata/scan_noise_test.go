package metadata

import "testing"

func TestStripFormatNoisePreservesTitleWords(t *testing.T) {
	for _, title := range []string{
		"Spider's Web",
		"The Web",
		"Web of Lies",
		"Charlotte's Web",
		"8 Bit Heart",
		"16 Bit Lolitas",
		"Ape Man",
		"The Naked Ape",
	} {
		t.Run(title, func(t *testing.T) {
			if got := StripFormatNoise(title); got != title {
				t.Fatalf("去噪后标题为 %q，期望 %q", got, title)
			}
		})
	}
}
