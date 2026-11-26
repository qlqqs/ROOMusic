package app

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/curation/model"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
	"github.com/qlqq/roomusic/backend/internal/upstreammock"
)

// 仅记录阶段、应用生成的项编号及 Responses 返回的数值，不保存请求、输出或请求头。
type liveResponseMeter struct {
	mu    sync.Mutex
	calls []*liveResponseCall
}

type liveResponseCall struct {
	meter         *liveResponseMeter
	stage         string
	items         []string
	status        int
	input, output *int
}

func (m *liveResponseMeter) begin(body []byte) *liveResponseCall {
	var req struct {
		Instructions string
		Tools        []struct{ Name string }
		Input        []struct {
			Role    string
			Content json.RawMessage
		}
	}
	c := &liveResponseCall{meter: m, stage: "unclassified"}
	if json.Unmarshal(body, &req) == nil {
		for _, msg := range req.Input {
			if msg.Role != "user" {
				continue
			}
			var content string
			if json.Unmarshal(msg.Content, &content) != nil {
				continue
			}
			pending, ok, err := metricsPendingItems(content)
			if !ok || err != nil || len(pending) == 0 {
				continue
			}
			c.items = nil
			c.stage = metricsPendingStage(pending[0], req.Instructions)
			for _, p := range pending {
				if uuid.Valid(p.ID) {
					c.items = append(c.items, p.ID)
				}
			}
		}
		if req.Instructions == runtime.SummarySystemPrompt {
			c.stage = "summary"
		}
		for _, tool := range req.Tools {
			switch tool.Name {
			case "submit_review":
				c.stage = "review"
			case "roomusic_probe":
				c.stage = "probe"
			}
		}
	}
	m.mu.Lock()
	m.calls = append(m.calls, c)
	m.mu.Unlock()
	return c
}

func (c *liveResponseCall) fail() { c.setStatus(0) }

func (c *liveResponseCall) setStatus(status int) {
	c.meter.mu.Lock()
	c.status = status
	c.meter.mu.Unlock()
}

func (c *liveResponseCall) observe(status int, body io.ReadCloser) io.ReadCloser {
	c.setStatus(status)
	if status < 200 || status >= 300 {
		return body
	}
	w := &liveUsageFrames{call: c}
	return struct {
		io.Reader
		io.Closer
	}{io.TeeReader(body, w), body}
}

type liveUsageFrames struct {
	call       *liveResponseCall
	line, data []byte
	skip       bool
}

// 按原字节转发流；多个 data 行组成同一个 SSE 事件。与应用保持相同的单事件上限。
func (w *liveUsageFrames) Write(p []byte) (int, error) {
	n := len(p)
	const limit = 8 << 20
	for len(p) > 0 {
		line, rest, found := bytes.Cut(p, []byte{'\n'})
		if len(w.line)+len(w.data)+len(line) > limit {
			w.skip = true
			w.line = nil
			w.data = nil
		}
		if !w.skip {
			w.line = append(w.line, line...)
		}
		if !found {
			break
		}
		if w.skip {
			if len(line) == 0 || bytes.Equal(line, []byte{'\r'}) {
				w.skip = false
			}
		} else {
			w.consumeLine(bytes.TrimSuffix(w.line, []byte{'\r'}))
		}
		w.line = w.line[:0]
		p = rest
	}
	return n, nil
}

func (w *liveUsageFrames) consumeLine(line []byte) {
	if field, ok := bytes.CutPrefix(line, []byte("data:")); ok {
		w.data = append(w.data, bytes.TrimPrefix(field, []byte(" "))...)
		w.data = append(w.data, '\n')
		return
	}
	if len(line) != 0 || len(w.data) == 0 {
		return
	}
	var event struct {
		Type     string
		Response struct {
			Usage struct {
				Input  *int `json:"input_tokens"`
				Output *int `json:"output_tokens"`
			}
		}
	}
	if json.Unmarshal(w.data, &event) == nil && (event.Type == "response.completed" || event.Type == "response.incomplete" || event.Type == "response.failed") {
		w.call.meter.mu.Lock()
		w.call.input, w.call.output = event.Response.Usage.Input, event.Response.Usage.Output
		w.call.meter.mu.Unlock()
	}
	w.data = w.data[:0]
}

