package wikidata

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/egress"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

func TestArtistByIDTermsAndClaims(t *testing.T) {
	const mbid = "abcdef12-0000-4000-8000-000000000001"
	for _, tc := range []struct {
		name, primary, description, wantMBID string
		labels, descriptions                 map[string]string
		ambiguous                            bool
	}{
		{name: "zh_and_en", primary: "合成乐队", description: "示例组合", wantMBID: mbid,
			labels: map[string]string{"zh": "合成乐队", "en": "Synthetic Ensemble"}, descriptions: map[string]string{"zh": "示例组合", "en": "Example ensemble"}},
		{name: "mul", primary: "Common Name", labels: map[string]string{"mul": "Common Name"}},
		{name: "zh_hans", primary: "简体名称", labels: map[string]string{"zh-hans": "简体名称"}},
		{name: "missing_labels", primary: "Q42"},
		{name: "multiple_p434", primary: "Synthetic Ensemble", labels: map[string]string{"en": "Synthetic Ensemble"}, ambiguous: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				if r.URL.Path != "/w/api.php" || q.Get("action") != "wbgetentities" || q.Get("ids") != "Q42" {
					t.Errorf("错误的 Wikidata 请求 %s", r.URL)
					http.NotFound(w, r)
					return
				}
				allowed := strings.Split(q.Get("languages"), "|")
				terms := func(values map[string]string) map[string]map[string]string {
					entries := make(map[string]map[string]string)
					for lang, value := range values {
						if q.Get("languages") == "" || slices.Contains(allowed, lang) {
							entries[lang] = map[string]string{"value": value}
						}
					}
					return entries
				}
				claim := func(rank string, value any) map[string]any {
					return map[string]any{"rank": rank, "mainsnak": map[string]any{"datavalue": map[string]any{"value": value}}}
				}
				p434 := []map[string]any{
					claim("deprecated", "abcdef12-0000-4000-8000-000000000099"), claim("normal", "not-a-uuid"),
				}
				if tc.wantMBID != "" || tc.ambiguous {
					p434 = append(p434, claim("normal", strings.ToUpper(mbid)), claim("normal", mbid))
				}
				if tc.ambiguous {
					p434 = append(p434, claim("normal", "abcdef12-0000-4000-8000-000000000002"))
				}
				entry := map[string]any{
					"id": "Q42", "labels": terms(tc.labels), "descriptions": terms(tc.descriptions),
					"aliases": map[string]any{"en": []map[string]string{{"value": "Fixture alias"}, {"value": "Fixture alias"}}},
					"claims": map[string]any{"P434": p434, "P31": []map[string]any{
						claim("normal", map[string]string{"id": "Q5"}),
						claim("normal", map[string]string{"id": "Q5"}),
						claim("deprecated", map[string]string{"id": "Q9"}),
					}},
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(map[string]any{"entities": map[string]any{"Q42": entry}}); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			client := wikidataTestClient(server)
			got, err := client.ArtistByID(t.Context(), "Q42")
			if err != nil || got.WikidataID != "Q42" || got.Name != tc.primary || got.Description != tc.description || got.MBID != tc.wantMBID ||
				!slices.Equal(got.InstanceOf, []string{"Q5"}) {
				t.Errorf("ArtistByID(Q42) 得到 %+v, %v", got, err)
			}
			if tc.name == "zh_and_en" && !slices.Equal(got.Aliases, []string{"Synthetic Ensemble", "Fixture alias"}) {
				t.Errorf("期望保留英文标签及去重的别名，得到 %v", got.Aliases)
			}
		})
	}
}

func TestArtistByIDMissingAndInvalid(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("ids"); got != "Q123456789012" {
			t.Errorf("非法 ID 不应查询上游，收到 %s", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"entities":{"Q123456789012":{"id":"Q123456789012","missing":""}}}`)
	}))
	defer server.Close()
	client := wikidataTestClient(server)
	for _, tc := range []struct {
		id  string
		err error
	}{
		{"Q123456789012", sources.ErrNotFound},
		{"Q1234567890123", sources.ErrInvalidQuery},
		{"Q0", sources.ErrInvalidQuery},
	} {
		if _, err := client.ArtistByID(t.Context(), tc.id); !errors.Is(err, tc.err) {
			t.Errorf("ArtistByID(%q) 错误 %v，期望 %v", tc.id, err, tc.err)
		}
	}
}

func wikidataTestClient(server *httptest.Server) *Client {
	return New(Options{
		BaseURL: server.URL, MockHost: sources.NewMockHost(server.URL),
		NewClient: func(egress.Options) (*http.Client, error) { return server.Client(), nil },
		Limiter:   sources.NewLimiter(0, nil, nil),
	})
}
