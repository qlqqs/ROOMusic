package upstreammock

import (
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

type includes []string

func (inc includes) has(key string) bool { return slices.Contains(inc, key) }

var searchIncludes = includes{"artist-credits", "release-groups", "media", "labels", "isrcs"}

func (d dataset) credit(id string) []object {
	a := d.artist(id)
	return []object{{"name": a.Name, "artist": object{"id": id, "name": a.Name}, "joinphrase": ""}}
}
func (d dataset) releaseEntity(v release, inc includes) object {
	row := object{"id": v.ID, "title": v.Title, "barcode": v.Barcode, "date": v.Date, "country": v.Country, "status": "Official"}
	if inc.has("artist-credits") {
		row["artist-credit"] = d.credit(v.Artist)
	}
	if inc.has("release-groups") {
		row["release-group"] = d.group(v.Group)
	}
	if inc.has("labels") {
		row["label-info"] = []object{{"label": object{"name": v.Label}, "catalog-number": v.Catalog}}
	}
	if inc.has("media") || inc.has("recordings") || inc.has("discids") {
		media := []object{}
		for i, m := range v.Media {
			part := object{"format": "CD", "position": i + 1, "track-count": len(m.Tracks)}
			if inc.has("discids") && m.Disc != "" {
				part["discs"] = []object{{"id": m.Disc}}
			}
			if inc.has("recordings") {
				tracks := []object{}
				for j, t := range m.Tracks {
					rec := d.recording(t.Recording)
					track := object{"id": t.ID, "position": j + 1, "number": strconv.Itoa(j + 1), "title": rec.Title, "length": rec.Length, "recording": d.recordingEntity(rec, inc)}
					if inc.has("artist-credits") {
						track["artist-credit"] = d.credit(rec.Artist)
					}
					tracks = append(tracks, track)
				}
				part["tracks"] = tracks
			}
			media = append(media, part)
		}
		row["media"] = media
	}
	return row
}
func (d dataset) recordingEntity(v recording, inc includes) object {
	row := object{"id": v.ID, "title": v.Title, "length": v.Length}
	if inc.has("artist-credits") {
		row["artist-credit"] = d.credit(v.Artist)
	}
	if inc.has("isrcs") {
		row["isrcs"] = []string{v.ISRC}
	}
	if inc.has("releases") {
		rels := []object{}
		for _, rel := range d.Releases {
			if rel.contains(v.ID) {
				rels = append(rels, d.releaseEntity(rel, searchIncludes))
			}
		}
		row["releases"] = rels
	}
	return row
}
func (d dataset) artistEntity(a artist, inc includes) object {
	row := object{"id": a.ID, "name": a.Name, "sort-name": a.Sort, "country": a.Country, "type": a.Type, "disambiguation": a.Note}
	if inc.has("url-rels") {
		row["relations"] = a.Media.relations()
	}
	if inc.has("aliases") {
		row["aliases"] = a.Aliases
	}
	if inc.has("releases") || inc.has("release-groups") {
		rels := []object{}
		groups := []group{}
		seen := map[string]bool{}
		for _, r := range d.Releases {
			if r.Artist != a.ID {
				continue
			}
			rels = append(rels, d.releaseEntity(r, nil))
			if !seen[r.Group] {
				groups = append(groups, d.group(r.Group))
				seen[r.Group] = true
			}
		}
		if inc.has("releases") {
			row["releases"] = rels
		}
		if inc.has("release-groups") {
			row["release-groups"] = groups
		}
	}
	if inc.has("recordings") {
		recs := []object{}
		for _, rec := range d.Recordings {
			if rec.Artist == a.ID {
				recs = append(recs, d.recordingEntity(rec, nil))
			}
		}
		row["recordings"] = recs
	}
	return row
}
func (d dataset) groupEntity(g group, inc includes) object {
	row := object{"id": g.ID, "title": g.Title, "primary-type": g.Type, "first-release-date": g.Date}
	rels := []object{}
	for _, r := range d.Releases {
		if r.Group == g.ID {
			rels = append(rels, d.releaseEntity(r, nil))
			if inc.has("artist-credits") {
				row["artist-credit"] = d.credit(r.Artist)
			}
		}
	}
	if inc.has("releases") {
		row["releases"] = rels
	}
	return row
}

// 查找器只组合带引号的字段、时长区间、曲数和条码的 AND/OR 表达式。
var fieldTerm = regexp.MustCompile(`^([a-z-]+):("(?:\\.|[^"\\])*"|\[[^\]]+\]|[^ ]+)`)
var escapedChar = regexp.MustCompile(`\\(.)`)

func matchesQuery(query string, fields map[string]string) bool {
	if !fieldTerm.MatchString(query) {
		for _, value := range fields {
			if strings.Contains(strings.ToLower(value), strings.ToLower(query)) {
				return true
			}
		}
		return false
	}
	matched := true
	for {
		m := fieldTerm.FindStringSubmatch(query)
		if m == nil {
			return false
		}
		key, term := m[1], m[2]
		if len(term) >= 2 && term[0] == '"' && term[len(term)-1] == '"' {
			term = term[1 : len(term)-1]
		}
		term = escapedChar.ReplaceAllString(term, "$1")
		val, exists := fields[key]
		same := strings.EqualFold(val, term)
		if key == "dur" {
			var low, high int
			length, err := strconv.Atoi(val)
			n, _ := fmt.Sscanf(term, "[%d TO %d]", &low, &high)
			same = err == nil && n == 2 && low <= length && length <= high
		}
		matched = matched && exists && same
		rest := query[len(m[0]):]
		switch {
		case rest == "":
			return matched
		case strings.HasPrefix(rest, " AND "):
			query = rest[5:]
		case strings.HasPrefix(rest, " OR "):
			if matched {
				return true
			}
			matched = true
			query = rest[4:]
		default:
			return false
		}
	}
}

func (s *Server) musicbrainz(r *http.Request, empty bool) response {
	d := s.data
	kind, id := r.PathValue("entity"), r.PathValue("id")
	params := r.URL.Query()
	inc := includes(strings.Fields(params.Get("inc")))
	query := strings.TrimSpace(params.Get("query"))
	limit, _ := strconv.Atoi(params.Get("limit"))
	rows := []object{}
	match := func(candidate string, fields map[string]string) bool {
		if empty {
			return false
		}
		if id != "" {
			return strings.EqualFold(candidate, id)
		}
		return matchesQuery(query, fields)
	}
	if id == "" {
		inc = searchIncludes
		if kind == "recording" {
			inc = append(slices.Clone(inc), "releases")
		}
	}
	switch kind {
	case "release":
		for _, v := range d.Releases {
			if match(v.ID, map[string]string{"release": v.Title, "artist": d.artist(v.Artist).Name, "tracks": strconv.Itoa(v.trackCount()), "catno": v.Catalog, "label": v.Label, "barcode": v.Barcode}) {
				rows = append(rows, d.releaseEntity(v, inc))
			}
		}
	case "recording", "isrc":
		for _, v := range d.Recordings {
			if kind == "isrc" {
				if empty || !strings.EqualFold(id, v.ISRC) {
					continue
				}
			} else if !match(v.ID, map[string]string{"recording": v.Title, "artist": d.artist(v.Artist).Name, "isrc": v.ISRC, "dur": strconv.Itoa(v.Length)}) {
				continue
			}
			rows = append(rows, d.recordingEntity(v, inc))
		}
		if kind == "isrc" {
			if len(rows) == 0 {
				return missing()
			}
			return ok(object{"isrc": id, "recordings": rows})
		}
	case "artist":
		for _, v := range d.Artists {
			if match(v.ID, map[string]string{"artist": v.Name}) {
				rows = append(rows, d.artistEntity(v, inc))
			}
		}
	case "release-group":
		for _, v := range d.Groups {
			if match(v.ID, map[string]string{"release-group": v.Title}) {
				rows = append(rows, d.groupEntity(v, inc))
			}
		}
	case "discid":
		for _, rel := range d.Releases {
			for _, m := range rel.Media {
				if !empty && m.Disc == id {
					rows = append(rows, d.releaseEntity(rel, inc))
					break
				}
			}
		}
		if len(rows) == 0 {
			return missing()
		}
		return ok(object{"id": id, "releases": rows, "release-count": len(rows)})
	default:
		return missing()
	}
	if id != "" {
		if len(rows) == 0 {
			return missing()
		}
		return ok(rows[0])
	}
	count := len(rows)
	if limit > 0 && limit < count {
		rows = rows[:limit]
	}
	return ok(object{kind + "s": rows, "count": count, "offset": 0})
}
