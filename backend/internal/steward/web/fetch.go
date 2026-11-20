package web

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/text/encoding/htmlindex"
	"golang.org/x/text/transform"

	"github.com/qlqq/roomusic/backend/internal/steward/egress"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

const maxTextBytes = 100 << 10

// Page 是网页正文的固定输出，截断不表示读取失败。
type Page struct {
	URL       string `json:"url"`
	FinalURL  string `json:"final_url"`
	Title     string `json:"title"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
}

func validatePageURL(address string) error {
	u, err := url.Parse(address)
	if err != nil || len(address) > 2048 || u.User != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return failure("fetch_blocked_address")
	}
	if (u.Port() != "" && u.Port() != "80" && u.Port() != "443") || strings.HasSuffix(u.Host, ":") {
		return failure("fetch_blocked_port")
	}
	return nil
}

func pageHeaders(req *http.Request) {
	// 重定向不沿用自动生成的 Referer，也不向目标传递其他请求头。
	req.Header = make(http.Header)
	req.Header.Set("User-Agent", genericUserAgent)
	req.Header.Set("Accept", "text/html, text/plain, application/json, application/xml, text/xml, application/xhtml+xml")
}

// FetchPage 对所有调用者只允许公网，逐跳检查地址、端口与重定向次数。
func (s *Service) FetchPage(ctx context.Context, address string) (page Page, err error) {
	started := time.Now()
	defer func() {
		count := 0
		if err == nil {
			count = 1
		}
		s.logRequest(ctx, "fetch_page", address, started, count, err)
	}()
	if err := validatePageURL(address); err != nil {
		return page, err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	client, err := s.newClient(egress.Options{Policy: egress.PublicOnly, MaxRedirects: 5, Timeout: requestTimeout})
	if err != nil {
		return page, failure("fetch_failed")
	}
	defer client.CloseIdleConnections()
	redirect := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := validatePageURL(req.URL.String()); err != nil {
			return err
		}
		if err := redirect(req, via); err != nil {
			return err
		}
		pageHeaders(req)
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return page, failure("fetch_blocked_address")
	}
	pageHeaders(req)
	response, err := client.Do(req)
	if err != nil {
		return page, fetchFailure(err)
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusNotFound, http.StatusGone:
		return page, failure("fetch_not_found")
	case http.StatusUnauthorized, http.StatusForbidden:
		return page, failure("fetch_forbidden")
	case http.StatusOK:
	default:
		return page, failure("fetch_failed")
	}
	mediaType, params, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || !textMediaType(mediaType) {
		return page, failure("fetch_unsupported_type")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes+1))
	if err != nil {
		return page, fetchFailure(err)
	}
	truncated := len(data) > maxBodyBytes
	if truncated {
		data = data[:maxBodyBytes]
	}
	data, encodingTruncated, err := decodeText(data, params["charset"], truncated)
	if err != nil {
		return page, err
	}
	page = Page{URL: address, FinalURL: response.Request.URL.String(), Truncated: truncated || encodingTruncated}
	if mediaType == "text/html" || mediaType == "application/xhtml+xml" {
		page.Title, page.Text, err = extractHTML(data)
		if err != nil {
			return Page{}, failure("fetch_failed")
		}
	} else {
		page.Text = string(data)
	}
	page.Text, truncated = truncateText(page.Text, maxTextBytes)
	page.Truncated = page.Truncated || truncated
	if ctx.Err() != nil {
		return Page{}, fetchFailure(ctx.Err())
	}
	return page, nil
}

func textMediaType(mediaType string) bool {
	return mediaType == "text/html" || mediaType == "text/plain" || mediaType == "text/xml" || mediaType == "application/xml" || mediaType == "application/json" || strings.HasPrefix(mediaType, "application/") && (strings.HasSuffix(mediaType, "+json") || strings.HasSuffix(mediaType, "+xml"))
}

func decodeText(data []byte, charset string, truncated bool) ([]byte, bool, error) {
	charset = strings.ToLower(strings.TrimSpace(charset))
	if charset == "" || charset == "utf-8" || charset == "utf8" {
		if truncated {
			// 输入边界可能正好落在一个 UTF-8 字符中间，只移除末尾的不完整字符。
			for n := 0; n < 3 && len(data) > 0 && !utf8.Valid(data); n++ {
				data = data[:len(data)-1]
			}
		}
		if !utf8.Valid(data) {
			return nil, false, failure("fetch_unsupported_type")
		}
		return bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf}), false, nil
	}
	encoding, err := htmlindex.Get(charset)
	if err != nil {
		return nil, false, failure("fetch_unsupported_type")
	}
	decoded, err := io.ReadAll(io.LimitReader(transform.NewReader(bytes.NewReader(data), encoding.NewDecoder()), maxBodyBytes+1))
	if err != nil {
		return nil, false, failure("fetch_unsupported_type")
	}
	text, cut := truncateText(string(decoded), maxBodyBytes)
	return []byte(text), cut, nil
}

func truncateText(text string, limit int) (string, bool) {
	if len(text) <= limit {
		return text, false
	}
	end := limit
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end], true
}

func extractHTML(data []byte) (string, string, error) {
	tokenizer := html.NewTokenizer(bytes.NewReader(data))
	var title, body strings.Builder
	var skipped []string
	inTitle := false
	for {
		kind := tokenizer.Next()
		if kind == html.ErrorToken {
			if tokenizer.Err() != io.EOF {
				return "", "", tokenizer.Err()
			}
			break
		}
		token := tokenizer.Token()
		switch kind {
		case html.StartTagToken, html.SelfClosingTagToken:
			switch token.Data {
			case "script", "style", "nav", "header", "footer", "aside", "form", "noscript":
				skipped = append(skipped, token.Data)
			}
			if len(skipped) > 0 {
				continue
			}
			if token.Data == "title" {
				inTitle = true
			}
			if blockElement(token.Data) {
				body.WriteByte(' ')
			}
		case html.EndTagToken:
			if len(skipped) > 0 {
				for i := len(skipped) - 1; i >= 0; i-- {
					if skipped[i] == token.Data {
						skipped = skipped[:i]
						break
					}
				}
				continue
			}
			if token.Data == "title" {
				inTitle = false
			}
			if blockElement(token.Data) {
				body.WriteByte(' ')
			}
		case html.TextToken:
			if len(skipped) != 0 {
				continue
			}
			if inTitle {
				title.WriteString(token.Data)
			} else {
				body.WriteString(token.Data)
			}
		}
	}
	return strings.Join(strings.Fields(title.String()), " "), strings.Join(strings.Fields(body.String()), " "), nil
}

func blockElement(name string) bool {
	switch name {
	case "p", "div", "section", "article", "main", "h1", "h2", "h3", "h4", "h5", "h6", "br", "hr", "ul", "ol", "li", "table", "tr", "td", "th", "pre", "blockquote", "dl", "dt", "dd":
		return true
	}
	return false
}

func fetchFailure(err error) error {
	var typed *tool.Error
	if errors.As(err, &typed) {
		return typed
	}
	var outbound *egress.Error
	if errors.As(err, &outbound) {
		if outbound.Code == egress.CodeTooManyRedirects {
			return failure("fetch_too_many_redirects")
		}
		return failure("fetch_blocked_address")
	}
	var network net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &network) && network.Timeout() {
		return failure("fetch_timeout")
	}
	return failure("fetch_failed")
}