// 跨档时生产代码会把单项计数归零；在隔离测试库按档保存归零前的实际峰值，含审查和摘要。
func liveStartItemUsage(t *testing.T, app *integrationTestApplication) {
	t.Helper()
	mustExec(t, app, `CREATE TABLE live_item_usage(item_id uuid,stage text,tools integer,tokens bigint,PRIMARY KEY(item_id,stage))`)
	mustExec(t, app, `CREATE FUNCTION live_capture_item_usage() RETURNS trigger LANGUAGE plpgsql AS $$
	DECLARE usage_stage text;
	BEGIN
		usage_stage := CASE WHEN NEW.kind='artist_identity' THEN 'web' ELSE NEW.route END;
		IF usage_stage IN ('lookup','web') THEN
			INSERT INTO live_item_usage VALUES(NEW.id,usage_stage,NEW.item_tool_calls,NEW.item_tokens)
			ON CONFLICT (item_id,stage) DO UPDATE SET tools=greatest(live_item_usage.tools,excluded.tools),
			tokens=greatest(live_item_usage.tokens,excluded.tokens);
		END IF;
		RETURN NEW;
	END $$`)
	mustExec(t, app, `CREATE TRIGGER live_item_usage_updates AFTER UPDATE OF item_tool_calls,item_tokens ON curation_pending_items
	FOR EACH ROW WHEN (NEW.item_tool_calls>0 OR NEW.item_tokens>0) EXECUTE FUNCTION live_capture_item_usage()`)
}

// 每批一项，以首条用户消息关联同一待定项的全部会话。主消息与审查记录分别聚合，
// 避免工具调用或审查重试使 token 重复计数。摘要用量仅由接口观察器计入全轮。
func liveAllStageItemPeaks(t *testing.T, app *integrationTestApplication) json.RawMessage {
	t.Helper()
	const query = `WITH first_item AS (
		SELECT DISTINCT ON (conversation_id) conversation_id,(content::jsonb->>'background_item_id')::uuid AS item_id
		FROM steward_messages WHERE role='user' AND content::jsonb->>'background_item_id' IS NOT NULL
		ORDER BY conversation_id,sequence
	), main AS (
		SELECT f.item_id,sum(coalesce(m.input_tokens,0)+coalesce(m.output_tokens,0)) AS tokens
		FROM first_item f JOIN steward_messages m USING(conversation_id) WHERE m.role='assistant' GROUP BY f.item_id
	), reviews AS (
		SELECT f.item_id,sum(coalesce((r.usage->>'input_tokens')::bigint,0)+coalesce((r.usage->>'output_tokens')::bigint,0)) AS tokens
		FROM first_item f JOIN steward_plans p USING(conversation_id) JOIN steward_reviews r ON r.plan_id=p.id GROUP BY f.item_id
	), calls AS (
		SELECT f.item_id,count(*) AS tool_calls FROM first_item f JOIN steward_messages m USING(conversation_id)
		JOIN steward_tool_calls c ON c.message_id=m.id GROUP BY f.item_id
	), tally AS (
		SELECT m.item_id,p.kind,m.tokens AS main_tokens,coalesce(r.tokens,0) AS review_tokens,
		       m.tokens+coalesce(r.tokens,0) AS tokens,coalesce(c.tool_calls,0) AS tool_calls
		FROM main m JOIN curation_pending_items p ON p.id=m.item_id LEFT JOIN reviews r USING(item_id) LEFT JOIN calls c USING(item_id)
	)
	SELECT json_build_object('observed_at',now(),'items',count(*),'message_and_review_tokens',coalesce(sum(tokens),0),
		'tool_calls',coalesce(sum(tool_calls),0),
		'max_tokens_item',(SELECT row_to_json(t) FROM tally t ORDER BY tokens DESC,item_id LIMIT 1),
		'max_tools_item',(SELECT row_to_json(t) FROM tally t ORDER BY tool_calls DESC,tokens DESC,item_id LIMIT 1),
		'summary_conversations',(SELECT count(*) FROM steward_conversations WHERE summary_updated_at IS NOT NULL))
	FROM tally`
	var peaks json.RawMessage
	requireCurationOK(t, app.database.QueryRowContext(t.Context(), query).Scan(&peaks))
	return peaks
}

