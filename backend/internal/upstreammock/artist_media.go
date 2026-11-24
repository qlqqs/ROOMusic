package upstreammock

import (
	"net/http"
	"net/url"
	"strings"
)

type article struct {
	Title string `json:"title"`
	Intro string `json:"intro"`
}

type artistMedia struct {
	QID          string             `json:"qid"`
	LinkWikidata bool               `json:"link_wikidata"`
	Commons      string             `json:"commons"`
	Image        string             `json:"image"`
	Articles     map[string]article `json:"articles"`
	FanartID     string             `json:"fanart_id"`
	AudioDBID    string             `json:"audiodb_id"`
	BiographyCN  string             `json:"biography_cn"`
	BiographyEN  string             `json:"biography_en"`
}

func articleURL(lang, title string) string {
	return "https://" + lang + ".wikipedia.org/wiki/" + url.PathEscape(strings.ReplaceAll(title, " ", "_"))
}

func (m artistMedia) relations() []object {
	links := []object{}
	if m.LinkWikidata {
		links = append(links, object{"type": "wikidata", "url": object{"resource": "https://www.wikidata.org/wiki/" + m.QID}})
	}
	for _, lang := range []string{"zh", "en"} {
		if page, ok := m.Articles[lang]; ok {
			links = append(links, object{"type": "wikipedia", "url": object{"resource": articleURL(lang, page.Title)}})
		}
	}
	return links
}

func (s *Server) wikidata(r *http.Request, empty bool) response {
	q := r.URL.Query()
	switch q.Get("action") {
	case "query":
		found := []object{}
		prefix := "haswbstatement:P434="
		if id, ok := strings.CutPrefix(q.Get("srsearch"), prefix); ok && !empty && q.Get("list") == "search" {
			if m := s.data.artist(id).Media; m.QID != "" {
				found = append(found, object{"ns": 0, "title": m.QID})
			}
		}
		return ok(object{"query": object{"search": found}})
	case "wbgetentities":
		qid := q.Get("ids")
		for _, a := range s.data.Artists {
			m := a.Media
			if empty || m.QID == "" || m.QID != qid {
				continue
			}
			claims, sites := object{}, object{}
			if m.Commons != "" {
				claims["P18"] = []object{{"mainsnak": object{"datavalue": object{"type": "string", "value": m.Commons}}, "rank": "normal"}}
			}
			for lang, p := range m.Articles {
				sites[lang+"wiki"] = object{"site": lang + "wiki", "title": p.Title, "url": articleURL(lang, p.Title)}
			}
			return ok(object{"entities": object{qid: object{"id": qid, "claims": claims, "sitelinks": sites}}})
		}
		return ok(object{"entities": object{qid: object{"id": qid, "missing": ""}}})
	default:
		return missing()
	}
}

func mediaURL(r *http.Request, path string) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host + path
}

func (s *Server) commons(r *http.Request, empty bool) response {
	q := r.URL.Query()
	if q.Get("iiurlwidth") != "960" {
		return problem(400, "缩略图仅支持宽度 960")
	}
	title := q.Get("titles")
	pages := []object{}
	for _, a := range s.data.Artists {
		m := a.Media
		if empty || m.Commons == "" || title != "File:"+m.Commons {
			continue
		}
		thumb := mediaURL(r, "/commons/thumb/960/"+url.PathEscape(m.Commons))
		metadata := object{"Artist": object{"value": "<span>合成影像小组</span>"}, "LicenseShortName": object{"value": "CC BY 4.0"}, "LicenseUrl": object{"value": "https://creativecommons.org/licenses/by/4.0/"}}
		pages = append(pages, object{"pageid": 9001, "title": title, "imageinfo": []object{{"url": thumb, "thumburl": thumb, "descriptionurl": "https://commons.wikimedia.org/wiki/File:" + url.PathEscape(m.Commons), "mime": http.DetectContentType(s.data.covers[m.Image]), "extmetadata": metadata}}})
	}
	if len(pages) == 0 {
		pages = append(pages, object{"ns": 6, "title": title, "missing": true})
	}
	return ok(object{"query": object{"pages": pages}})
}

func (s *Server) commonsThumb(r *http.Request, empty bool) response {
	for _, a := range s.data.Artists {
		if !empty && a.Media.Commons == r.PathValue("image") {
			return response{status: 200, image: s.data.covers[a.Media.Image]}
		}
	}
	return missing()
}

func (s *Server) wikipedia(r *http.Request, empty bool) response {
	lang := r.PathValue("lang")
	title := strings.ReplaceAll(r.PathValue("title"), "_", " ")
	for _, a := range s.data.Artists {
		p, okPage := a.Media.Articles[lang]
		if !empty && okPage && p.Title == title {
			return ok(object{"type": "standard", "title": title, "extract": p.Intro, "lang": lang, "wikibase_item": a.Media.QID, "content_urls": object{"desktop": object{"page": articleURL(lang, title)}}})
		}
	}
	return missing()
}

func (s *Server) fanart(r *http.Request, empty bool) response {
	if r.Header.Get("api-key") == "" {
		return problem(401, "缺少项目密钥")
	}
	a := s.data.artist(r.PathValue("id"))
	if empty || a.Media.FanartID == "" {
		return missing()
	}
	path := mediaURL(r, "/fanarttv/images/"+a.Media.FanartID+".png")
	return ok(object{"name": a.Name, "mbid_id": a.ID, "artistthumb": []object{
		{"id": a.Media.FanartID + "1", "url": path + "?version=1", "likes": "2"},
		{"id": a.Media.FanartID + "2", "url": path + "?version=2", "likes": "12"},
	}})
}

func (s *Server) fanartImage(r *http.Request, empty bool) response {
	for _, a := range s.data.Artists {
		if !empty && a.Media.FanartID != "" && a.Media.FanartID+".png" == r.PathValue("image") {
			return response{status: 200, image: s.data.covers[a.Media.Image]}
		}
	}
	return missing()
}

func (s *Server) audioDB(r *http.Request, empty bool) response {
	a := s.data.artist(r.URL.Query().Get("i"))
	m := a.Media
	if empty || m.AudioDBID == "" {
		return ok(object{"artists": nil})
	}
	return ok(object{"artists": []object{{"idArtist": m.AudioDBID, "strArtist": a.Name, "strMusicBrainzID": a.ID, "strArtistThumb": mediaURL(r, "/theaudiodb/images/"+m.AudioDBID+".png"), "strBiographyCN": m.BiographyCN, "strBiography": m.BiographyEN}}})
}

func (s *Server) audioDBImage(r *http.Request, empty bool) response {
	for _, a := range s.data.Artists {
		if !empty && a.Media.AudioDBID != "" && a.Media.AudioDBID+".png" == r.PathValue("image") {
			return response{status: 200, image: s.data.covers[a.Media.Image]}
		}
	}
	return missing()
}
