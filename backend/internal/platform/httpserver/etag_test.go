package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEntityTagQuotesTheOpaqueValue(t *testing.T) {
	if got := EntityTag("abc.png"); got != `"abc.png"` {
		t.Fatalf("EntityTag = %s", got)
	}
}

func TestIfNoneMatchHitsUsesWeakComparison(t *testing.T) {
	const opaque = "0123abcd.png"
	for _, testCase := range []struct {
		name   string
		values []string
		want   bool
	}{
		{"no header", nil, false},
		{"empty header", []string{""}, false},
		{"exact strong tag", []string{`"0123abcd.png"`}, true},
		{"weak tag matches under weak comparison", []string{`W/"0123abcd.png"`}, true},
		{"different tag", []string{`"other.png"`}, false},
		{"tag is case sensitive", []string{`"0123ABCD.png"`}, false},
		{"prefix of the tag", []string{`"0123abcd"`}, false},
		{"tag with a suffix", []string{`"0123abcd.png.old"`}, false},
		{"list with spaces", []string{`"a", "0123abcd.png", "b"`}, true},
		{"list without spaces", []string{`"a","0123abcd.png","b"`}, true},
		{"list with a weak entry first", []string{`W/"a", W/"0123abcd.png"`}, true},
		{"list without a match", []string{`"a", "b", W/"c"`}, false},
		{"leading and trailing whitespace", []string{"  \t\"0123abcd.png\"  "}, true},
		{"wildcard", []string{"*"}, true},
		{"wildcard with whitespace", []string{"  *  "}, true},
		{"wildcard is only valid alone", []string{`*, "other"`}, false},
		{"several header lines, second matches", []string{`"a"`, `"0123abcd.png"`}, true},
		{"several header lines, none match", []string{`"a"`, `"b"`}, false},
		{"unquoted token", []string{"0123abcd.png"}, false},
		{"unterminated quote", []string{`"0123abcd.png`}, false},
		{"garbage before a valid tag", []string{`junk, "0123abcd.png"`}, false},
		{"only a weak marker", []string{"W/"}, false},
		{"comma inside another tag does not split the match", []string{`"x,"0123abcd.png"`}, false},
		{"empty tag", []string{`""`}, false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/v1/artworks/x", nil)
			for _, value := range testCase.values {
				request.Header.Add("If-None-Match", value)
			}
			if got := IfNoneMatchHits(request, opaque); got != testCase.want {
				t.Fatalf("IfNoneMatchHits(%q) = %v, want %v", testCase.values, got, testCase.want)
			}
		})
	}
}