func liveRealMetrics(t *testing.T, app *integrationTestApplication, result map[string]any, meter *liveResponseMeter) map[string][]string {
	t.Helper()
	byStage := map[string]metricsModelUsage{}
	routes := map[string][]string{}
	statuses := map[int]int{}
	missing := map[string]int{}
	var total metricsModelUsage
	meter.mu.Lock()
	for _, c := range meter.calls {
		if c.stage == "probe" {
			continue
		}
		statuses[c.status]++
		u := byStage[c.stage]
		u.Calls++
		total.Calls++
		if c.input != nil {
			u.InputTokens += *c.input
			total.InputTokens += *c.input
		}
		if c.output != nil {
			u.OutputTokens += *c.output
			total.OutputTokens += *c.output
		}
		if c.input == nil || c.output == nil {
			missing[c.stage]++
		}
		byStage[c.stage] = u
		for _, id := range c.items {
			seq := routes[id]
			if len(seq) == 0 || seq[len(seq)-1] != c.stage {
				routes[id] = append(seq, c.stage)
			}
		}
	}
	meter.mu.Unlock()

	items := map[string]map[string]metricsItemSample{}
	for id, history := range routes {
		for _, s := range []string{"lookup", "web"} {
			if !slices.Contains(history, s) {
				continue
			}
			if items[s] == nil {
				items[s] = map[string]metricsItemSample{}
			}
			items[s][id] = metricsItemSample{}
		}
	}
	metricReadRows(t, app, `SELECT item_id,stage,tools,tokens FROM live_item_usage`, func(rows *sql.Rows) {
		var id, stage string
		var sample metricsItemSample
		requireCurationOK(t, rows.Scan(&id, &stage, &sample.Tools, &sample.Tokens))
		if items[stage] == nil {
			items[stage] = map[string]metricsItemSample{}
		}
		items[stage][id] = sample
	})
	peaks := map[string]metricsStagePeak{}
	for _, stage := range []string{"lookup", "web"} {
		p := metricsStagePeak{Items: len(items[stage])}
		for _, x := range items[stage] {
			p.ToolCalls = max(p.ToolCalls, x.Tools)
			p.Tokens = max(p.Tokens, x.Tokens)
		}
		peaks[stage] = p
	}
	counts := func(query string) map[string]int {
		m := map[string]int{}
		metricReadRows(t, app, query, func(rows *sql.Rows) {
			var k string
			var n int
			requireCurationOK(t, rows.Scan(&k, &n))
			m[k] = n
		})
		return m
	}
	ops := counts(`SELECT CASE WHEN o.origin->>'review_id' IS NULL THEN 'direct/' ELSE 'reviewed/' END || o.operation_type,
		count(*) FROM operations o JOIN curation_batches b ON b.id=o.batch_id AND b.tier='model' GROUP BY 1`)
	reviews := counts(`SELECT status,count(*) FROM steward_reviews GROUP BY status`)
	decisions := counts(`SELECT COALESCE(i->>'decision','unknown'),count(*) FROM steward_reviews r
		CROSS JOIN LATERAL jsonb_array_elements(r.items) i GROUP BY 1`)
	var actualIn, actualOut int
	requireCurationOK(t, app.database.QueryRowContext(t.Context(), `SELECT COALESCE(sum(input_tokens),0),COALESCE(sum(output_tokens),0) FROM curation_model_usage`).Scan(&actualIn, &actualOut))
	var direct, reviewed int
	for k, n := range ops {
		if strings.HasPrefix(k, "direct/") {
			direct += n
		} else {
			reviewed += n
		}
	}
	result["model_by_stage"], result["item_usage"], result["model_routes"] = byStage, items, routes
	result["real_model"] = map[string]any{"model_usage": total, "http_statuses": statuses, "calls_without_complete_usage": missing,
		"application_usage": map[string]int{"input_tokens": actualIn, "output_tokens": actualOut},
		"review_records":    reviews, "review_item_decisions": decisions, "direct_model_operations": direct, "reviewed_model_operations": reviewed,
		"model_operations_by_type": ops, "item_usage_peaks": peaks, "all_stage_item_peaks": liveAllStageItemPeaks(t, app)}
	return routes
}

type metricsItemSample struct {
	Tools  int `json:"tool_calls"`
	Tokens int `json:"tokens"`
}

