package stewardtools

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/egress"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

func (a adapter) mediaTool(available availability) tool.Definition {
	return tool.Definition{Name: "submit_artist_media", Description: "提交有网页引用的艺人图片或简介。图片同时提供图片地址与所在网页，简介用中文或英文纯文本。", MinRole: tool.RoleAdmin, Scope: tool.ScopeShared, Revertible: true, Available: available, InvalidInputCode: "invalid_input",
		Input:  json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"item_id":` + idSchema + `,"image":{"type":"object","additionalProperties":false,"properties":{"url":` + urlSchema + `,"page_url":` + urlSchema + `},"required":["url","page_url"]},"bio":{"type":"object","additionalProperties":false,"properties":{"text":{"type":"string"},"lang":{"type":"string","enum":["zh","en"]}},"required":["text","lang"]},"citations":{"type":"array","maxItems":5,"items":{"type":"object","additionalProperties":false,"properties":{"url":` + urlSchema + `,"title":{"type":"string","maxLength":500}},"required":["url","title"]}}},"required":["item_id","citations"]}`),
		Output: json.RawMessage(`{"type":"object"}`), Handler: a.submitMedia, Preview: a.previewMedia}
}

type mediaInput struct {
	ItemID    string             `json:"item_id"`
	Citations []catalog.Citation `json:"citations"`
	Image     *struct {
		URL     string `json:"url"`
		PageURL string `json:"page_url"`
	} `json:"image"`
	Bio *struct {
		Text string `json:"text"`
		Lang string `json:"lang"`
	} `json:"bio"`
}

func (in *mediaInput) validate() error {
	if in.Image == nil && in.Bio == nil {
		return invalid()
	}
	pageCited := false
	for i := range in.Citations {
		c := &in.Citations[i]
		c.Title = sources.PlainText(c.Title)
		if in.Image != nil && in.Image.PageURL == c.URL {
			pageCited = true
		}
	}
	if err := curation.ValidateArtistMediaCitations(in.Citations); err != nil {
		return err
	}
	if in.Image != nil {
		if !pageCited {
			return operations.Invalid("invalid_input", "图片所在网页须列在引用中")
		}
		if _, err := curation.ParseArtistWebURL(in.Image.URL); err != nil {
			return err
		}
	}
	if in.Bio != nil {
		in.Bio.Text = sources.PlainText(in.Bio.Text)
		length := utf8.RuneCountInString(in.Bio.Text)
		if length < 20 || length > 4000 || (in.Bio.Lang != "zh" && in.Bio.Lang != "en") {
			return operations.Invalid("invalid_input", "简介须为 20 至 4000 字的中文或英文纯文本")
		}
	}
	return nil
}

func (a adapter) checkPort(u *url.URL) error {
	if u.Port() == "" || u.Port() == "80" || u.Port() == "443" {
		return nil
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 || !a.mockHost.Allows(u.Hostname(), port) {
		return operations.Invalid("invalid_input", "网页端口未获放行")
	}
	return nil
}

func mediaRequestError(err error) error {
	if errors.Is(err, context.Canceled) {
		return err
	}
	var rejected *operations.Failure
	if errors.Is(err, egress.ErrDenied) || errors.As(err, &rejected) {
		return operations.Invalid("invalid_input", "图片或出处的地址不允许访问")
	}
	return sources.ErrTryLater
}

// 实际连接由 egress 检查每次 DNS 解析结果；每一跳都重新检查凭据与端口。
func (a adapter) prepareMedia(ctx context.Context, in mediaInput, download bool) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	options := egress.Options{Policy: egress.PublicOnly, Timeout: 10 * time.Second, MaxRedirects: 5}
	client, err := egress.NewClient(a.mockHost.EgressOptions(options))
	if err != nil {
		return nil, err
	}
	defer client.CloseIdleConnections()
	for _, citation := range in.Citations {
		u, err := curation.ParseArtistWebURL(citation.URL)
		if err != nil {
			return nil, err
		}
		if err = a.checkPort(u); err != nil {
			return nil, err
		}
		if err = egress.CheckEndpoint(ctx, client, citation.URL); err != nil {
			return nil, mediaRequestError(err)
		}
	}
	if !download {
		return nil, nil
	}
	imageURL, err := curation.ParseArtistWebURL(in.Image.URL)
	if err != nil {
		return nil, err
	}
	if err = a.checkPort(imageURL); err != nil {
		return nil, err
	}
	check := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if _, err := curation.ParseArtistWebURL(req.URL.String()); err != nil {
			return err
		}
		if err := a.checkPort(req.URL); err != nil {
			return err
		}
		req.Header.Del("Referer")
		return check(req, via)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL.String(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "image/*")
	response, err := client.Do(request)
	if err != nil {
		return nil, mediaRequestError(err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
		return nil, sources.ErrTryLater
	}
	if response.StatusCode != http.StatusOK {
		return nil, operations.Invalid("invalid_input", "图片下载失败")
	}
	const maxBytes = 16 << 20
	data, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return nil, mediaRequestError(err)
	}
	if len(data) > maxBytes {
		return nil, operations.Invalid("invalid_input", "图片超过 16 MiB")
	}
	return data, nil
}

func (a adapter) replayMedia(ctx context.Context, key, kind string) (curation.ExternalApplied, error) {
	var result curation.ExternalApplied
	raw, err := a.writer.ReplayArtistTool(ctx, key, kind)
	if err == nil && raw != nil {
		err = json.Unmarshal(raw, &result)
	}
	return result, err
}

func isMediaSkip(result curation.ExternalApplied, err error) bool {
	var f *operations.Failure
	return result.Skipped != "" && errors.As(err, &f) && f.Code == "nothing_to_apply"
}

func mediaResponse(image, bio curation.ExternalApplied) (json.RawMessage, error) {
	results := map[string]curation.ExternalApplied{}
	if image != (curation.ExternalApplied{}) {
		results["image"] = image
	}
	if bio != (curation.ExternalApplied{}) {
		results["bio"] = bio
	}
	return json.Marshal(results)
}

func (a adapter) submitMedia(ctx context.Context, c tool.Caller, raw json.RawMessage) (json.RawMessage, error) {
	var in mediaInput
	if json.Unmarshal(raw, &in) != nil {
		return nil, invalid()
	}
	if err := in.validate(); err != nil {
		return nil, toolError(err)
	}
	ctx, run, items, err := a.artistCall(ctx, c, in.ItemID, in)
	if err != nil {
		return nil, err
	}
	imageKey, bioKey := c.IdempotencyKey, c.IdempotencyKey
	if in.Image != nil && in.Bio != nil {
		bioKey = curation.DerivedKey(c.IdempotencyKey, "bio")
	}
	var image, bio curation.ExternalApplied
	if in.Image != nil {
		image, err = a.replayMedia(ctx, imageKey, operations.TypeArtistExternalArtwork)
		if err != nil {
			return nil, toolError(err)
		}
	}
	if in.Bio != nil {
		bio, err = a.replayMedia(ctx, bioKey, operations.TypeArtistExternalBio)
		if err != nil {
			return nil, toolError(err)
		}
	}
	needImage := in.Image != nil && image.OperationID == ""
	needBio := in.Bio != nil && bio.OperationID == ""
	item, err := selectArtistItem(items, in.ItemID)
	if err != nil {
		if !needImage && !needBio {
			return mediaResponse(image, bio)
		}
		// 最后一次提交可能已经完成，而同次提交的另一项因已有资料而跳过。
		if image.OperationID != "" || bio.OperationID != "" {
			saved, readErr := a.writer.PendingItem(ctx, in.ItemID)
			if readErr != nil {
				return nil, toolError(readErr)
			}
			if saved.ConversationID == run.ConversationID && saved.Status == "resolved" && saved.Resolution != nil && *saved.Resolution == "model_media" {
				if needImage {
					image.Skipped = "occupied"
				}
				if needBio {
					bio.Skipped = "occupied"
				}
				return mediaResponse(image, bio)
			}
		}
		return nil, err
	}
	if needImage || needBio {
		if c.ReviewID == "" || c.ReviewDecision != "approved" {
			return nil, &tool.Error{Code: tool.CodePermissionDenied, Message: "提交网页资料需要独立审查"}
		}
		proof, err := a.previewMedia(ctx, c, raw)
		if err != nil {
			return nil, err
		}
		if proof.Contradicted {
			return nil, toolError(operations.Conflict("no_evidence", "没有可以核实的媒体出处"))
		}
		data, err := a.prepareMedia(ctx, in, needImage)
		if err != nil {
			return nil, toolError(err)
		}
		batch, err := run.Batch(ctx)
		if err != nil {
			return nil, toolError(err)
		}
		if needImage {
			image, err = a.writer.ApplyArtistImage(ctx, curation.ArtistImageRequest{ArtistID: item.ArtistID, BatchID: batch, Key: imageKey, RequestID: c.RequestID, Citations: in.Citations,
				Image: sources.ArtistImage{Attribution: sources.Attribution{Source: "web_search", SourceURL: in.Image.PageURL}, ID: sources.ImageURLID(in.Image.URL), Data: data}})
			if err != nil && !isMediaSkip(image, err) {
				return nil, toolError(err)
			}
		}
		if needBio {
			bio, err = a.writer.ApplyArtistBio(ctx, curation.ArtistBioRequest{ArtistID: item.ArtistID, BatchID: batch, Key: bioKey, RequestID: c.RequestID, Citations: in.Citations,
				Bio: sources.ArtistBio{Attribution: sources.Attribution{Source: "web_search", SourceURL: in.Citations[0].URL}, ID: "artist", Lang: in.Bio.Lang, Text: in.Bio.Text}})
			if err != nil && !isMediaSkip(bio, err) {
				if image.OperationID != "" && !errors.Is(err, operations.ErrUnavailable) {
					if finishErr := a.writer.FinishArtistMedia(ctx, item); finishErr != nil {
						return nil, toolError(finishErr)
					}
				}
				return nil, toolError(err)
			}
		}
	}
	if image.OperationID != "" || bio.OperationID != "" {
		if err := a.writer.FinishArtistMedia(ctx, item); err != nil {
			return nil, toolError(err)
		}
	}
	return mediaResponse(image, bio)
}
