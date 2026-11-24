package upstreammock

import "net/http"

func (s *Server) search(r *http.Request, empty bool) response {
	results := s.data.Search
	query := r.URL.Query().Get("q")
	if r.Method == "POST" {
		var input struct {
			Query      string
			MaxResults int `json:"max_results"`
		}
		if err := decodeJSON(r.Body, &input); err != nil {
			return problem(400, "搜索请求 JSON 无效")
		}
		query = input.Query
		if input.MaxResults > 0 && input.MaxResults < len(results) {
			results = results[:input.MaxResults]
		}
	}
	if empty || query == "" || results == nil {
		results = []object{}
	}
	return ok(object{"query": query, "results": results})
}
