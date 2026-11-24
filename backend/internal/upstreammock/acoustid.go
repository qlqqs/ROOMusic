package upstreammock

import (
	"compress/gzip"
	"io"
	"net/http"
	"time"
)

func (s *Server) acoustid(r *http.Request, empty bool) response {
	if r.Header.Get("Content-Encoding") == "gzip" {
		reader, err := gzip.NewReader(r.Body)
		if err != nil {
			return problem(400, "无效的 gzip 表单")
		}
		defer reader.Close()
		r.Body = io.NopCloser(reader)
	}
	if err := r.ParseForm(); err != nil {
		return problem(400, "指纹表单无效")
	}
	hits := []object{}
	for _, id := range s.data.Fingerprints[r.PostForm.Get("fingerprint")] {
		rec := s.data.recording(id)
		if empty || rec.ID == "" {
			continue
		}
		releases := []object{}
		for _, rel := range s.data.Releases {
			if rel.contains(id) {
				date, _ := time.Parse("2006-01-02", rel.Date)
				releases = append(releases, object{"id": rel.ID, "title": rel.Title, "artists": []artist{s.data.artist(rel.Artist)}, "date": object{"year": date.Year(), "month": date.Month(), "day": date.Day()}, "country": rel.Country, "track_count": rel.trackCount(), "medium_count": len(rel.Media)})
			}
		}
		hits = append(hits, object{"id": rec.ID, "score": 0.99, "recordings": []object{{"id": rec.ID, "title": rec.Title, "duration": float64(rec.Length) / 1000, "artists": []artist{s.data.artist(rec.Artist)}, "releases": releases}}})
	}
	return ok(object{"status": "ok", "results": hits})
}
