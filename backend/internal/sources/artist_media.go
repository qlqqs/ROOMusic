package sources

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/net/html"
)

// ArtistLinks 保存从已对位的 MBID 推出的条目号、文件名与维基百科标题。
type ArtistLinks struct {
	MBID, WikidataID, CommonsFile, WikipediaZH, WikipediaEN string
}

type ArtistLinkFinder interface {
	ArtistLinks(context.Context, ArtistLinks) (ArtistLinks, error)
}
type ArtistImageFinder interface {
	ArtistImage(context.Context, ArtistLinks) (ArtistImage, error)
}
type ArtistBioFinder interface {
	ArtistBio(context.Context, ArtistLinks, string) (ArtistBio, error)
}

type Attribution struct{ Source, SourceURL, Author, License, LicenseURL string }
type ArtistImage struct {
	Attribution
	ID   string
	Data []byte
}
type ArtistBio struct {
	Attribution
	ID, Text, Lang string
}

// ImageURLID 用于源没有提供独立图片编号的缩略图。
func ImageURLID(address string) string {
	sum := sha256.Sum256([]byte(address))
	return hex.EncodeToString(sum[:])
}

func NewWikimediaRequester(d Declaration, opts Options, language string) *Requester {
	r := NewRequester(d, opts)
	r.wikimedia = true
	r.language = language
	return r
}

// MediaHosts 同时约束外部返回的下载地址与重定向地址。
func MediaHosts(mock MockHost, hosts ...string) func(*url.URL) bool {
	return func(u *url.URL) bool {
		if u.User != nil {
			return false
		}
		if mock.Matches(u) {
			return u.Scheme == "http" || u.Scheme == "https"
		}
		return u.Scheme == "https" && (u.Port() == "" || u.Port() == "443") && slices.Contains(hosts, strings.ToLower(u.Hostname()))
	}
}

func (r *Requester) DownloadArtistImage(ctx context.Context, address string) ([]byte, error) {
	u, err := url.Parse(address)
	if err != nil || r.allowRedirect == nil || !r.allowRedirect(u) {
		return nil, ErrNotFound
	}
	data, _, err := r.GetBytes(ctx, address, "image/*", 16<<20)
	return data, err
}

// PlainText 保留可见文字；艺人资料和 Commons 的作者署名都可能含 HTML。
func PlainText(value string) string {
	return strings.Join(strings.Fields(visibleText(value)), " ")
}

func visibleText(value string) string {
	var b strings.Builder
	z := html.NewTokenizer(strings.NewReader(value))
	hidden := ""
	for {
		typ := z.Next()
		switch typ {
		case html.ErrorToken:
			return b.String()
		case html.StartTagToken, html.EndTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			tag := string(name)
			if tag == "script" || tag == "style" {
				if typ == html.StartTagToken {
					hidden = tag
				} else if typ == html.EndTagToken && hidden == tag {
					hidden = ""
				}
			}
			if tag == "p" || tag == "div" || tag == "br" {
				b.WriteByte('\n')
			}
		case html.TextToken:
			if hidden == "" {
				b.Write(z.Text())
			}
		}
	}
}

var citation = regexp.MustCompile(`\[[0-9]+\]`)

func BioText(value string) string {
	for paragraph := range strings.SplitSeq(visibleText(value), "\n") {
		text := strings.Join(strings.Fields(citation.ReplaceAllString(paragraph, "")), " ")
		if text != "" {
			runes := []rune(text)
			return string(runes[:min(len(runes), 4000)])
		}
	}
	return ""
}

var wikidataID = regexp.MustCompile(`^Q[1-9][0-9]*$`)

func ValidWikidataID(id string) bool { return wikidataID.MatchString(id) }
