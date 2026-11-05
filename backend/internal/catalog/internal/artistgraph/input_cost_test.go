package artistgraph

import (
	"fmt"
	"strings"
	"testing"
)

func TestArtistInputNameCostUsesRealSplitting(t *testing.T) {
	names := make([]string, 100)
	for i := range names {
		names[i] = fmt.Sprintf("姓名%d", i)
	}
	for _, tc := range []struct {
		raw  string
		want int
	}{
		{strings.Join(names, " / "), 100},
		{"AC/DC", 1},
		{"周杰倫 / 周杰倫", 1},
		{"周杰倫 / Jay Chou", 2},
		{" / ", 1},
	} {
		if got := ArtistInputNameCost(tc.raw); got != tc.want {
			t.Fatalf("原输入署名估计：got=%d want=%d", got, tc.want)
		}
	}
}
