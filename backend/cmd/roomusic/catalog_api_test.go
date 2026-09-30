package main

import (
	"net/http"
	"net/http/httptest"
	"strconv"
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
