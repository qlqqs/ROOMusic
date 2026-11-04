package searchindex

import (
	"reflect"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
)

func TestSearchMatchingHelpers(t *testing.T) {
	for _, test := range []struct {
		value, gram string
		grams       []string
	}{
		{"", "", []string{}}, {"周", "周", []string{"周"}},
		{"aab", "aa", []string{"a", "aa", "ab", "b"}},
		{"𠀀%~", "𠀀%", []string{"%", "%~", "~", "𠀀", "𠀀%"}},
	} {
		if got := QueryGram(test.value); got != test.gram {
			t.Errorf("QueryGram(%q)=%q", test.value, got)
		}
		if got := ShortGrams(test.value); !reflect.DeepEqual(got, test.grams) {
			t.Errorf("ShortGrams(%q)=%q", test.value, got)
		}
	}
	if got := EscapeLike("a%_~b"); got != "a~%~_~~b" {
		t.Fatalf("EscapeLike=%q", got)
	}
}

func TestSearchStateRequiresCompleteMatchingVersion(t *testing.T) {
	ready := State{TargetGeneration: 1, PublishedGeneration: 1, TextnormVersion: int64(textnorm.Version),
		PublishedTextnormVersion: int64(textnorm.Version), SearchProjectionVersion: ProjectionVersion, PublishedSearchProjectionVersion: ProjectionVersion,
		ManifestSealed: true, Status: "ready"}
	if !ready.Ready() {
		t.Fatal("matching complete state is not ready")
	}
	for name, change := range map[string]func(*State){
		"status": func(s *State) { s.Status = "building" }, "generation": func(s *State) { s.TargetGeneration++ },
		"normalization": func(s *State) { s.TextnormVersion++ }, "projection": func(s *State) { s.SearchProjectionVersion++ },
		"manifest":       func(s *State) { s.ManifestSealed = false },
		"unknown binary": func(s *State) { s.SearchProjectionVersion++; s.PublishedSearchProjectionVersion++ },
	} {
		t.Run(name, func(t *testing.T) {
			state := ready
			change(&state)
			if state.Ready() {
				t.Fatal("incomplete state is ready")
			}
		})
	}
}
