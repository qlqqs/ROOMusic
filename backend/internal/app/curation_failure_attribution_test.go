package app

import (
	"database/sql"
	"encoding/json"
	"maps"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/steward/provider"
)

// 同一项可以在多档、多次领取中出现。逐次保留证据，不能把两次各缺一半的材料拼成一次完整判断。
type metricRun struct {
	ID, Object, Kind, Status, Resolution string
	Item                                 map[string]any
	Reads                                []any
	Unavailable                          bool
	final                                bool
}

func (r metricRun) closedUnavailable() bool {
	return r.final && r.Unavailable && (r.Status == "shelved" || r.Status == "resolved" && (r.Resolution == "unlisted" || r.Resolution == "no_result" || r.Resolution == "suggestion_recorded"))
}

type metricHistory map[string][]*metricRun

func metricHistories(t *testing.T, app *integrationTestApplication) metricHistory {
	t.Helper()
	items := map[string]metricRun{}
	metricReadRows(t, app, `SELECT id,coalesce(target_id,artist_id),kind,status,coalesce(resolution,'') FROM curation_pending_items`, func(rows *sql.Rows) {
		var r metricRun
		requireCurationOK(t, rows.Scan(&r.ID, &r.Object, &r.Kind, &r.Status, &r.Resolution))
		items[r.ID] = r
	})
	history := metricHistory{}
	active := map[string][]*metricRun{}
	last := map[string]*metricRun{}
	metricReadRows(t, app, `SELECT m.conversation_id,m.content FROM steward_messages m JOIN steward_conversations c ON c.id=m.conversation_id
		WHERE c.kind='curation' AND m.role IN ('user','tool') ORDER BY m.created_at,m.conversation_id,m.sequence`, func(rows *sql.Rows) {
		var conversation string
		var data []byte
		requireCurationOK(t, rows.Scan(&conversation, &data))
		var msg struct {
			provider.Message
			Source struct{ Name string }
		}
		requireCurationOK(t, json.Unmarshal(data, &msg))
		if msg.Role == provider.RoleUser {
			active[conversation] = nil
			for _, block := range msg.Content {
				_, body, ok := strings.Cut(block.Text, `<roomusic-source type="curation_pending" trust="untrusted">`)
				if !ok {
					continue
				}
				body, _, ok = strings.Cut(body, "</roomusic-source>")
				if !ok {
					t.Fatal("整理消息缺少来源结束标记")
				}
				var batch []map[string]any
				requireCurationOK(t, json.Unmarshal([]byte(body), &batch))
				for _, item := range batch {
					id, _ := item["item_id"].(string)
					r, ok := items[id]
					if !ok {
						continue
					}
					if previous := last[id]; previous != nil {
						previous.final = false
					}
					r.Item = item
					r.final = true
					last[id] = &r
					history[r.Object] = append(history[r.Object], &r)
					active[conversation] = append(active[conversation], &r)
				}
			}
			return
		}
		// 多项选择档只返回选择，不使用工具。读工具档和联网档每次只领取一项。
		if len(active[conversation]) != 1 {
			return
		}
		r := active[conversation][0]
		for _, block := range msg.Content {
			result := block.ToolResult
			if result == nil {
				continue
			}
			var v any
			if json.Unmarshal([]byte(result.Content), &v) != nil {
				continue
			}
			if result.IsError {
				errObj, _ := v.(map[string]any)
				code := errObj["code"]
				r.Unavailable = r.Unavailable || code == "source_unavailable" || code == "rate_limited"
				continue
			}
			switch msg.Source.Name {
			case "read_pending_items", "search_music_metadata", "get_music_metadata", "web_search", "fetch_page":
				r.Reads = append(r.Reads, v)
			}
		}
	})
	return history
}

type metricNeed struct{ Object, Kind, Value string }
type metricCheck struct {
	Kind      string `json:"required_evidence"`
	Value     string `json:"expected_value,omitempty"`
	ItemID    string `json:"item_id,omitempty"`
	Seen      bool   `json:"value_seen"`
	Supported bool   `json:"evidence_complete"`
}
type metricAttribution struct {
	Category string        `json:"category"`
	Checks   []metricCheck `json:"checks"`
}

