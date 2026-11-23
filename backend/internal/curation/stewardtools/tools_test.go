package stewardtools

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/curation"
)

func TestModelSource(t *testing.T) {
	for _, tc := range []struct{ name, model, note, provider, basis string }{
		{"normalized_provider", "ACME/Model: X", "合成依据", "acme-model--x", "合成依据"},
		{"empty_provider", "", "", "model", ""},
		{"long_provider", strings.Repeat("A", 65), "依据", strings.Repeat("a", 64), "依据"},
		{"basis_byte_limit", "Model", strings.Repeat("a", 497) + "证据", "model", strings.Repeat("a", 497) + "证"},
		{"basis_rune_boundary", "Model", strings.Repeat("a", 499) + "证据", "model", strings.Repeat("a", 499)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := modelSource(&curation.ModelRun{Model: tc.model}, tc.note)
			if got.Kind != "model" || got.Provider != tc.provider || got.Basis != tc.basis || !utf8.ValidString(got.Basis) {
				t.Fatalf("来源=%q/%q，依据大小=%d；期望 model/%q，依据大小=%d", got.Kind, got.Provider, len(got.Basis), tc.provider, len(tc.basis))
			}
		})
	}
}
