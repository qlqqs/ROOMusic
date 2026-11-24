package upstreammock

import (
	"bytes"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"net/http"
	"strings"
)

func (s *Server) coverManifest(r *http.Request, empty bool) response {
	kind, id := r.PathValue("kind"), r.PathValue("id")
	for _, rel := range s.data.Releases {
		if empty || rel.Image == "" || !(kind == "release" && id == rel.ID || kind == "release-group" && id == rel.Group) {
			continue
		}
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		base := scheme + "://" + r.Host + "/coverartarchive/release/" + rel.ID
		image := base + "/1"
		return ok(object{"release": base, "images": []object{{"id": 1, "front": true, "back": false, "types": []string{"Front"}, "image": image, "thumbnails": object{"250": image + "-250", "500": image + "-500", "1200": image + "-1200"}}}})
	}
	return missing()
}
func (s *Server) coverImage(r *http.Request, empty bool) response {
	id, size, thumb := strings.Cut(r.PathValue("image"), "-")
	if empty || id != "1" || thumb && size != "250" && size != "500" && size != "1200" {
		return missing()
	}
	for _, rel := range s.data.Releases {
		if rel.ID == r.PathValue("id") && rel.Image != "" {
			data := s.data.covers[rel.Image]
			if thumb && http.DetectContentType(data) != "image/jpeg" {
				img, _, err := image.Decode(bytes.NewReader(data))
				if err != nil {
					return missing()
				}
				var encoded bytes.Buffer
				if err := jpeg.Encode(&encoded, img, nil); err != nil {
					return problem(500, "无法编码 JPEG 缩略图")
				}
				data = encoded.Bytes()
			}
			return response{status: 200, image: data}
		}
	}
	return missing()
}
