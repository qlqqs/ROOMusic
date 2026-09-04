package main

import (
	"database/sql"
	"encoding/json"
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

func TestReleaseArtworkProjectionValidation(t *testing.T) {
	stringValue := func(value string) sql.NullString {
		return sql.NullString{String: value, Valid: true}
	}
	intValue := func(value int64) sql.NullInt64 {
		return sql.NullInt64{Int64: value, Valid: true}
	}

	if artwork, err := releaseArtworkFromProjection(sql.NullString{}, sql.NullString{}, sql.NullInt64{}, sql.NullInt64{}); err != nil || artwork != nil {
		t.Fatalf("empty artwork projection = %+v, %v; want nil, nil", artwork, err)
	}
	for _, mime := range []string{"image/jpeg", "image/png", "image/gif", "image/webp"} {
		t.Run("accepts "+mime, func(t *testing.T) {
			artwork, err := releaseArtworkFromProjection(stringValue("cover.webp"), stringValue(mime), intValue(640), intValue(480))
			if err != nil {
				t.Fatalf("valid artwork projection: %v", err)
			}
			if artwork == nil || artwork.ResourceID != "cover.webp" || artwork.MIME != mime || artwork.Width != 640 || artwork.Height != 480 {
				t.Fatalf("unexpected artwork projection: %+v", artwork)
			}
		})
	}

	for _, testCase := range []struct {
		name       string
		resourceID sql.NullString
		mime       sql.NullString
		width      sql.NullInt64
		height     sql.NullInt64
	}{
		{name: "partial nullable columns", resourceID: stringValue("cover.webp"), mime: stringValue("image/webp"), width: intValue(640)},
		{name: "empty resource identifier", resourceID: stringValue(""), mime: stringValue("image/webp"), width: intValue(640), height: intValue(480)},
		{name: "dot resource identifier", resourceID: stringValue("."), mime: stringValue("image/webp"), width: intValue(640), height: intValue(480)},
		{name: "parent resource identifier", resourceID: stringValue(".."), mime: stringValue("image/webp"), width: intValue(640), height: intValue(480)},
		{name: "slash resource identifier", resourceID: stringValue("private/cover.webp"), mime: stringValue("image/webp"), width: intValue(640), height: intValue(480)},
		{name: "backslash resource identifier", resourceID: stringValue(`private\cover.webp`), mime: stringValue("image/webp"), width: intValue(640), height: intValue(480)},
		{name: "control character resource identifier", resourceID: stringValue("cover\n.webp"), mime: stringValue("image/webp"), width: intValue(640), height: intValue(480)},
		{name: "invalid UTF-8 resource identifier", resourceID: stringValue(string([]byte{0xff})), mime: stringValue("image/webp"), width: intValue(640), height: intValue(480)},
		{name: "unsupported MIME", resourceID: stringValue("cover.webp"), mime: stringValue("image/svg+xml"), width: intValue(640), height: intValue(480)},
		{name: "zero width", resourceID: stringValue("cover.webp"), mime: stringValue("image/webp"), width: intValue(0), height: intValue(480)},
		{name: "negative height", resourceID: stringValue("cover.webp"), mime: stringValue("image/webp"), width: intValue(640), height: intValue(-1)},
		{name: "width outside PostgreSQL integer", resourceID: stringValue("cover.webp"), mime: stringValue("image/webp"), width: intValue(1 << 31), height: intValue(480)},
		{name: "height outside PostgreSQL integer", resourceID: stringValue("cover.webp"), mime: stringValue("image/webp"), width: intValue(640), height: intValue(1 << 31)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if artwork, err := releaseArtworkFromProjection(testCase.resourceID, testCase.mime, testCase.width, testCase.height); err == nil || artwork != nil {
				t.Fatalf("unsafe artwork projection = %+v, %v; want nil artwork and non-nil error", artwork, err)
			}
		})
	}
}

func TestReleaseSummaryJSONAlwaysIncludesArtwork(t *testing.T) {
	encodedSummary, err := json.Marshal(releaseSummaryDTO{})
	if err != nil {
		t.Fatalf("encode release summary: %v", err)
	}
	if !strings.Contains(string(encodedSummary), `"artwork":null`) {
		t.Fatalf("release summary omitted null artwork: %s", encodedSummary)
	}

	encodedDetail, err := json.Marshal(releaseDetailDTO{releaseSummaryDTO: releaseSummaryDTO{
		Artwork: &releaseArtworkDTO{ResourceID: "cover.webp", MIME: "image/webp", Width: 640, Height: 480},
	}})
	if err != nil {
		t.Fatalf("encode release detail: %v", err)
	}
	if strings.Count(string(encodedDetail), `"artwork":`) != 1 {
		t.Fatalf("release detail did not share exactly one artwork field: %s", encodedDetail)
	}
}
