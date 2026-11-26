package upstreammock

import (
	"encoding/json"
	"html"
	"testing"
)

func oracleRound(t *testing.T, name, id string, args any, result string) []OracleMessage {
	t.Helper()
	params, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"id": id, "function": map[string]string{"name": name, "arguments": string(params)}}}})
	if err != nil {
		t.Fatal(err)
	}
	var call OracleMessage
	if err = json.Unmarshal(raw, &call); err != nil {
		t.Fatal(err)
	}
	return []OracleMessage{call, {Role: "tool", ToolCallID: id, Content: `<roomusic-source kind="tool" name="` + name + `">` + html.EscapeString(result) + `</roomusic-source>`}}
}

func TestOracleEvidenceScope(t *testing.T) {
	item := json.RawMessage(`{"item_id":"本项","label":"可见 & 字段"}`)
	history := oracleRound(t, "search_music_metadata", "搜索", map[string]string{"query": "仅参数可见"}, `{"code":"source_unavailable","results":[{"mbid":"错误夹带编号"}]}`)
	history = append(history, OracleMessage{Role: "assistant", Content: "模型编造编号"})
	unpaired := oracleRound(t, "get_music_metadata", "未配对", nil, `{"metadata":{"mbid":"孤立编号"}}`)
	history = append(history, unpaired[1])
	history = append(history, oracleRound(t, "get_music_metadata", "读详情", map[string]string{"entity": "release"}, `{"metadata":{"mbid":"读回的编号","title":"甲 & 乙"}}`)...)
	history = append(history, OracleMessage{Role: "user", Content: "下一项"})
	history = append(history, oracleRound(t, "get_music_metadata", "下一项查询", nil, `{"metadata":{"mbid":"其它项编号"}}`)...)
	seen := NewOracleEvidence(item, history)
	for _, value := range []string{"本项", "可见 & 字段", "读回的编号", "甲 & 乙"} {
		if !seen.Seen(value) {
			t.Errorf("丢失可见值 %q", value)
		}
	}
	for _, value := range []string{"仅参数可见", "模型编造编号", "错误夹带编号", "孤立编号", "其它项编号", "label", "甲"} {
		if seen.Seen(value) {
			t.Errorf("越界采纳 %q", value)
		}
	}
	if len(seen.Successful("search_music_metadata")) != 0 || !seen.ContainsText(" & 乙") {
		t.Fatal("错误结果或本地片段边界错误")
	}
}

func TestOracleLookupReadsBeforeSubmitting(t *testing.T) {
	const id = "53000000-0000-4000-8000-000000000035"
	item := json.RawMessage(`{"item_id":"本项","title":"可见标题"}`)
	q := OracleLookup{ItemID: "本项", Entity: "release", ExpectedID: id, Title: "可见标题", Artist: "标注中的隐名"}
	first := NewOracleEvidence(item, nil).Lookup(q)
	if first.Name != "search_music_metadata" || first.Args["query"] != `release:"可见标题"` {
		t.Fatalf("首轮查询 %+v", first)
	}
	for _, tc := range []struct{ name, result string }{
		{"查无结果", `{"results":[]}`}, {"来源错误", `{"code":"source_unavailable","results":[{"mbid":"` + id + `"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := oracleRound(t, first.Name, "检索", first.Args, tc.result)
			if action := NewOracleEvidence(item, h).Lookup(q); action.Name != "" {
				t.Fatalf("证据不足时应放弃，实际 %+v", action)
			}
		})
	}
	h := oracleRound(t, first.Name, "检索", first.Args, `{"results":[{"mbid":"`+id+`"}]}`)
	read := NewOracleEvidence(item, h).Lookup(q)
	if read.Name != "get_music_metadata" || read.Args["mbid"] != id {
		t.Fatalf("未读回候选 %+v", read)
	}
	h = append(h, oracleRound(t, read.Name, "详情", read.Args, `{"metadata":{"mbid":"`+id+`"}}`)...)
	action := NewOracleEvidence(item, h).Lookup(q)
	if action.Name != "resolve_release_match" || action.Args["external_id"] != id {
		t.Fatalf("缺少可验证的提交 %+v", action)
	}
}

func TestOracleWebOnlyUsesObservedPage(t *testing.T) {
	const id = "53000000-0000-4000-8000-000000000036"
	item := json.RawMessage(`{"item_id":"本项"}`)
	h := oracleRound(t, "web_search", "搜索", map[string]string{"q": "合成艺人"}, `{"results":[{"url":"https://example.invalid/合成页","snippet":"合成艺人"}]}`)
	read := NewOracleEvidence(item, h).Web("本项", "artist", id)
	if read.Name != "fetch_page" || read.Args["url"] != "https://example.invalid/合成页" {
		t.Fatalf("未查实际页面 %+v", read)
	}
	for _, failure := range []bool{true, false} {
		result := `{"text":"https://musicbrainz.org/artist/` + id + `"}`
		if failure {
			result = `{"code":"source_unavailable","text":"https://musicbrainz.org/artist/` + id + `"}`
		}
		history := append(h, oracleRound(t, read.Name, "读取", read.Args, result)...)
		got := NewOracleEvidence(item, history).Web("本项", "artist", id)
		if failure && got.Name != "" || !failure && (got.Name != "resolve_artist_identity" || got.Args["external_id"] != id) {
			t.Fatalf("页面结果失败=%t，调用 %+v", failure, got)
		}
	}
}

func TestOracleReadPendingOnlyUsesClaimedItem(t *testing.T) {
	item := json.RawMessage(`{"item_id":"本项"}`)
	e := NewOracleEvidence(item, nil)
	action := e.ReadPending("本项")
	if action.Name != "read_pending_items" || e.ReadPending("另一项").Name != "" {
		t.Fatalf("待定项读取越界或缺失：%+v", action)
	}
	h := oracleRound(t, action.Name, "读本项", action.Args, `[{"item_id":"隔壁项","canonical_title":"隔壁的规范名"},{"item_id":"本项","canonical_title":"霜渡薄潮"}]`)
	e = NewOracleEvidence(item, h)
	if e.Seen("隔壁的规范名") || e.ReadPending("本项").Name != "" {
		t.Fatal("读取了其它项或重复查询同一项")
	}
	next := e.Lookup(OracleLookup{ItemID: "本项", Entity: "release", ExpectedID: "53000000-0000-4000-8000-000000000041"})
	if next.Name != "search_music_metadata" || next.Args["query"] != `release:"霜渡薄潮"` {
		t.Fatalf("没有沿本项刚取得的规范名检索：%+v", next)
	}
}
