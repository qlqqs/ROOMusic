package provider

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestC8ResponsesSearchCallLimit(t *testing.T) {
	for _, tc := range []struct {
		name   string
		native bool
		force  string
		want   bool
	}{
		{"search", true, "", true}, {"no_search", false, "", false}, {"forced_review", true, "submit_review", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := testRequest()
			r.NativeSearch = tc.native
			r.ForceTool = tc.force
			body, err := responsesRequest(r)
			if err != nil {
				t.Fatal(err)
			}
			value, exists := body["max_tool_calls"]
			if exists != tc.want || exists && value != 5 {
				t.Fatalf("max_tool_calls=%v，存在=%v", value, exists)
			}
		})
	}
}

func TestC8SearchProjectionBounds(t *testing.T) {
	title := strings.Repeat("题", 201)
	values := []Citation{{URL: "https://example.org/" + strings.Repeat("x", 2048), Title: "丢弃"}, {URL: "https://example.org/" + strings.Repeat("x", 2048-len("https://example.org/")), Title: title}}
	for i := 0; i < 25; i++ {
		values = append(values, Citation{URL: fmt.Sprintf("https://example.org/%d", i), Title: title})
	}
	values = append(values, values[1])
	citations := CleanCitations(values)
	if len(citations) != 20 || utf8.RuneCountInString(citations[0].URL) != 2048 {
		t.Fatalf("引用数量/地址边界=%+v", citations)
	}
	for _, c := range citations {
		if c.Title != strings.Repeat("题", 200) || !utf8.ValidString(c.Title) {
			t.Fatal("标题没有按 rune 截断")
		}
	}
	search := CleanSearch(Search{Query: strings.Repeat("查", 401), Results: values})
	if len(search.Results) != 10 || search.Query != strings.Repeat("查", 400) || !utf8.ValidString(search.Query) {
		t.Fatalf("搜索边界=%+v", search)
	}
}
