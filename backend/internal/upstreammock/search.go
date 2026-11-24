package upstreammock

import (
	"net/http"
	"slices"
	"strings"
)

func (s *Server) search(r *http.Request, empty bool) response {
	results := s.data.Search
	query := r.URL.Query().Get("q")
	limit := 0
	if r.Method == "POST" {
		var input struct {
			Query      string
			MaxResults int `json:"max_results"`
		}
		if err := decodeJSON(r.Body, &input); err != nil {
			return problem(400, "搜索请求 JSON 无效")
		}
		query = input.Query
		limit = input.MaxResults
	}
	for _, name := range []string{"纸屿", "海岸来信", "Paper Islet"} {
		if strings.Contains(query, name) {
			results = append(slices.Clone(results), artistSearchPage(mediaURL(r, "")))
			break
		}
	}
	if limit > 0 && limit < len(results) {
		results = results[:limit]
	}
	if empty || query == "" || results == nil {
		results = []object{}
	}
	return ok(object{"query": query, "results": results})
}
