package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/steward/egress"
	"github.com/qlqq/roomusic/backend/internal/steward/secret"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

var tavilyURL = "https://api.tavily.com/search"

// SearchResult 只向模型提供标题、地址和摘要。
type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

type searchOutput struct {
	Results []SearchResult `json:"results"`
}

func (s *Service) searchAvailable(ctx context.Context, caller tool.Caller) bool {
	settings, err := s.settings.GetUserSettings(ctx, caller.User.ID)
	return err == nil && (settings.SearchSource == "searxng" || settings.SearchSource == "tavily")
}

// Search 使用已保存的设置；失败只返回固定错误码，不携带服务响应或查询词。
func (s *Service) Search(ctx context.Context, user identity.User, query string, limit int) (results []SearchResult, err error) {
	if strings.TrimSpace(query) == "" || limit < 1 || limit > 10 {
		return nil, &tool.Error{Code: tool.CodeInvalidInput, Message: "查询不能为空；结果数必须在 1 到 10 之间"}
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	settings, err := s.settings.GetUserSettings(ctx, user.ID)
	if err != nil {
		return nil, failure("search_unreachable")
	}
	address := settings.SearXNGURL
	if settings.SearchSource == "tavily" {
		address = tavilyURL
	}
	started := time.Now()
	defer func() { s.logRequest(ctx, settings.SearchSource, address, started, len(results), err) }()
	var client *http.Client
	var req *http.Request
	switch settings.SearchSource {
	case "searxng":
		if address == "" {
			return nil, failure("search_not_configured")
		}
		client, err = s.endpointClient(ctx, user)
		if err != nil {
			return nil, failure("search_unreachable")
		}
		defer client.CloseIdleConnections()
		u, parseErr := url.Parse(strings.TrimRight(address, "/") + "/search")
		if parseErr != nil {
			return nil, failure("search_unreachable")
		}
		params := u.Query()
		params.Set("q", query)
		params.Set("format", "json")
		u.RawQuery = params.Encode()
		req, err = request(ctx, http.MethodGet, u.String(), nil, genericUserAgent)
	case "tavily":
		if settings.SearchKey == nil {
			return nil, failure("search_key_invalid")
		}
		key, openErr := s.secrets.Open(user.ID, secret.SearchKeyPurpose, settings.SearchKey.Ciphertext)
		if openErr != nil {
			return nil, failure("search_key_invalid")
		}
		client, err = s.newClient(egress.Options{Policy: egress.PublicOnly, Timeout: requestTimeout})
		if err != nil {
			return nil, failure("search_unreachable")
		}
		defer client.CloseIdleConnections()
		body, _ := json.Marshal(struct {
			Query             string `json:"query"`
			MaxResults        int    `json:"max_results"`
			SearchDepth       string `json:"search_depth"`
			IncludeAnswer     bool   `json:"include_answer"`
			IncludeRawContent bool   `json:"include_raw_content"`
		}{query, limit, "basic", false, false})
		req, err = request(ctx, http.MethodPost, address, bytes.NewReader(body), genericUserAgent)
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+string(key))
		}
	default:
		return nil, failure("search_not_configured")
	}
	if err != nil {
		return nil, failure("search_unreachable")
	}
	response, err := client.Do(req)
	if err != nil {
		var denied *egress.Error
		if errors.As(err, &denied) && denied.Code != egress.CodeTooManyRedirects {
			return nil, failure("egress_denied")
		}
		return nil, failure("search_unreachable")
	}
	defer response.Body.Close()
	if settings.SearchSource == "searxng" && response.StatusCode == 403 {
		return nil, failure("searxng_json_disabled")
	}
	if settings.SearchSource == "tavily" {
		switch response.StatusCode {
		case 401:
			return nil, failure("search_key_invalid")
		case 432, 433:
			return nil, failure("search_quota_exceeded")
		}
	}
	if response.StatusCode != 200 {
		return nil, failure("search_unreachable")
	}
	var payload struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if readJSON(response, &payload) != nil || payload.Results == nil {
		return nil, failure("search_unreachable")
	}
	results = make([]SearchResult, 0, min(limit, len(payload.Results)))
	for _, item := range payload.Results {
		if len(results) == limit {
			break
		}
		results = append(results, SearchResult{Title: item.Title, URL: item.URL, Snippet: item.Content})
	}
	return results, nil
}

// SearchTestResult 是设置页测试搜索的固定形状。
type SearchTestResult struct {
	OK          bool   `json:"ok"`
	ResultCount *int   `json:"result_count,omitempty"`
	Code        string `json:"code,omitempty"`
}

// TestSearch 仅发起固定的合成查询，不使用对话或曲库内容。
func (s *Service) TestSearch(ctx context.Context, user identity.User) SearchTestResult {
	results, err := s.Search(ctx, user, "MusicBrainz", 3)
	if err != nil {
		return SearchTestResult{Code: errorCode(err)}
	}
	count := len(results)
	return SearchTestResult{OK: true, ResultCount: &count}
}