func metricScoreNeeds(e metricEntry, s metricScore, w metricExpect) []metricNeed {
	var needs []metricNeed
	for _, issue := range s.Reasons {
		// 没有期望外部值的错写单列在验收原因中，不能把诊断用的中文占位当成标注。
		if issue.Expected == "不建立外部对应" || issue.Expected == "保留写入前的值" {
			continue
		}
		n := metricNeed{Object: e.release, Kind: issue.Field, Value: issue.Expected}
		switch issue.Kind {
		case "missing_link", "wrong_link":
			switch issue.Field {
			case "artist":
				n.Object = e.artists[strings.TrimPrefix(issue.Target, e.sample.ID+"/artist ")]
			case "release_group_match", "release_group":
				n.Kind = "group"
			case "track":
				n.Kind = "recording"
			}
		case "missing_field", "wrong_field":
			n.Kind = issue.Field
		case "missing_suggestion":
			n.Kind = "suggestion"
		case "missing_artist_merge":
			n.Kind, n.Value = "artist_merge", issue.Peer
		default:
			continue
		}
		if !slices.Contains(needs, n) {
			needs = append(needs, n)
		}
	}
	if len(needs) == 0 {
		switch {
		case w.Release != "":
			needs = append(needs, metricNeed{e.release, "release", w.Release})
		case w.GroupMatch != "":
			needs = append(needs, metricNeed{e.release, "group", w.GroupMatch})
		default:
			needs = append(needs, metricNeed{e.release, "terminal", strings.Join(w.allowedOutcomes(), " 或 ")})
		}
	}
	return needs
}

// 只检查值与 URL 路径中的完整编号；工具参数、助手文本、错误回显都不进入证据。
func metricWalk(v any, test func(map[string]any) bool) bool {
	switch x := v.(type) {
	case map[string]any:
		if test(x) {
			return true
		}
		for _, child := range x {
			if metricWalk(child, test) {
				return true
			}
		}
	case []any:
		for _, child := range x {
			if metricWalk(child, test) {
				return true
			}
		}
	}
	return false
}

func metricValue(v any, want string) bool {
	switch x := v.(type) {
	case string:
		if x == want {
			return true
		}
		u, err := url.Parse(x)
		return err == nil && u.Hostname() == "musicbrainz.org" && strings.HasSuffix(u.Path, "/"+want)
	case float64:
		b, _ := json.Marshal(x)
		return string(b) == want
	case map[string]any:
		for _, child := range x {
			if metricValue(child, want) {
				return true
			}
		}
	case []any:
		for _, child := range x {
			if metricValue(child, want) {
				return true
			}
		}
	}
	return false
}

func metricArray(m map[string]any, key string) []any { v, _ := m[key].([]any); return v }
func metricObj(m map[string]any, key string) map[string]any {
	v, _ := m[key].(map[string]any)
	return v
}

func metricObservedDuration(local, remote map[string]any) (known, within bool) {
	seconds, _ := local["duration_seconds"].(float64)
	length, ok := remote["length_ms"].(float64)
	if !ok {
		length, _ = remote["LengthMS"].(float64)
	}
	return seconds > 0 && length > 0, math.Abs(seconds-length/1000) <= math.Max(3, length/100000)
}

// 选择档采用 sources.Release，工具详情采用 web.Metadata，时长均以毫秒表示。
func metricReleaseStructure(local []any, candidate map[string]any) bool {
	media := metricArray(candidate, "media")
	if len(media) == 0 {
		media = metricArray(candidate, "Media")
	}
	at, comparable := 0, 0
	for discIndex, row := range media {
		disc, _ := row.(map[string]any)
		for _, key := range []string{"position", "Position"} {
			if pos, ok := disc[key].(float64); ok && int(pos) != discIndex+1 {
				return false
			}
		}
		tracks := metricArray(disc, "tracks")
		if len(tracks) == 0 {
			tracks = metricArray(disc, "Tracks")
		}
		if len(tracks) == 0 {
			return false
		}
		if expected, ok := disc["TrackCount"].(float64); ok && expected > 0 && int(expected) != len(tracks) {
			return false
		}
		for i, row := range tracks {
			if at >= len(local) {
				return false
			}
			t, _ := local[at].(map[string]any)
			at++
			d, _ := t["disc"].(float64)
			pos, _ := t["position"].(float64)
			if int(d) != discIndex+1 || int(pos) != i+1 {
				return false
			}
			remote, _ := row.(map[string]any)
			known, within := metricObservedDuration(t, remote)
			if known {
				comparable++
				if !within {
					return false
				}
			}
			if remote["title"] == nil && remote["Title"] == nil {
				return false
			}
			if num, _ := remote["number"].(string); num != "" {
				if n, err := strconv.Atoi(num); err == nil && n != i+1 {
					return false
				}
			}
		}
	}
	return comparable > 0 && at == len(local)
}