func Test真实模型计量保留分块事件并只取末帧用量(t *testing.T) {
	const itemID = "93213c30-b89d-45cc-ad34-285a13e637a2"
	message := "<roomusic-source type=\"curation_pending\" trust=\"untrusted\">\n" +
		`[{"item_id":"` + itemID + `","kind":"release_match","evidence":{}}]` +
		"\n</roomusic-source>"
	request, err := json.Marshal(map[string]any{
		"instructions": backgroundPrompt(model.TaskKind("lookup")),
		"input":        []map[string]string{{"role": "user", "content": message}},
	})
	if err != nil {
		t.Fatal(err)
	}
	meter := new(liveResponseMeter)
	call := meter.begin(request)
	if call.stage != "lookup" || !slices.Equal(call.items, []string{itemID}) {
		t.Fatal("未能定位第二档的具体整理项")
	}
	stream := "event: response.created\r\ndata: {\"type\":\"response.created\",\"response\":{\"usage\":{\"input_tokens\":100}}}\r\n\r\n" +
		"event: response.completed\r\ndata: {\"type\":\"response.completed\",\r\ndata: \"response\":{\"usage\":{\"input_tokens\":37,\"output_tokens\":11}}}\r\n\r\n"
	reader := call.observe(200, io.NopCloser(strings.NewReader(stream)))
	defer reader.Close()
	var kept bytes.Buffer
	b := make([]byte, 3)
	for {
		n, err := reader.Read(b)
		kept.Write(b[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if kept.String() != stream || call.status != 200 || call.input == nil || *call.input != 37 || call.output == nil || *call.output != 11 {
		t.Fatal("转发流或终帧的接口用量不符")
	}
}

func TestMetricsRouteWithSharedTools(t *testing.T) {
	const id = "53000000-0000-4000-8000-000000000038"
	for _, tc := range []struct {
		name, route      string
		prompt           model.TaskKind
		stage, firstTool string
	}{
		{"联网字段优先", "web", model.TaskKind("lookup"), "web", "web_search"},
		{"第二档字段优先", "lookup", model.TaskKind("web"), "lookup", "search_music_metadata"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item := map[string]any{"item_id": id, "release_id": "专辑甲", "kind": "release_match", "evidence": map[string]string{"title": "竹笺折痕"}}
			if tc.route != "" {
				item["route"] = tc.route
			}
			raw, err := json.Marshal([]any{item})
			requireCurationOK(t, err)
			msg := `<roomusic-source type="curation_pending" trust="untrusted">` + string(raw) + "</roomusic-source>"
			system := backgroundPrompt(tc.prompt)
			both := []string{"search_music_metadata", "get_music_metadata", "web_search", "fetch_page"}
			var chatTools []map[string]any
			var responseTools []map[string]string
			for _, name := range both {
				chatTools = append(chatTools, map[string]any{"type": "function", "function": map[string]string{"name": name}})
				responseTools = append(responseTools, map[string]string{"type": "function", "name": name})
			}
			raw, err = json.Marshal(map[string]any{"tools": chatTools, "messages": []map[string]string{{"role": "system", "content": system}, {"role": "user", "content": msg}}})
			requireCurationOK(t, err)
			var q metricsChatRequest
			requireCurationOK(t, json.Unmarshal(raw, &q))
			m := metricsModel{mode: "oracle", targets: map[string]metricsModelTarget{"专辑甲": {SampleID: "合成样本"}}, routes: map[string][]string{}}
			rep, err := m.pendingReply(t.Context(), q)
			requireCurationOK(t, err)
			if rep.category != tc.stage || !slices.Equal(m.routes[id], []string{tc.stage}) || len(rep.calls) != 1 || rep.calls[0].name != tc.firstTool {
				t.Fatalf("模型档位或首个工具不符 %+v", rep)
			}
			if tc.stage == "web" {
				var after []upstreammock.OracleMessage
				requireCurationOK(t, json.Unmarshal([]byte(`[{"role":"assistant","tool_calls":[{"id":"合成查询","function":{"name":"web_search","arguments":"{}"}}]},{"role":"tool","tool_call_id":"合成查询","content":"<roomusic-source kind=\"tool\" name=\"web_search\">{\"results\":[]}</roomusic-source>"}]`), &after))
				q.Messages = append(q.Messages, after...)
				next, err := m.pendingReply(t.Context(), q)
				requireCurationOK(t, err)
				if next.category != "web" || len(next.calls) != 1 || next.calls[0].name != "search_music_metadata" {
					t.Fatalf("联网档继续读取开放库的用量应归于同档 %+v", next)
				}
			}
			raw, err = json.Marshal(map[string]any{"instructions": system, "tools": responseTools, "input": []map[string]string{{"role": "user", "content": msg}}})
			requireCurationOK(t, err)
			call := new(liveResponseMeter).begin(raw)
			if call.stage != tc.stage || !slices.Equal(call.items, []string{id}) {
				t.Fatalf("真实协议计量 %s %v，期望 %s %s", call.stage, call.items, tc.stage, id)
			}
		})
	}
}
