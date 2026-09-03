package main

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestReleaseAttentionFilterAllowlist(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		path     string
		required bool
		wantErr  bool
	}{
		{name: "absent", path: "/api/v1/releases"},
		{name: "required", path: "/api/v1/releases?attention=required", required: true},
		{name: "empty", path: "/api/v1/releases?attention=", wantErr: true},
		{name: "unknown", path: "/api/v1/releases?attention=guessed", wantErr: true},
		{name: "duplicate", path: "/api/v1/releases?attention=required&attention=required", wantErr: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, testCase.path, nil)
			required, err := parseReleaseAttention(request)
			if (err != nil) != testCase.wantErr {
				t.Fatalf("parseReleaseAttention() error = %v, wantErr %v", err, testCase.wantErr)
			}
			if required != testCase.required {
				t.Fatalf("parseReleaseAttention() = %v, want %v", required, testCase.required)
			}
		})
	}
}

func TestReleasePaginationRejectsOffsetOverflow(t *testing.T) {
	maximumInt := int(^uint(0) >> 1)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/releases?page="+strconv.Itoa(maximumInt)+"&page_size=100", nil)
	if _, _, err := parsePagination(request); err == nil {
		t.Fatal("pagination offset overflow was accepted")
	}
}

func TestCatalogEvidenceProjectionRejectsUnsafeSourceReferences(t *testing.T) {
	for _, testCase := range []struct {
		value string
		want  string
		ok    bool
	}{
		{value: "Album/Disc 1/01.flac", want: "Album/Disc 1/01.flac", ok: true},
		{value: `Album\Disc 1\01.flac`, want: "Album/Disc 1/01.flac", ok: true},
		{value: "/srv/private/01.flac"},
		{value: "C:/Music/01.flac"},
		{value: "file:///srv/private/01.flac"},
		{value: "../outside.flac"},
		{value: "Album/../../outside.flac"},
		{value: ".\n./outside.flac"},
		{value: strings.Repeat("a", maxEvidenceStringBytes-3) + "/..."},
	} {
		got, ok := safeRelativeSourceRef(testCase.value)
		if ok != testCase.ok || got != testCase.want {
			t.Errorf("safeRelativeSourceRef(%q) = (%q, %v), want (%q, %v)", testCase.value, got, ok, testCase.want, testCase.ok)
		}
	}

	values, truncated, err := decodeBoundedStringArray(`["Album/01.flac","/srv/private.flac","../outside.flac"]`, 10, true)
	if err != nil {
		t.Fatalf("decode bounded source references: %v", err)
	}
	if !truncated || len(values) != 1 || values[0] != "Album/01.flac" {
		t.Fatalf("unsafe source references were not removed: values=%v truncated=%v", values, truncated)
	}
}

func TestRESTIdentifiersAreValidatedBeforeDatabaseCasts(t *testing.T) {
	if !isValidIdentifier("01234567-89ab-cdef-0123-456789abcdef") {
		t.Fatal("valid UUID-shaped identifier was rejected")
	}
	for _, value := range []string{"", "release-1", "01234567-89ab-cdef-0123-456789abcdeg", "0123456789abcdef0123456789abcdef"} {
		if isValidIdentifier(value) {
			t.Errorf("malformed identifier %q was accepted", value)
		}
	}
}

func TestScanDiagnosticProjectionDoesNotEchoStoredHostPaths(t *testing.T) {
	message := safeDiagnosticMessage("unsupported_cue", "open /srv/private/album.cue: permission denied")
	if strings.Contains(message, "/srv/private") {
		t.Fatalf("safe diagnostic message leaked a host path: %q", message)
	}
	if safeDiagnosticKind("../../private") != "unknown" {
		t.Fatal("unsafe diagnostic kind was exposed")
	}
}
