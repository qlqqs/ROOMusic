package upstreammock

import (
	"encoding/json"
	"fmt"
	"html"
	"slices"
	"strings"
)

// OracleMessage 是度量端点收到的 Chat Completions 消息。工具结果通过调用编号配对。
type OracleMessage struct {
	Role, Content string
	ToolCallID    string `json:"tool_call_id"`
	ToolCalls     []struct {
		ID       string
		Function struct{ Name, Arguments string }
	} `json:"tool_calls"`
}

type oracleRead struct {
	name   string
	args   struct{ Entity, MBID, Query, URL string }
	result json.RawMessage
}

// OracleEvidence 只保存本项首条消息与本轮读取工具的可见结果。
type OracleEvidence struct {
	item   json.RawMessage
	values []string
	reads  []oracleRead
}

// NewOracleEvidence 的 history 从本次领取本项之后开始，不传上一项会话。
func NewOracleEvidence(item json.RawMessage, history []OracleMessage) OracleEvidence {
	e := OracleEvidence{item: item}
	e.index(item)
	calls := map[string]int{}
	for _, msg := range history {
		switch msg.Role {
		case "user":
			// 新领取的用户消息是轮次边界。
			return e
		case "assistant":
			for _, call := range msg.ToolCalls {
				switch call.Function.Name {
				case "read_pending_items", "search_music_metadata", "get_music_metadata", "web_search", "fetch_page":
					read := oracleRead{name: call.Function.Name}
					if call.ID != "" && json.Unmarshal([]byte(call.Function.Arguments), &read.args) == nil {
						calls[call.ID] = len(e.reads)
						e.reads = append(e.reads, read)
					}
				}
			}
		case "tool":
			i, ok := calls[msg.ToolCallID]
			if !ok {
				continue
			}
			delete(calls, msg.ToolCallID)
			read := &e.reads[i]
			raw, ok := OracleToolBody(msg.Content, read.name)
			if !ok {
				continue
			}
			var status struct{ Code string }
			if !json.Valid(raw) || (json.Unmarshal(raw, &status) == nil && status.Code != "") {
				continue
			}
			if read.name == "read_pending_items" {
				var current struct {
					ID string `json:"item_id"`
				}
				var rows []json.RawMessage
				if json.Unmarshal(item, &current) != nil || json.Unmarshal(raw, &rows) != nil {
					continue
				}
				for _, row := range rows {
					var pending struct {
						ID string `json:"item_id"`
					}
					if json.Unmarshal(row, &pending) == nil && current.ID != "" && pending.ID == current.ID {
						read.result = row
						e.index(row)
					}
				}
				continue
			}
			read.result = raw
			e.index(raw)
		}
	}
	return e
}

// OracleToolBody 只拆开来自指定工具的完整来源包装。
func OracleToolBody(content, name string) (json.RawMessage, bool) {
	body, ok := strings.CutPrefix(content, `<roomusic-source kind="tool" name="`+name+`">`)
	if !ok {
		return nil, false
	}
	body, ok = strings.CutSuffix(body, "</roomusic-source>")
	if !ok {
		return nil, false
	}
	return json.RawMessage(strings.TrimSpace(html.UnescapeString(body))), true
}

func (e *OracleEvidence) index(raw json.RawMessage) {
	var v any
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.UseNumber()
	if d.Decode(&v) != nil {
		return
	}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			e.values = append(e.values, x)
		case json.Number:
			e.values = append(e.values, x.String())
		case map[string]any:
			for _, child := range x {
				walk(child)
			}
		case []any:
			for _, child := range x {
				walk(child)
			}
		}
	}
	walk(v)
}

func (e OracleEvidence) Seen(value string) bool {
	return value != "" && slices.Contains(e.values, value)
}

// ContainsText 用于从单个可见文件名、目录名、标签或网页正文取连续片段。
func (e OracleEvidence) ContainsText(value string) bool {
	return value != "" && slices.ContainsFunc(e.values, func(s string) bool { return strings.Contains(s, value) })
}

func (e OracleEvidence) Successful(name string) []json.RawMessage {
	var found []json.RawMessage
	for _, read := range e.reads {
		if read.name == name && len(read.result) > 0 {
			found = append(found, read.result)
		}
	}
	return found
}

// ReadPending 在其它可见线索耗尽时向同一项补读一次。
func (e OracleEvidence) ReadPending(itemID string) OracleAction {
	if !e.Seen(itemID) || slices.ContainsFunc(e.reads, func(r oracleRead) bool { return r.name == "read_pending_items" }) {
		return OracleAction{}
	}
	return OracleAction{"read_pending_items", map[string]any{"item_ids": []string{itemID}}}
}

type OracleLookup struct {
	ItemID, Entity, ExpectedID, ExpectedGroup       string
	Title, Artist, TrackTitle, CatalogNumber, Label string
	Barcodes                                        []string
}

type OracleAction struct {
	Name string
	Args map[string]any
}

type oracleEntity struct {
	MBID, Title  string
	ReleaseGroup struct{ MBID string } `json:"release_group"`
	Releases     []oracleEntity
}

func (q OracleLookup) matches(row oracleEntity) bool {
	return row.MBID != "" && (row.MBID == q.ExpectedID || q.ExpectedGroup != "" && row.ReleaseGroup.MBID == q.ExpectedGroup)
}

