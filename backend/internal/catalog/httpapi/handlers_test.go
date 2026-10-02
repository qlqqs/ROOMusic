package httpapi

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

// 分页参数的解析由 platform/httpserver 共用；这里确认目录列表仍是默认 50、上限 100，非法值写出 400 invalid_pagination。
func TestReleaseListPaginationContract(t *testing.T) {
	maximumInt := int(^uint(0) >> 1)
	for _, testCase := range []struct {
		name     string
		query    string
		page     int
		pageSize int
		invalid  bool
	}{
		{name: "defaults", query: "", page: 1, pageSize: 50},
		{name: "explicit", query: "?page=3&page_size=100", page: 3, pageSize: 100},
		{name: "page zero", query: "?page=0", invalid: true},
		{name: "non-numeric page", query: "?page=x", invalid: true},
		{name: "page size zero", query: "?page_size=0", invalid: true},
		{name: "page size above 100", query: "?page_size=101", invalid: true},
		{name: "offset overflow", query: "?page=" + strconv.Itoa(maximumInt) + "&page_size=100", invalid: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			params, ok := parseReleaseListParams(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/releases"+testCase.query, nil))
			if ok == testCase.invalid {
				t.Fatalf("parseReleaseListParams ok = %v, want %v (HTTP %d: %s)", ok, !testCase.invalid, recorder.Code, recorder.Body.String())
			}
			if testCase.invalid {
				if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), `"invalid_pagination"`) {
					t.Fatalf("expected 400 invalid_pagination, got HTTP %d: %s", recorder.Code, recorder.Body.String())
				}
				return
			}
			if params.Page != testCase.page || params.PageSize != testCase.pageSize {
				t.Fatalf("params = (%d, %d), want (%d, %d)", params.Page, params.PageSize, testCase.page, testCase.pageSize)
			}
		})
	}
}

func TestReleaseSearchParsingTrimsAndEscapesWildcards(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/releases?q=%20%25Rock_%20", nil)
	query, err := parseReleaseSearch(request)
	if err != nil || query != "%Rock_" {
		t.Fatalf("unexpected parsed query %q, %v", query, err)
	}
	longQuery := strings.Repeat("x", 201)
	if _, err := parseReleaseSearch(httptest.NewRequest(http.MethodGet, "/api/v1/releases?q="+longQuery, nil)); err == nil {
		t.Fatal("overlong search query was accepted")
	}
}
