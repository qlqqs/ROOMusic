package upstreammock

import "net/http"

func (s *Server) pluginDemo(r *http.Request, empty bool) response {
	if empty {
		return missing()
	}
	q := r.URL.Query()
	const base = "https://lyrics.example.org/api"
	switch r.PathValue("kind") {
	case "lyrics":
		if q.Get("title") != "潮汐信箱" || q.Get("artist") != "纸屿" {
			return missing()
		}
		return ok(object{"kind": "plain", "text": "海岸的灯亮了\n潮汐寄来一封信", "source_url": base + "/songs/tide", "external_id": "demo-tide"})
	case "artist_bio":
		if q.Get("mbid") != "10000000-0000-4000-8000-000000000003" || q.Get("lang") != "zh" {
			return missing()
		}
		return ok(object{"lang": "zh", "text": "<p>纸屿是合成目录中的独奏者，作品记录海岸的声音。</p>", "source_url": base + "/artists/solo", "external_id": "demo-solo"})
	}
	return missing()
}