func metricProof(r *metricRun, n metricNeed) metricCheck {
	c := metricCheck{Kind: n.Kind, Value: n.Value, ItemID: r.ID}
	e := metricObj(r.Item, "evidence")
	docs := append([]any{r.Item}, r.Reads...)
	if n.Value != "" {
		c.Seen = metricValue(docs, n.Value)
	}
	if !c.Seen && n.Kind == "year" {
		c.Seen = metricWalk(docs, func(obj map[string]any) bool {
			for _, key := range []string{"date", "Date"} {
				date, _ := obj[key].(string)
				if len(date) >= 4 && date[:4] == n.Value {
					return true
				}
			}
			return false
		})
	}
	if !c.Seen {
		return c
	}
	proof := metricObj(r.Item, "verification")
	candidate := metricObj(proof, "candidate")
	if n.Kind == "suggestion" {
		c.Supported = true
		return c
	}
	if r.Kind == "verify_name" && candidate["after"] == n.Value && candidate["field"] == n.Kind && candidate["structure"] == true && metricObj(proof, "name_match") != nil {
		c.Supported = true
		return c
	}
	if r.Kind == "verify_artist_merge" && n.Kind == "artist_merge" {
		c.Supported = proof["method"] == "external_identity" || metricObj(proof, "library_support") != nil || metricObj(proof, "citation") != nil
		return c
	}
	// 同一领取的首条消息可以单列 local；read_pending_items 也能返回本项的完整本地资料。
	localEvidence := e
	if provided := metricObj(r.Item, "local"); provided != nil {
		localEvidence = provided
	}
	for _, read := range r.Reads {
		metricWalk(read, func(row map[string]any) bool {
			if row["item_id"] != r.ID {
				return false
			}
			provided := metricObj(row, "evidence")
			if provided["authenticity"] != nil && len(metricArray(provided, "tracks")) > 0 {
				localEvidence = provided
				return true
			}
			return false
		})
	}
	local := metricArray(localEvidence, "tracks")
	localStructure := len(local) > 0 && localEvidence["authenticity"] != nil
	for _, row := range local {
		track, _ := row.(map[string]any)
		if track["disc"] == nil || track["position"] == nil || track["duration_seconds"] == nil {
			localStructure = false
		}
	}
	c.Supported = metricWalk(docs, func(m map[string]any) bool {
		// 候选内嵌的来源结构和读详情工具采用两种公开投影。
		id, _ := m["mbid"].(string)
		if id == "" {
			id, _ = m["ID"].(string)
		}
		switch n.Kind {
		case "artist":
			if id != n.Value || r.Kind != "artist_identity" {
				return false
			}
			names := metricArray(e, "names")
			for _, name := range names {
				text, _ := name.(string)
				if text != "" && (m["name"] == text || m["Name"] == text || metricValue(m["aliases"], text)) {
					return true
				}
			}
			return false
		case "group":
			// 组编号能从某版摘要看见；版本列表与可核对结构也必须在同一次领取中。
			if !localStructure || id != n.Value {
				return false
			}
			return slices.ContainsFunc(metricArray(m, "releases"), func(v any) bool {
				member, _ := v.(map[string]any)
				if metricReleaseStructure(local, member) {
					return true
				}
				version, _ := member["mbid"].(string)
				return version != "" && metricWalk(docs, func(obj map[string]any) bool {
					return (obj["mbid"] == version || obj["ID"] == version) && metricReleaseStructure(local, obj)
				})
			})
		case "recording":
			if !localStructure || (id != n.Value && m["RecordingID"] != n.Value && metricObj(m, "recording")["mbid"] != n.Value) {
				return false
			}
			return slices.ContainsFunc(local, func(v any) bool {
				track, _ := v.(map[string]any)
				known, within := metricObservedDuration(track, m)
				title := track["title"]
				return known && within && title != nil && (title == m["title"] || title == m["Title"] || title == m["RecordingTitle"])
			})
		}
		if !localStructure || id == "" {
			return false
		}
		if !metricReleaseStructure(local, m) {
			return false
		}
		if n.Kind == "release" {
			return id == n.Value
		}
		switch n.Kind {
		case "label", "catalog_number":
			return metricValue(m["Labels"], n.Value) || metricValue(m["labels"], n.Value)
		case "barcode":
			return m["Barcode"] == n.Value || m["barcode"] == n.Value
		case "year":
			for _, key := range []string{"Date", "date"} {
				date, _ := m[key].(string)
				if len(date) >= 4 && date[:4] == n.Value {
					return true
				}
			}
		case "title":
			return m["Title"] == n.Value || m["title"] == n.Value
		case "album_artist", "artist_credit":
			return metricValue(m["Artists"], n.Value) || metricValue(m["artists"], n.Value)
		}
		return false
	})
	return c
}

func metricAttribute(needs []metricNeed, history metricHistory, object string) *metricAttribution {
	a := &metricAttribution{Category: "model_judgment"}
	if len(needs) == 0 {
		a.Category = "info_missing"
	}
	for _, n := range needs {
		best := metricCheck{Kind: n.Kind, Value: n.Value}
		for _, r := range history[n.Object] {
			check := metricProof(r, n)
			if best.ItemID == "" || check.Seen || check.Supported {
				best = check
			}
			if check.Supported {
				break
			}
		}
		if !best.Supported {
			a.Category = "info_missing"
		}
		a.Checks = append(a.Checks, best)
	}
	objects := map[string]bool{object: true}
	for _, n := range needs {
		objects[n.Object] = true
	}
	for id := range maps.Keys(objects) {
		for _, r := range history[id] {
			if r.final && r.Unavailable && (r.Status == "waiting_route" || r.Status == "waiting_model") {
				a.Category = "source_unavailable"
			}
		}
	}
	return a
}
