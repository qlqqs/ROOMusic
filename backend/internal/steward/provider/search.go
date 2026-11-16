package provider

import (
	"encoding/json"
	"net/url"
	"unicode/utf8"
)

const (
	maxSearchResults = 10
	maxCitations     = 20
	maxQueryRunes    = 400
	maxTitleRunes    = 200
	maxURLRunes      = 2048
)

// Citation 是可展示的网页来源，不包含提供方的加密内容或引用正文。
type Citation struct {
	URL   string `json:"url"`
	Title string `json:"title"`
}

// Search 是一次服务端搜索的展示投影。
type Search struct {
	SearchID  string     `json:"search_id"`
	Query     string     `json:"query"`
	Status    string     `json:"status"`
	Results   []Citation `json:"results"`
	ErrorCode string     `json:"error_code,omitempty"`
}

// CleanCitations 只保留有界的 HTTP(S) 来源，按 URL 去重并保持原顺序。
func CleanCitations(values []Citation) []Citation {
	result := make([]Citation, 0, min(len(values), maxCitations))
	seen := make(map[string]int, min(len(values), maxCitations))
	for _, value := range values {
		if utf8.RuneCountInString(value.URL) > maxURLRunes {
			continue
		}
		u, err := url.Parse(value.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
			continue
		}
		value.Title = truncateRunes(value.Title, maxTitleRunes)
		if i, exists := seen[value.URL]; exists {
			if result[i].Title == "" {
				result[i].Title = value.Title
			}
			continue
		}
		if len(result) == maxCitations {
			continue
		}
		seen[value.URL] = len(result)
		result = append(result, value)
	}
	return result
}

// CleanSearch 限制搜索的展示投影；提供方原始块由运行时另行限制落库大小。
func CleanSearch(search Search) Search {
	search.Query = truncateRunes(search.Query, maxQueryRunes)
	search.Results = CleanCitations(search.Results)
	if len(search.Results) > maxSearchResults {
		search.Results = search.Results[:maxSearchResults]
	}
	return search
}

func truncateRunes(value string, limit int) string {
	count := 0
	for i := range value {
		if count == limit {
			return value[:i]
		}
		count++
	}
	return value
}

func protocolBlocks(message Message, protocol Protocol) []json.RawMessage {
	var blocks []json.RawMessage
	if message.Role != RoleAssistant {
		return blocks
	}
	for _, block := range message.ProviderBlocks {
		if block.Protocol == protocol {
			blocks = append(blocks, block.Data)
		}
	}
	return blocks
}
