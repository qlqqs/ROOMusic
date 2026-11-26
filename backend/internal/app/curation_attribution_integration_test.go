package app

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/provider"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
)

func TestPostgreSQLCurationAttributionEvidence(t *testing.T) {
	f := newCurationFixture(t)
	store := runtime.NewStore(f.application.database)
	conversation, err := store.CreateCurationConversation(t.Context(), f.admin.id, "霜桥回声归因", "合成模型")
	requireCurationOK(t, err)

	type row = map[string]any
	const title, artist = "霜桥回声", "沙盏三重奏"
	expected, other := uuid.New(), uuid.New()
	localRows, sourceRows := []row{}, []row{}
	for i, name := range []string{"蓝砂阶", "悬舟藤", "弯叶径", "苍羽窗", "棕帆田"} {
		seconds := 121 + i
		localRows = append(localRows, row{"id": uuid.New(), "title": name, "artist": artist, "disc": 1, "position": i + 1, "duration_seconds": seconds, "authenticity": row{"title": "real", "artist": "real"}})
		sourceRows = append(sourceRows, row{"title": name, "number": strconv.Itoa(i + 1), "length_ms": seconds * 1000, "mbid": uuid.New(), "recording": row{"mbid": uuid.New(), "title": name}, "artists": []row{{"name": artist}}})
	}
	evidence := row{"title": title, "artist": artist, "tracks": localRows, "authenticity": row{"title": "real", "artist": "real"}, "disc_count": 1, "track_counts": row{"1": len(localRows)}}
	details := func(id string) row {
		return row{"metadata": row{"mbid": id, "title": title, "artists": []row{{"name": artist}}, "media": []row{{"position": 1, "tracks": sourceRows}}}}
	}
	encode := func(v any) json.RawMessage {
		t.Helper()
		raw, err := json.Marshal(v)
		requireCurationOK(t, err)
		return raw
	}
	appendMessage := func(role provider.Role, block provider.ContentBlock, tool string) {
		t.Helper()
		content := row{"role": role, "content": []provider.ContentBlock{block}}
		if tool != "" {
			content["source"] = row{"name": tool, "untrusted": true}
		}
		_, err := store.AppendMessage(t.Context(), f.admin.id, conversation.ID, runtime.MessageInput{Role: string(role), Content: encode(content)})
		requireCurationOK(t, err)
	}
	claimMessage := func(item row) {
		t.Helper()
		data := encode([]row{item})
		appendMessage(provider.RoleUser, provider.ContentBlock{Type: provider.BlockText, Text: `<roomusic-source type="curation_pending" trust="untrusted">` + string(data) + "</roomusic-source>"}, "")
	}
	claim := func(item, release string, local row) {
		t.Helper()
		claimMessage(row{"item_id": item, "release_id": release, "kind": "release_match", "evidence": local})
	}
	read := func(output row, failed bool) {
		t.Helper()
		id := uuid.New()
		appendMessage(provider.RoleAssistant, provider.ContentBlock{Type: provider.BlockToolCall, ToolCall: &provider.ToolCall{ID: id, Name: "get_music_metadata", Arguments: encode(row{"entity": "release", "mbid": expected})}}, "")
		appendMessage(provider.RoleTool, provider.ContentBlock{Type: provider.BlockToolResult, ToolResult: &provider.ToolResult{CallID: id, IsError: failed, Content: string(encode(output))}}, "get_music_metadata")
	}
	newItem := func(release string) string {
		t.Helper()
		id := uuid.New()
		mustExec(t, f.application, `INSERT INTO curation_pending_items(id,target_type,target_id,kind,status,route,rule_version,input_digest,evidence)
			VALUES($1,'release',$2,'release_match','waiting_route','lookup',1,'霜桥样本',$3)`, id, release, encode(evidence))
		return id
	}
	check := func(release, category string, seen, supported bool) {
		t.Helper()
		history := metricHistories(t, f.application)
		result := metricAttribute([]metricNeed{{Object: release, Kind: "release", Value: expected}}, history, release)
		if result.Category != category || len(result.Checks) != 1 || result.Checks[0].Seen != seen || result.Checks[0].Supported != supported {
			t.Errorf("归因期望 %s（可见 %t、佐证 %t），实际 %+v", category, seen, supported, result)
		}
	}

	a, b := newItem(f.a), newItem(f.b)
	claimMessage(row{"item_id": a, "release_id": f.a, "kind": "release_match", "local": evidence})
	read(details(expected), false)
	check(f.a, "model_judgment", true, true)
	claim(a, f.a, evidence)
	read(details(expected), false)
	check(f.a, "model_judgment", true, true)

	// 同一会话的下一项看不到上一项的成功结果；助手复述、工具参数和失败回显都不能补足证据。
	claim(b, f.b, evidence)
	appendMessage(provider.RoleAssistant, provider.ContentBlock{Type: provider.BlockText, Text: string(encode(details(expected)))}, "")
	failure := details(expected)
	failure["code"] = "invalid_input"
	read(failure, true)
	check(f.b, "info_missing", false, false)

	// 两次领取分别缺本地结构和正确的外部编号，不能将它们合成一次完整核对。
	claim(b, f.b, row{"title": title})
	read(details(expected), false)
	claim(b, f.b, evidence)
	read(details(other), false)
	check(f.b, "info_missing", true, false)

	// 所有曲名、碟位与编号都吻合，唯一的时长反证仍须阻止据此指责模型判断错误。
	claim(b, f.b, evidence)
	sourceRows[4]["length_ms"] = 255000
	read(details(expected), false)
	check(f.b, "info_missing", true, false)

	claim(b, f.b, evidence)
	read(row{"code": "source_unavailable", "message": "合成来源持续返回 HTTP 503"}, true)
	check(f.b, "source_unavailable", true, false)
	h := metricHistories(t, f.application)[f.b]
	if h[len(h)-1].closedUnavailable() {
		t.Fatal("仍在原路线排队的项被算成结案")
	}
	mustExec(t, f.application, `UPDATE curation_pending_items SET status='resolved',resolution='unlisted',resolved_at=now() WHERE id=$1`, b)
	h = metricHistories(t, f.application)[f.b]
	if !h[len(h)-1].closedUnavailable() {
		t.Fatal("缺少本轮来源 503 后的错误结案")
	}
}
