package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestParsePagination(t *testing.T) {
	maximumInt := strconv.Itoa(int(^uint(0) >> 1))
	for _, testCase := range []struct {
		name              string
		query             string
		defaultSize, max  int
		page, pageSize    int
		wantInvalidResult bool
	}{
		{name: "defaults", query: "", defaultSize: 50, max: 100, page: 1, pageSize: 50},
		{name: "another default size", query: "", defaultSize: 20, max: 100, page: 1, pageSize: 20},
		{name: "explicit values", query: "?page=3&page_size=100", defaultSize: 50, max: 100, page: 3, pageSize: 100},
		{name: "page size at the maximum", query: "?page_size=100", defaultSize: 20, max: 100, page: 1, pageSize: 100},
		{name: "empty values use the defaults", query: "?page=&page_size=", defaultSize: 20, max: 100, page: 1, pageSize: 20},
		{name: "page zero", query: "?page=0", defaultSize: 50, max: 100, wantInvalidResult: true},
		{name: "negative page", query: "?page=-1", defaultSize: 50, max: 100, wantInvalidResult: true},
		{name: "non-numeric page", query: "?page=abc", defaultSize: 50, max: 100, wantInvalidResult: true},
		{name: "decimal page", query: "?page=1.5", defaultSize: 50, max: 100, wantInvalidResult: true},
		{name: "page size zero", query: "?page_size=0", defaultSize: 50, max: 100, wantInvalidResult: true},
		{name: "page size above the maximum", query: "?page_size=101", defaultSize: 20, max: 100, wantInvalidResult: true},
		{name: "page size above a smaller maximum", query: "?page_size=11", defaultSize: 5, max: 10, wantInvalidResult: true},
		{name: "non-numeric page size", query: "?page_size=many", defaultSize: 50, max: 100, wantInvalidResult: true},
		{name: "offset overflow", query: "?page=" + maximumInt + "&page_size=100", defaultSize: 50, max: 100, wantInvalidResult: true},
		{name: "offset overflow with the default size", query: "?page=" + maximumInt, defaultSize: 50, max: 100, wantInvalidResult: true},
		{name: "largest page whose offset fits", query: "?page=" + strconv.Itoa(int(^uint(0)>>1)/100) + "&page_size=100", defaultSize: 50, max: 100, page: int(^uint(0)>>1) / 100, pageSize: 100},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/v1/items"+testCase.query, nil)
			page, pageSize, err := ParsePagination(request, testCase.defaultSize, testCase.max)
			if (err != nil) != testCase.wantInvalidResult {
				t.Fatalf("ParsePagination() error = %v, want invalid = %v", err, testCase.wantInvalidResult)
			}
			if err == nil && (page != testCase.page || pageSize != testCase.pageSize) {
				t.Fatalf("ParsePagination() = (%d, %d), want (%d, %d)", page, pageSize, testCase.page, testCase.pageSize)
			}
		})
	}
}
