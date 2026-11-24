package stewardtools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

func TestArtistMediaInputValidatesCitationAndBioBoundaries(t *testing.T) {
	const page = "https://artists.example.org/profile"
	for _, tc := range []struct {
		name, url, title string
		citations, runes int
		valid            bool
	}{
		{"最少字数与引用", page, "", 1, 20, true},
		{"最大字数与引用", page, strings.Repeat("题", 500), 5, 4000, true},
		{"简介不足二十字", page, "", 1, 19, false},
		{"简介超过四千字", page, "", 1, 4001, false},
		{"引用为空", page, "", 0, 20, false},
		{"引用超过五条", page, "", 6, 20, false},
		{"拒绝非 HTTP 引用", "file:///synthetic/profile", "", 1, 20, false},
		{"拒绝带凭据的引用", "https://fixture-only@artists.example.org/profile", "", 1, 20, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := mediaInput{Citations: make([]catalog.Citation, tc.citations),
				Bio: &struct {
					Text string `json:"text"`
					Lang string `json:"lang"`
				}{Text: "<p>" + strings.Repeat("字", tc.runes) + "</p>", Lang: "zh"}}
			for i := range input.Citations {
				input.Citations[i] = catalog.Citation{URL: tc.url, Title: tc.title}
			}
			err := input.validate()
			if (err == nil) != tc.valid {
				t.Fatalf("简介和引用校验返回 %v，期望有效=%t", err, tc.valid)
			}
			if tc.valid && input.Bio.Text != strings.Repeat("字", tc.runes) {
				t.Fatalf("简介的 HTML 标签未清除")
			}
		})
	}
}

func TestArtistMediaRegistryCitationTitleLimit(t *testing.T) {
	definition := (adapter{}).mediaTool(nil)
	definition.Handler = func(context.Context, tool.Caller, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	}
	registry := tool.NewRegistry()
	if err := registry.Register(definition); err != nil {
		t.Fatal(err)
	}
	caller := tool.Caller{User: identity.User{Role: "admin"}, Source: tool.SourceCuration, SharedExecution: true}
	for _, length := range []int{500, 501} {
		raw, err := json.Marshal(map[string]any{
			"item_id":   "10000000-0000-4000-8000-000000000001",
			"bio":       map[string]string{"text": strings.Repeat("字", 20), "lang": "zh"},
			"citations": []catalog.Citation{{URL: "https://artists.example.org/profile", Title: strings.Repeat("题", length)}},
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = registry.Invoke(t.Context(), caller, definition.Name, raw)
		if length == 500 {
			if err != nil {
				t.Fatalf("五百字标题被拒绝 %v", err)
			}
			continue
		}
		var failure *tool.Error
		if !errors.As(err, &failure) || failure.Code != "invalid_input" {
			t.Fatalf("五百零一字标题返回 %v，期望 invalid_input", err)
		}
	}
}
