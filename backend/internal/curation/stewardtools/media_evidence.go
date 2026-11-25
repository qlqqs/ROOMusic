package stewardtools

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/egress"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
	"golang.org/x/net/html"
)

func (a adapter) previewMedia(ctx context.Context, c tool.Caller, raw json.RawMessage) (tool.Preview, error) {
	var in mediaInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return tool.Preview{}, invalid()
	}
	if err := in.validate(); err != nil {
		return tool.Preview{}, toolError(err)
	}
	run, items, err := a.claimed(ctx, c)
	if err != nil {
		return tool.Preview{}, err
	}
	item, err := selectArtistItem(items, in.ItemID)
	if err != nil {
		return tool.Preview{}, err
	}
	urls := make([]string, 0, len(in.Citations))
	for _, c := range in.Citations {
		urls = append(urls, c.URL)
	}
	names, proof, citationErr := artistPageEvidence(ctx, run, item, urls)
	if citationErr != nil && !missingPageEvidence(citationErr) {
		return tool.Preview{}, toolError(citationErr)
	}
	p := tool.Preview{Target: strings.Join(names, " / "), Summary: "依据出处页补充艺人资料"}
	if in.Image != nil {
		if citationErr == nil {
			citationErr = a.checkImagePage(ctx, in.Image.PageURL, in.Image.URL)
			if citationErr != nil && !missingPageEvidence(citationErr) {
				return tool.Preview{}, toolError(citationErr)
			}
		}
		p.Changes = append(p.Changes, tool.Change{Field: "image", After: in.Image.URL})
	}
	if in.Bio != nil {
		p.Changes = append(p.Changes, tool.Change{Field: "bio", After: in.Bio.Text})
	}
	if citationErr != nil {
		p.Contradicted = true
		p.Verification, _ = json.Marshal(map[string]string{"reason": "no_verified_page"})
		return p, nil
	}
	p.Verification, err = json.Marshal(proof)
	return p, err
}

// 按实际 HTML 的图片节点和页面元数据核对出处与图址的关系。
func (a adapter) checkImagePage(ctx context.Context, page, asset string) error {
	client, err := egress.NewClient(a.mockHost.EgressOptions(egress.Options{Policy: egress.PublicOnly, Timeout: 10 * time.Second, MaxRedirects: 5}))
	if err != nil {
		return err
	}
	defer client.CloseIdleConnections()
	addr, err := curation.ParseArtistWebURL(page)
	if err != nil {
		return err
	}
	if err = a.checkPort(addr); err != nil {
		return err
	}
	redirect := client.CheckRedirect
	client.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if _, err := curation.ParseArtistWebURL(r.URL.String()); err != nil {
			return err
		}
		if err := a.checkPort(r.URL); err != nil {
			return err
		}
		r.Header.Del("Referer")
		return redirect(r, via)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, page, nil)
	if err != nil {
		return err
	}
	res, err := client.Do(req)
	if err != nil {
		return operations.Conflict("source_inactive", "图片出处暂时无法读取")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return operations.Conflict("no_evidence", "未取得图片出处")
	}
	z := html.NewTokenizer(io.LimitReader(res.Body, 2<<20))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			continue
		}
		t := z.Token()
		if t.Data != "img" && t.Data != "meta" {
			continue
		}
		var src, content, property string
		for _, a := range t.Attr {
			switch a.Key {
			case "src":
				src = a.Val
			case "content":
				content = a.Val
			case "name", "property":
				property = a.Val
			}
		}
		if t.Data == "meta" {
			if property != "og:image" && property != "twitter:image" {
				continue
			}
			src = content
		}
		if src != "" {
			ref, err := url.Parse(src)
			if err == nil && res.Request.URL.ResolveReference(ref).String() == asset {
				return nil
			}
		}
	}
	return operations.Conflict("no_evidence", "出处页没有引用拟提交的图片")
}

func missingPageEvidence(err error) bool {
	var f *operations.Failure
	if errors.As(err, &f) {
		return f.Code == "no_evidence" || f.Code == "invalid_citation"
	}
	var t *tool.Error
	if errors.As(err, &t) {
		return t.Code == "fetch_blocked_address" || t.Code == "fetch_blocked_port" || t.Code == "fetch_not_text"
	}
	return false
}

func artistPageEvidence(ctx context.Context, run *curation.ModelRun, item curation.PendingItem, urls []string) ([]string, []curation.VerificationCitation, error) {
	if run.EvidencePage == nil {
		return nil, nil, operations.Conflict("no_evidence", "需要本项读取的出处页")
	}
	var local struct {
		Names []string `json:"names"`
	}
	if err := json.Unmarshal(item.Evidence, &local); err != nil {
		return nil, nil, err
	}
	proof := make([]curation.VerificationCitation, 0, len(urls))
	for _, ref := range urls {
		content, err := run.EvidencePage(ctx, item.ID, ref)
		if err != nil {
			return nil, nil, err
		}
		text := []rune(content)
		start := -1
		for _, name := range local.Names {
			if name == "" {
				continue
			}
			if offset := strings.Index(strings.ToLower(content), strings.ToLower(name)); offset >= 0 {
				// 小写化可能改变字节数，截取位置按小写结果的字符数映射。
				lower := strings.ToLower(content)
				start = len([]rune(lower[:offset]))
				break
			}
		}
		if start < 0 {
			return nil, nil, operations.Conflict("no_evidence", "出处未提及该艺人")
		}
		proof = append(proof, curation.VerificationCitation{URL: ref, Quote: string(text[max(0, start-500):min(len(text), start+1500)])})
	}

	return local.Names, proof, nil
}
