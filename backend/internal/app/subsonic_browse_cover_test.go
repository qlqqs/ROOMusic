package app

import (
	"bytes"
	"encoding/xml"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/artwork"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/subsonic/protocol"
)

func subsonicBrowsePNG(t *testing.T, width, height int, shade uint8, alpha bool) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	painted := img.Bounds()
	if alpha {
		painted.Min.X = width / 2
	}
	draw.Draw(img, painted, image.NewUniform(color.NRGBA{R: shade, G: 80, B: 150, A: 255}), image.Point{}, draw.Src)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const subsonicCoverCacheControl = "private, max-age=31536000, immutable"

func TestSubsonicBrowseCoverArt(t *testing.T) {
	f := newSubsonicBrowseFixture(t)
	f.publish(t)
	release := f.albums[subsonicZebra]
	albumCover := "al-" + release.group
	get := func(id, size, etag string) *httptest.ResponseRecorder {
		params := f.params("json", "id", id, "size", size)
		req := httptest.NewRequest(http.MethodGet, "/rest/getCoverArt?"+params.Encode(), nil)
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		response := httptest.NewRecorder()
		f.app.handler.ServeHTTP(response, req)
		return response
	}

	t.Run("BucketsStayTransparent", func(t *testing.T) {
		for _, tc := range []struct {
			size  string
			width int
		}{
			{"0", 1200}, {"1", 64}, {"64", 64}, {"65", 128}, {"128", 128}, {"129", 256}, {"257", 512}, {"513", 1024}, {"9999", 1024},
		} {
			t.Run(tc.size, func(t *testing.T) {
				response := get(albumCover, tc.size, "")
				image := requireSubsonicBrowseImage(t, response, "png", tc.width, tc.width/2)
				if _, _, _, alpha := image.At(0, 0).RGBA(); alpha != 0 {
					t.Errorf("透明 PNG 的透明像素被填充：alpha=%d", alpha)
				}
			})
		}
	})

	t.Run("CacheHitAndRevalidation", func(t *testing.T) {
		first := get(albumCover, "128", "")
		requireSubsonicBrowseImage(t, first, "png", 128, 64)
		etag := first.Header().Get("ETag")
		var key string
		if err := f.app.database.QueryRow(`SELECT storage_key FROM release_artworks WHERE release_id=$1`, release.release).Scan(&key); err != nil {
			t.Fatal(err)
		}
		cacheDir := filepath.Join(f.app.dataDirectory, "cache", "subsonic-art")
		cached, err := os.ReadFile(filepath.Join(cacheDir, key+"-128.png"))
		if err != nil || !bytes.Equal(first.Body.Bytes(), cached) {
			t.Fatalf("缓存文件和 HTTP 缩放结果不一致：%v", err)
		}

		// 暂时移走受管原图。此时缩放原图会失败，同档请求仍能从缓存读取。
		source := filepath.Join(f.app.dataDirectory, "artwork", key)
		staged := source + ".test"
		if err := os.Rename(source, staged); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := os.Rename(staged, source); err != nil {
				t.Error(err)
			}
		})
		hit := get(albumCover, "65", "")
		requireSubsonicBrowseImage(t, hit, "png", 128, 64)
		if hit.Header().Get("ETag") != etag || !bytes.Equal(first.Body.Bytes(), hit.Body.Bytes()) {
			t.Error("同档命中应返回相同的 ETag 和图像")
		}
		conditional := get(albumCover, "128", etag)
		if conditional.Code != http.StatusNotModified || conditional.Body.Len() != 0 ||
			conditional.Header().Get("ETag") != etag || conditional.Header().Get("Cache-Control") != subsonicCoverCacheControl {
			t.Errorf("条件请求返回 HTTP %d，头 %v，正文 %d 字节", conditional.Code, conditional.Header(), conditional.Body.Len())
		}
	})

	t.Run("ArtistFallback", func(t *testing.T) {
		artist := f.artists["Atlas Collective"]
		album := get(albumCover, "128", "")
		requireSubsonicBrowseImage(t, album, "png", 128, 64)
		fallback := get("ar-"+artist, "128", "")
		requireSubsonicBrowseImage(t, fallback, "png", 128, 64)
		if !bytes.Equal(fallback.Body.Bytes(), album.Body.Bytes()) {
			t.Error("艺人无照片时应使用专辑封面")
		}
	})

	t.Run("Playlist", func(t *testing.T) {
		created := f.app.request(t, http.MethodPost, playlistsURL,
			map[string]any{"name": "Subsonic Cover List", "track_ids": []string{f.tracks["Overture"]}},
			&f.admin, idempotencyHeader(uuid.New()))
		if created.Code != http.StatusCreated {
			t.Fatalf("创建合成歌单：HTTP %d %s", created.Code, created.Body)
		}
		id := "pl-" + personalState(t, created).ID
		cover := get(id, "128", "")
		requireSubsonicBrowseImage(t, cover, "png", 128, 64)
		album := get(albumCover, "128", "")
		requireSubsonicBrowseImage(t, album, "png", 128, 64)
		if !bytes.Equal(cover.Body.Bytes(), album.Body.Bytes()) {
			t.Error("歌单封面应使用其中曲目的专辑封面")
		}
		const other = "subsonic-cover-other"
		createSubsonicMember(t, f.app, &f.admin, other)
		params := f.params("json", "id", id, "size", "128")
		params.Set("u", other)
		r := requestSubsonic(f.app, http.MethodGet, "getCoverArt", params)
		requireSubsonicFailure(t, r, http.StatusOK, 70)
	})

	t.Run("ArtistOwnImage", func(t *testing.T) {
		artist := f.artists["Atlas Collective"]
		portrait := subsonicBrowsePNG(t, 40, 28, 225, false)
		revision := readArtistMedia(t, f.app, &f.admin, artist).Revision
		uploaded := artistImageUpload(t, f.app, &f.admin, artist, uuid.New(), revision, portrait)
		requireArtistMediaEdit(t, uploaded, artist, "", revision+1)
		params := f.params("xml", "id", "ar-"+artist, "size", "128")
		cover := requestSubsonic(f.app, http.MethodGet, "getCoverArt", params)
		requireSubsonicBrowseImage(t, cover, "png", 40, 28)
		if !bytes.Equal(cover.Body.Bytes(), portrait) {
			t.Error("艺人原图小于请求档位时应保留原图字节")
		}
	})

	t.Run("NewFingerprint", func(t *testing.T) {
		original := get(albumCover, "128", "")
		requireSubsonicBrowseImage(t, original, "png", 128, 64)
		opaque := subsonicBrowsePNG(t, 620, 310, 210, false)
		if err := artwork.NewStore(f.app.dataDirectory).Save(t.Context(), f.app.database, release.release, opaque, "folder"); err != nil {
			t.Fatal(err)
		}
		updated := get(albumCover, "128", original.Header().Get("ETag"))
		requireSubsonicBrowseImage(t, updated, "jpeg", 128, 64)
		if updated.Header().Get("ETag") == original.Header().Get("ETag") || bytes.Equal(updated.Body.Bytes(), original.Body.Bytes()) {
			t.Error("替换原图后，同一资源、同一尺寸的内容与 ETag 都应更新")
		}
		unscaled := get(albumCover, "1024", "")
		requireSubsonicBrowseImage(t, unscaled, "png", 620, 310)
		if !bytes.Equal(unscaled.Body.Bytes(), opaque) {
			t.Error("小于档位的原图应保留原始字节")
		}
	})

	t.Run("MissingAndInvalid", func(t *testing.T) {
		for _, tc := range []struct{ name, id string }{
			{"MissingAlbum", "al-" + uuid.New()},
			{"MissingArtist", "ar-" + uuid.New()},
			{"MissingPlaylist", "pl-" + uuid.New()},
			{"InvalidPrefix", "wrong-" + release.group},
			{"AlbumWithoutCover", "al-" + f.albums[subsonicBlue].group},
			{"ArtistWithoutCover", "ar-" + f.artists["Cedar Band"]},
		} {
			t.Run(tc.name, func(t *testing.T) {
				requireSubsonicFailure(t, get(tc.id, "128", ""), http.StatusOK, 70)
			})
		}
		requireSubsonicFailure(t, get(albumCover, "x", ""), http.StatusOK, 10)
		requireSubsonicFailure(t, get("", "128", ""), http.StatusOK, 10)
		xmlReply := requestSubsonic(f.app, http.MethodGet, "getCoverArt", f.params("xml", "id", "al-"+uuid.New()))
		requireSubsonicHTTP(t, xmlReply, http.StatusOK, "application/xml; charset=utf-8")
		var body protocol.Response
		if err := xml.Unmarshal(xmlReply.Body.Bytes(), &body); err != nil || body.Status != "failed" ||
			body.Error == nil || body.Error.Code != 70 || body.Error.Message == "" {
			t.Errorf("XML 未找到响应不符：%s（%v）", xmlReply.Body, err)
		}
	})
}

func requireSubsonicBrowseImage(t *testing.T, response *httptest.ResponseRecorder, format string, width, height int) image.Image {
	t.Helper()
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "image/"+format ||
		response.Header().Get("Cache-Control") != subsonicCoverCacheControl ||
		response.Header().Get("Content-Length") != strconv.Itoa(response.Body.Len()) || len(response.Result().Cookies()) != 0 {
		t.Fatalf("图片 HTTP 状态、响应头或长度不符：%d，%v，%d 字节", response.Code, response.Header(), response.Body.Len())
	}
	etag := response.Header().Get("ETag")
	if len(etag) < 3 || !strings.HasPrefix(etag, "\"") || !strings.HasSuffix(etag, "\"") {
		t.Fatalf("封面 ETag 缺失或无法作为强校验值：%q", etag)
	}
	img, decodedFormat, err := image.Decode(bytes.NewReader(response.Body.Bytes()))
	if err != nil || decodedFormat != format || img.Bounds().Size() != (image.Point{X: width, Y: height}) {
		t.Fatalf("解码图片格式或尺寸不符：%v，%s，期望 %s %dx%d", err, decodedFormat, format, width, height)
	}
	return img
}
