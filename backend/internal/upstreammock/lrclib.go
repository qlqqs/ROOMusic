package upstreammock

import (
	"math"
	"net/http"
	"strconv"
	"strings"
)

func (s *Server) lyrics(r *http.Request, empty bool) response {
	q := r.URL.Query()
	exact := r.URL.Path == "/lrclib/api/get"
	duration := q.Get("duration")
	seconds, err := strconv.ParseFloat(duration, 64)
	if exact && duration != "" && err != nil {
		return missing()
	}
	matches := []lyric{}
	for _, v := range s.data.Lyrics {
		if empty {
			break
		}
		title := q.Get("track_name")
		if title == "" || !strings.Contains(strings.ToLower(v.Title), strings.ToLower(title)) {
			continue
		}
		if exact && !strings.EqualFold(v.Title, title) {
			continue
		}
		if artist := q.Get("artist_name"); artist != "" && !strings.EqualFold(v.Artist, artist) {
			continue
		}
		if album := q.Get("album_name"); album != "" && !strings.EqualFold(v.Album, album) {
			continue
		}
		if exact {
			if duration == "" || math.Abs(seconds-float64(v.Duration)) <= 2 {
				return ok(v)
			}
			continue
		}
		if duration == "" || duration == strconv.Itoa(v.Duration) {
			matches = append(matches, v)
		}
	}
	if exact {
		return missing()
	}
	return ok(matches)
}