// Lookup 先按已出现的编号读详情；若信息还不够，以首条消息的标题、艺人和首曲名分别检索。
func (e OracleEvidence) Lookup(q OracleLookup) OracleAction {
	if !e.Seen(q.ItemID) {
		return OracleAction{}
	}
	id := ""
	if e.Seen(q.ExpectedID) {
		id = q.ExpectedID
	}
	for _, read := range e.reads {
		switch read.name {
		case "get_music_metadata":
			var result struct{ Metadata oracleEntity }
			if json.Unmarshal(read.result, &result) != nil || result.Metadata.MBID != read.args.MBID {
				continue
			}
			if read.args.Entity == "release-group" && q.Entity == "release" {
				for _, rel := range result.Metadata.Releases {
					if rel.MBID != "" && (rel.MBID == q.ExpectedID || result.Metadata.MBID == q.ExpectedGroup) {
						id = rel.MBID
						break
					}
				}
			}
			if read.args.Entity == q.Entity && q.matches(result.Metadata) {
				return OracleAction{"resolve_release_match", map[string]any{"item_id": q.ItemID, "external_id": result.Metadata.MBID, "basis": map[string]string{"note": "已从本项工具读回候选详情。"}}}
			}
		case "search_music_metadata":
			var result struct{ Results []oracleEntity }
			if json.Unmarshal(read.result, &result) != nil {
				continue
			}
			for _, row := range result.Results {
				if read.args.Entity == q.Entity && q.matches(row) {
					id = row.MBID
					break
				}
				if q.Entity == "release" && read.args.Entity == "recording" {
					for _, rel := range row.Releases {
						if q.matches(rel) {
							id = rel.MBID
							break
						}
					}
				}
			}
		}
	}
	if id != "" && !slices.ContainsFunc(e.reads, func(r oracleRead) bool {
		return r.name == "get_music_metadata" && r.args.Entity == q.Entity && r.args.MBID == id
	}) {
		include := []string{"artist-credits"}
		if q.Entity == "release" {
			include = append(include, "recordings", "release-groups")
		}
		return OracleAction{"get_music_metadata", map[string]any{"entity": q.Entity, "mbid": id, "include": include}}
	}
	if q.Entity == "release" && e.Seen(q.ExpectedGroup) && !slices.ContainsFunc(e.reads, func(r oracleRead) bool { return r.args.Entity == "release-group" && r.args.MBID == q.ExpectedGroup }) {
		return OracleAction{"get_music_metadata", map[string]any{"entity": "release-group", "mbid": q.ExpectedGroup, "include": []string{"releases"}}}
	}
	title := q.Title
	if q.Entity == "recording" {
		title = q.TrackTitle
	}
	type searchTerm struct{ entity, field, value string }
	terms := []searchTerm{{q.Entity, q.Entity, title}, {q.Entity, "artist", q.Artist}, {"recording", "recording", q.TrackTitle}}
	if q.Entity == "release" {
		for _, raw := range append([]json.RawMessage{e.item}, e.Successful("read_pending_items")...) {
			var row struct {
				Title           string `json:"canonical_title"`
				Artist          string `json:"canonical_artist"`
				Local, Evidence struct{ Title, Artist string }
			}
			if json.Unmarshal(raw, &row) == nil {
				terms = append(terms, searchTerm{"release", "release", row.Title}, searchTerm{"release", "artist", row.Artist},
					searchTerm{"release", "release", row.Local.Title}, searchTerm{"release", "artist", row.Local.Artist},
					searchTerm{"release", "release", row.Evidence.Title}, searchTerm{"release", "artist", row.Evidence.Artist})
			}
		}
		for _, barcode := range q.Barcodes {
			terms = append(terms, searchTerm{"release", "barcode", barcode})
		}
		terms = append(terms, searchTerm{"release", "catno", q.CatalogNumber}, searchTerm{"release", "label", q.Label})
	}
	for _, search := range terms {
		if !e.Seen(search.value) {
			continue
		}
		query := fmt.Sprintf("%s:%q", search.field, search.value)
		if slices.ContainsFunc(e.reads, func(r oracleRead) bool {
			return r.name == "search_music_metadata" && r.args.Entity == search.entity && r.args.Query == query
		}) {
			continue
		}
		return OracleAction{"search_music_metadata", map[string]any{"entity": search.entity, "query": query, "limit": 10}}
	}
	return OracleAction{}
}

// Web 读取实际搜索结果。摘要含有编号时可直接核对；否则读返回的网址，再检查正文。
func (e OracleEvidence) Web(itemID, entity, expected string) OracleAction {
	if expected == "" || !e.Seen(itemID) {
		return OracleAction{}
	}
	for _, read := range e.reads {
		if read.name != "web_search" {
			continue
		}
		var search struct {
			Results []struct{ URL, Snippet string }
		}
		if json.Unmarshal(read.result, &search) != nil {
			continue
		}
		for _, hit := range search.Results {
			if hit.URL == "" {
				continue
			}
			address := "https://musicbrainz.org/" + entity + "/" + expected
			seen := hit.URL == address || strings.Contains(hit.Snippet, address)
			fetched := false
			for _, page := range e.reads {
				if page.name != "fetch_page" || page.args.URL != hit.URL {
					continue
				}
				fetched = true
				var body struct{ Text string }
				if json.Unmarshal(page.result, &body) == nil && strings.Contains(body.Text, address) {
					seen = true
				}
			}
			if seen {
				if entity == "artist" {
					return OracleAction{"resolve_artist_identity", map[string]any{"item_id": itemID, "external_id": expected, "source": "musicbrainz", "basis": map[string]any{"note": "已在本项网页资料中核对艺人编号。", "citations": []string{hit.URL}}}}
				}
				return OracleAction{"resolve_release_match", map[string]any{"item_id": itemID, "external_id": expected, "basis": map[string]string{"note": "已在本项网页资料中核对编号。"}}}
			}
			if !fetched {
				return OracleAction{"fetch_page", map[string]any{"url": hit.URL}}
			}
		}
	}
	return OracleAction{}
}
