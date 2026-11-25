package httpapi

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

func coverBucket(size int) int {
	if size == 0 {
		return 0
	}
	for _, bucket := range []int{64, 128, 256, 512} {
		if size <= bucket {
			return bucket
		}
	}
	return 1024
}

func (h *Handlers) coverResource(r *http.Request) (string, error) {
	raw := r.Form.Get("id")
	if raw == "" {
		return "", errParameter
	}
	kind, id, ok := strings.Cut(raw, "-")
	id = strings.ToLower(id)
	if !ok || !uuid.Valid(id) {
		return "", errNotFound
	}
	switch kind {
	case "al":
		key, err := h.browse.Catalog.AlbumArtwork(r.Context(), id)
		if err != nil || key != "" {
			return key, err
		}
	case "ar":
		return h.browse.Catalog.ArtistArtwork(r.Context(), id)
	case "pl":
		d, err := h.browse.Personal.Playlist(r.Context(), memberID(r), id)
		if err != nil {
			return "", err
		}
		for _, cover := range d.Playlist.CoverGroups {
			if cover.Artwork != nil {
				return cover.Artwork.ResourceID, nil
			}
		}
	}
	return "", errNotFound
}

type coverBytes struct {
	mime string
	data []byte
}

func (h *Handlers) getCoverArt(w http.ResponseWriter, r *http.Request) error {
	n, err := number(r.Form, "size", 0)
	if err != nil {
		return err
	}
	bucket := coverBucket(n)
	id, err := h.coverResource(r)
	if err != nil {
		return err
	}
	// artwork.Store.Lookup 同时确认内容寻址的键安全，可用于本目录的文件名与强 ETag。
	if _, _, err := h.browse.ReadArtwork(r.Context(), id, true); err != nil {
		return err
	}
	key := id + "-" + strconv.Itoa(bucket)
	const cacheControl = "private, max-age=31536000, immutable"
	if httpserver.IfNoneMatchHits(r, key) {
		w.Header().Set("ETag", httpserver.EntityTag(key))
		w.Header().Set("Cache-Control", cacheControl)
		w.WriteHeader(http.StatusNotModified)
		return nil
	}
	var cover coverBytes
	if bucket == 0 {
		cover.mime, cover.data, err = h.browse.ReadArtwork(r.Context(), id, false)
	} else {
		value, err, _ := h.artworkFlights.Do(key, func() (any, error) { return h.scaledCover(r, id, key, bucket) })
		if err != nil {
			return err
		}
		cover = value.(coverBytes)
	}
	if err != nil {
		return err
	}
	w.Header().Set("ETag", httpserver.EntityTag(key))
	w.Header().Set("Cache-Control", cacheControl)
	w.Header().Set("Content-Type", cover.mime)
	w.Header().Set("Content-Length", strconv.Itoa(len(cover.data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(cover.data)
	return nil
}

func (h *Handlers) scaledCover(r *http.Request, id, key string, bucket int) (coverBytes, error) {
	dir := filepath.Join(h.browse.DataDirectory, "cache", "subsonic-art")
	base := filepath.Join(dir, key)
	for _, format := range []struct{ ext, mime string }{{".jpg", "image/jpeg"}, {".png", "image/png"}} {
		b, err := os.ReadFile(base + format.ext)
		if err == nil {
			return coverBytes{format.mime, b}, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return coverBytes{}, err
		}
	}
	mime, data, err := h.browse.ReadArtwork(r.Context(), id, false)
	if err != nil {
		return coverBytes{}, err
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return coverBytes{}, err
	}
	longest := max(cfg.Width, cfg.Height)
	if longest <= bucket {
		return coverBytes{mime, data}, nil
	}
	// 受管封面的入库上限按压缩字节计；解码前另外限制展开后的像素内存。
	if cfg.Height < 1 || cfg.Width < 1 || cfg.Width > (64<<20)/cfg.Height {
		return coverBytes{}, fmt.Errorf("cover dimensions exceed decoder budget")
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return coverBytes{}, err
	}
	dst := image.NewNRGBA(image.Rect(0, 0, max(1, cfg.Width*bucket/longest), max(1, cfg.Height*bucket/longest)))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
	var buf bytes.Buffer
	ext := ".jpg"
	if mime == "image/png" && !imageOpaque(src) {
		ext = ".png"
		err = png.Encode(&buf, dst)
	} else {
		mime = "image/jpeg"
		err = jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 85})
	}
	if err != nil {
		return coverBytes{}, err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return coverBytes{}, err
	}
	tmp, err := os.CreateTemp(dir, ".cover-*")
	if err != nil {
		return coverBytes{}, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		return coverBytes{}, err
	}
	if err := tmp.Close(); err != nil {
		return coverBytes{}, err
	}
	if err := os.Rename(tmp.Name(), base+ext); err != nil {
		return coverBytes{}, err
	}
	return coverBytes{mime, buf.Bytes()}, nil
}

func imageOpaque(img image.Image) bool {
	opaque, ok := img.(interface{ Opaque() bool })
	return ok && opaque.Opaque()
}
