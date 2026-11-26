package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/musicbrainz"
)

// 升级只读 ReleaseByID 的 JSON 响应缓存。GET 键使用 Requester 的规范化地址，
// 仅投影署名及录音编号，沿用来源缓存的有效期。
func cachedReleaseCredits(directory, baseURL string, now func() time.Time) func(string) (*sources.Release, error) {
	if directory == "" {
		return nil
	}
	if now == nil {
		now = time.Now
	}
	return func(id string) (*sources.Release, error) {
		u, err := url.Parse(baseURL + "/release/" + strings.ToLower(id))
		if err != nil {
			return nil, err
		}
		u.Host = strings.ToLower(u.Host)
		u.RawQuery = url.Values{"fmt": {"json"}, "inc": {"recordings isrcs artist-credits labels release-groups media discids"}}.Encode()
		sum := sha256.Sum256([]byte("GET " + u.String()))
		key := hex.EncodeToString(sum[:])
		body, err := os.ReadFile(filepath.Join(directory, "sources", "cache", "musicbrainz", key[:2], key+".json"))
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		var entry struct {
			Status    int
			Body      string
			ExpiresAt time.Time `json:"expires_at"`
		}
		var entity musicbrainz.Entity
		if json.Unmarshal(body, &entry) != nil || entry.Status != 200 || !now().Before(entry.ExpiresAt) ||
			json.Unmarshal([]byte(entry.Body), &entity) != nil || entity.ID != id {
			return nil, nil
		}
		project := func(artists []musicbrainz.ArtistCredit) []sources.Credit {
			var out []sources.Credit
			for _, a := range artists {
				out = append(out, sources.Credit{Name: a.Name, ArtistID: a.Artist.ID, JoinPhrase: a.JoinPhrase})
			}
			return out
		}
		r := &sources.Release{ID: id, Artists: project(entity.ArtistCredit)}
		for _, disc := range entity.Media {
			var medium sources.Medium
			for _, t := range disc.Tracks {
				if t.Recording != nil {
					medium.Tracks = append(medium.Tracks, sources.Track{RecordingID: t.Recording.ID, Artists: project(t.ArtistCredit)})
				}
			}
			r.Media = append(r.Media, medium)
		}
		return r, nil
	}
}
