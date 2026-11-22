package coverartarchive

import (
	"bytes"
	"encoding/json"
	"net/url"
	"path"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

func decodeManifest(body []byte) ([]sources.CoverImage, bool) {
	var manifest struct {
		Release any
		Images  []any
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if decoder.Decode(&manifest) != nil || manifest.Images == nil || len(bytes.TrimSpace(body[decoder.InputOffset():])) != 0 {
		return nil, false
	}
	address, _ := manifest.Release.(string)
	releaseID := ""
	if u, err := url.Parse(address); err == nil {
		if id := path.Base(u.Path); uuid.Valid(id) {
			releaseID = strings.ToLower(id)
		}
	}
	images := make([]sources.CoverImage, 0, len(manifest.Images))
	for _, item := range manifest.Images {
		fields, _ := item.(map[string]any)
		image := sources.CoverImage{ID: imageID(fields["id"]), ReleaseID: releaseID}
		image.URL, _ = fields["image"].(string)
		image.Front, _ = fields["front"].(bool)
		image.Back, _ = fields["back"].(bool)
		types, _ := fields["types"].([]any)
		for _, v := range types {
			if s, ok := v.(string); ok {
				image.Types = append(image.Types, s)
			}
		}
		if thumbs, ok := fields["thumbnails"].(map[string]any); ok {
			image.Thumbnails = make(map[string]string, len(thumbs))
			for k, v := range thumbs {
				if s, ok := v.(string); ok {
					image.Thumbnails[k] = s
				}
			}
		}
		images = append(images, image)
	}
	return images, true
}

func imageID(value any) string {
	var id string
	switch v := value.(type) {
	case json.Number:
		id = v.String()
	case string:
		id = v
	default:
		return ""
	}
	if id == "" || strings.Trim(id, "0123456789") != "" {
		return ""
	}
	normalized := strings.TrimLeft(id, "0")
	if normalized == "" {
		return "0"
	}
	return normalized
}
