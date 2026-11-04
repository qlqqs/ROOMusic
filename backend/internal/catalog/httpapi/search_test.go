package httpapi

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestGlobalSearchQueryValidation(t *testing.T) {
	for _, tc := range []struct {
		query, want string
		valid       bool
	}{
		{"", "", true}, {" \t\n爱\r ", "爱", true}, {" 周杰倫 ", "周杰倫", true}, {"a\tb", "a\tb", true},
		{strings.Repeat("a", 200), strings.Repeat("a", 200), true}, {strings.Repeat("a", 201), "", false},
		{strings.Repeat("爱", 67), "", false}, {"\xff", "", false}, {"\x00a", "", false}, {"a\x7f", "", false}, {"\x01", "", false},
	} {
		r := httptest.NewRequest("GET", "/api/v1/search?q="+url.QueryEscape(tc.query), nil)
		got, err := parseGlobalSearchQuery(r)
		if (err == nil) != tc.valid || got != tc.want {
			t.Errorf("query %q: %q %v", tc.query, got, err)
		}
	}
	got, err := parseGlobalSearchQuery(httptest.NewRequest("GET", "/api/v1/search?q=first&q=second", nil))
	if err != nil || got != "first" {
		t.Fatal("duplicate parameters must keep the first value")
	}
	// 新接口收紧控制字符校验，旧路由的合同不变。
	if _, err := parseReleaseSearch(httptest.NewRequest("GET", "/api/v1/releases?q=%01", nil)); err != nil {
		t.Fatal("legacy validation changed")
	}
}

func TestSearchSuggestionLimit(t *testing.T) {
	for _, tc := range []struct {
		query string
		want  int
		valid bool
	}{
		{"", 5, true}, {"limit=1", 1, true}, {"limit=8", 8, true}, {"limit=2&limit=9", 2, true},
		{"limit=", 0, false}, {"limit=0", 0, false}, {"limit=9", 0, false}, {"limit=-1", 0, false}, {"limit=1.5", 0, false}, {"limit=x", 0, false},
	} {
		got, err := parseSearchLimit(httptest.NewRequest("GET", "/api/v1/search/suggestions?"+tc.query, nil))
		if (err == nil) != tc.valid || got != tc.want {
			t.Errorf("%s: %d %v", tc.query, got, err)
		}
	}
}
