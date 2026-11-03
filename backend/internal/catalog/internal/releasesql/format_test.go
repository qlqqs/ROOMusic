package releasesql

import (
	"database/sql"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBoundedDisplayString(t *testing.T) {
	for _, test := range []struct {
		name, value, want string
	}{
		{name: "empty"},
		{name: "controls and whitespace", value: " \tA\x00B\nC\x7f ", want: "ABC"},
		{name: "at bound", value: strings.Repeat("a", MaxEvidenceStringBytes), want: strings.Repeat("a", MaxEvidenceStringBytes)},
		{name: "over bound", value: strings.Repeat("a", MaxEvidenceStringBytes+1), want: strings.Repeat("a", MaxEvidenceStringBytes)},
		{name: "UTF-8 boundary", value: strings.Repeat("曲", MaxEvidenceStringBytes/3+1), want: strings.Repeat("曲", MaxEvidenceStringBytes/3)},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := BoundedDisplayString(test.value)
			if got != test.want || len(got) > MaxEvidenceStringBytes || !utf8.ValidString(got) {
				t.Fatalf("BoundedDisplayString() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestDecodeBoundedStringArray(t *testing.T) {
	long, err := json.Marshal([]string{strings.Repeat("a", MaxEvidenceStringBytes+1)})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, raw             string
		limit                 int
		sourceRefs, truncated bool
		want                  []string
	}{
		{name: "empty", raw: `[]`, limit: 2, want: []string{}},
		{name: "null", raw: `null`, limit: 2, want: []string{}},
		{name: "zero bound", raw: `["a"]`, limit: 0, want: []string{}, truncated: true},
		{name: "count bound", raw: `["a","b","c"]`, limit: 2, want: []string{"a", "b"}, truncated: true},
		{name: "empty and normalized text", raw: `["  "," a "]`, limit: 2, want: []string{"a"}, truncated: true},
		{name: "text bound", raw: string(long), limit: 2, want: []string{strings.Repeat("a", MaxEvidenceStringBytes)}, truncated: true},
		{name: "source normalization", raw: `[" Album\\01.flac "]`, limit: 2, sourceRefs: true, want: []string{"Album/01.flac"}},
		{name: "source cleaning", raw: `["Album/Disc/../01.flac"]`, limit: 2, sourceRefs: true, want: []string{"Album/01.flac"}, truncated: true},
		{name: "unsafe sources", raw: `["../outside.flac","/private/01.flac","Album/01.flac"]`, limit: 4, sourceRefs: true, want: []string{"Album/01.flac"}, truncated: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, truncated, err := DecodeBoundedStringArray(test.raw, test.limit, test.sourceRefs)
			if err != nil || truncated != test.truncated || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("DecodeBoundedStringArray() = %v, %v, %v; want %v, %v, nil", got, truncated, err, test.want, test.truncated)
			}
		})
	}
	for _, raw := range []string{`[`, `[1]`, `{}`} {
		if got, truncated, err := DecodeBoundedStringArray(raw, 2, false); err == nil || got != nil || truncated {
			t.Fatalf("DecodeBoundedStringArray(%q) = %v, %v, %v; want nil, false, error", raw, got, truncated, err)
		}
	}
}

func TestSplitBoundedReasonCodes(t *testing.T) {
	for _, test := range []struct {
		name, raw string
		want      []string
		truncated bool
	}{
		{name: "empty", raw: " \t", want: []string{}},
		{name: "trim and skip empty", raw: " alpha ; ;beta\n ", want: []string{"alpha", "beta"}},
		{name: "text bound", raw: strings.Repeat("a", MaxEvidenceStringBytes+1), want: []string{strings.Repeat("a", MaxEvidenceStringBytes)}},
		{name: "count bound", raw: strings.Repeat("a;", MaxEvidenceCandidates) + "a", want: strings.Split(strings.Repeat("a;", MaxEvidenceCandidates-1)+"a", ";"), truncated: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, truncated := SplitBoundedReasonCodes(test.raw)
			if truncated != test.truncated || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("SplitBoundedReasonCodes() = %v, %v; want %v, %v", got, truncated, test.want, test.truncated)
			}
		})
	}
}

func TestSafeSourceLabel(t *testing.T) {
	for _, test := range []struct{ value, want string }{
		{value: "Album/01.flac", want: "01.flac"},
		{value: `Album\01.flac`, want: "01.flac"},
		{value: "Album/ 01\n.flac ", want: "01.flac"},
		{value: "", want: "未知来源"},
		{value: ".", want: "未知来源"},
		{value: "/", want: "未知来源"},
		{value: "Album/" + strings.Repeat("a", MaxEvidenceStringBytes+1), want: strings.Repeat("a", MaxEvidenceStringBytes)},
	} {
		if got := SafeSourceLabel(test.value); got != test.want {
			t.Errorf("SafeSourceLabel(%q) = %q, want %q", test.value, got, test.want)
		}
	}
}

func TestPointersFromNull(t *testing.T) {
	if got := StringPointerFromNull(sql.NullString{String: "ignored"}); got != nil {
		t.Fatalf("invalid string = %v, want nil", got)
	}
	for _, value := range []string{"", "title"} {
		if got := StringPointerFromNull(sql.NullString{String: value, Valid: true}); got == nil || *got != value {
			t.Fatalf("valid string %q = %v", value, got)
		}
	}
	if got := IntPointerFromNull(sql.NullInt64{Int64: 42}); got != nil {
		t.Fatalf("invalid integer = %v, want nil", got)
	}
	for _, value := range []int64{0, 2026} {
		if got := IntPointerFromNull(sql.NullInt64{Int64: value, Valid: true}); got == nil || *got != int(value) {
			t.Fatalf("valid integer %d = %v", value, got)
		}
	}
}
