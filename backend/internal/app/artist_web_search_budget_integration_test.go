package app

import (
	"net/http"
	"regexp"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPostgreSQLArtistWebSearchSharedBudget(t *testing.T) {
	f := newControlledArtistWebSearch(t, nil)
	f.addNamedRelease(t, "共享日额度艺人")
	f.addNamedRelease(t, "次日继续的艺人")
	publishSearch(t, f.application)
	artist := f.artistID(t, "共享日额度艺人")
	first := curationRulePending(t, f.schedulerFixture, "消耗共享日额度")
	settings := curationBase + "/settings"
	f.update(t, http.MethodPut, settings, map[string]any{"enabled": true, "daily_token_limit": 20, "model_concurrency": 1})
	normal := f.next(t, false)
	requireCurationRuleItems(t, normal, first.ID)
	normal.undetermined(t, first.ID, "没有可选的识别结果")
	schedulerWait(t, "整理实际用量到达日限", func() bool {
		p := f.progress(t)
		return p.State == "limit_reached" && p.Running == 0
	})

	item := f.startArtist(t, artist)
	if q := f.queue(t, http.MethodGet); q.RetryAt == nil || !q.RetryAt.Equal(curation.NextBudgetDay(f.clock.options().Now())) {
		t.Errorf("用完后队列缺少恢复日界：%+v", q)
	}
	f.noModelRequest(t)
	if got := f.item(t, item.ID); got.ConversationID != "" || got.Status != "waiting_model" {
		t.Fatalf("整理用完后仍为艺人建会话：%+v", got)
	}

	f.update(t, http.MethodPut, settings, map[string]int{"daily_token_limit": 40})
	request := f.next(t, false)
	requireCurationRuleItems(t, request, item.ID)
	second := curationRulePending(t, f.schedulerFixture, "艺人请求后继续等待的整理项")
	other := f.startArtist(t, f.artistID(t, "次日继续的艺人"))
	request.inputTokens = 24
	request.tool(t, "web_search", map[string]string{"q": "共享日额度艺人"})
	schedulerWait(t, "最后一次实际用量越过日限后停止", func() bool {
		p := f.progress(t)
		return p.State == "limit_reached" && p.Running == 0
	})
	if got := f.item(t, item.ID); got.ItemTokens != 31 || got.ItemToolCalls != 1 {
		t.Errorf("越过日限的艺人用量 %+v，期望完整记账 31 token、1 次", got)
	}
	p := f.progress(t)
	if p.Today.InputTokens != 37 || p.Today.OutputTokens != 14 {
		t.Errorf("共享日账 %+v，期望 37+14", p.Today)
	}
	f.noModelRequest(t)
	if got := f.item(t, second.ID); got.ConversationID != "" || got.Status != "waiting_model" {
		t.Errorf("艺人用完日额度后整理又领取了项：%+v", got)
	}
	if got := f.item(t, other.ID); got.ConversationID != "" || got.Status != "waiting_model" {
		t.Errorf("艺人用完日额度后又领取了艺人：%+v", got)
	}

	f.update(t, http.MethodPut, settings, map[string]bool{"enabled": false})
	f.clock.now.Add(int64(24 * time.Hour))
	request = f.next(t, false)
	// 昨日被截停的项可能排在其他等待项前，逐个完成并确认都可接续。
	var resumed []string
	for range 2 {
		for _, id := range []string{item.ID, other.ID} {
			if got := f.item(t, id); got.Status == "in_progress" {
				requireCurationRuleItems(t, request, id)
				request.tool(t, "shelve_pending_item", map[string]string{"item_id": id, "reason": "次日查完"})
				f.waitArtistResolution(t, id, "no_result")
				resumed = append(resumed, id)
				break
			}
		}
		if len(resumed) == 1 {
			request = f.next(t, false)
		}
	}
	if len(resumed) != 2 || resumed[0] == resumed[1] {
		t.Fatalf("跨日接续项 %v，期望两个不同的昨日项", resumed)
	}
}

func TestPostgreSQLArtistWebSearchItemCaps(t *testing.T) {
	for _, tc := range []struct {
		name   string
		input  int
		calls  int
		tokens int64
	}{
		{name: "tool_calls", input: 13, calls: 11, tokens: 20},
		{name: "token_exact", input: 49993, calls: 1, tokens: 50000},
		{name: "token_overshoot", input: 50003, calls: 1, tokens: 50010},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newControlledArtistWebSearch(t, nil)
			f.update(t, http.MethodPut, curationBase+"/settings", map[string]int{"model_concurrency": 1})
			f.addNamedRelease(t, "先达到单项上限的艺人")
			f.addNamedRelease(t, "后续艺人仍能查")
			publishSearch(t, f.application)
			artist := f.artistID(t, "先达到单项上限的艺人")
			item := f.startArtist(t, artist)
			request := f.next(t, false)
			other := f.startArtist(t, f.artistID(t, "后续艺人仍能查"))
			calls := make([]any, 0, tc.calls)
			for i := range tc.calls {
				calls = append(calls, map[string]any{
					"index": i, "id": uuid.New(), "type": "function",
					"function": map[string]string{"name": "web_search", "arguments": `{"q":"单项上限测试"}`},
				})
			}
			request.inputTokens = tc.input
			request.reply <- request.frame(t, map[string]any{"tool_calls": calls}, "tool_calls")
			next := f.next(t, false)
			requireCurationRuleItems(t, next, other.ID)
			status := f.status(t, artist, "shelved", 0)
			if status.Usage.ToolCalls != min(tc.calls, 10) || status.Usage.Tokens != tc.tokens ||
				status.Usage.ToolCallLimit != 10 || status.Usage.TokenLimit != 50000 {
				t.Errorf("单项用量 %+v，期望 %d 次和 %d token", status.Usage, min(tc.calls, 10), tc.tokens)
			}
			b3RequireCount(t, f.application, min(tc.calls, 10), `SELECT count(*) FROM steward_tool_calls c JOIN steward_messages m ON m.id=c.message_id WHERE m.conversation_id=$1 AND c.tool_name='web_search' AND c.status='succeeded'`, f.item(t, item.ID).ConversationID)
			p := f.progress(t)
			if p.Today.InputTokens != int64(tc.input) || p.Today.OutputTokens != 7 {
				t.Errorf("日账 %+v，期望完整记账 %d+7", p.Today, tc.input)
			}
			next.tool(t, "shelve_pending_item", map[string]string{"item_id": other.ID, "reason": "后续艺人已查完"})
			f.waitArtistResolution(t, other.ID, "no_result")
		})
	}
}

func TestPostgreSQLArtistWebSearchMissingUsage(t *testing.T) {
	f := newControlledArtistWebSearch(t, nil)
	artist := f.addArtist(t, "缺少模型用量报告")
	// 先积累实耗，第一轮缺报仍能发一次，重试必须调低输出上限。
	item := f.startArtist(t, artist)
	request := f.next(t, false)
	request.inputTokens = 28993
	request.tool(t, "web_search", map[string]string{"q": "缺少模型用量报告"})
	first := f.next(t, false)
	first.fail <- schedulerHTTPFailure{status: http.StatusServiceUnavailable, retryAfter: "0"}
	retry := f.next(t, false)
	wantAfterFirst := 29000 + artistWebWireInput(first.wire) + int64(first.outputLimit())
	if got := f.item(t, item.ID); got.ItemTokens != wantAfterFirst {
		t.Errorf("首个缺报请求未按完整估算计次：用量 %d，期望 %d", got.ItemTokens, wantAfterFirst)
	}
	wantAfterRetry := min(int64(50000), wantAfterFirst+artistWebWireInput(first.wire)+int64(first.outputLimit()))
	wantOutput := wantAfterRetry - wantAfterFirst - artistWebWireInput(first.wire)
	if retry.outputLimit() != int(wantOutput) || retry.outputLimit() >= first.outputLimit() {
		t.Errorf("重试输出上限 %d，初次 %d，期望受限到 %d", retry.outputLimit(), first.outputLimit(), wantOutput)
	}
	retry.fail <- schedulerHTTPFailure{status: http.StatusServiceUnavailable, retryAfter: "0"}
	schedulerWait(t, "重试缺报时到达单项上限", func() bool {
		return f.item(t, item.ID).Status == "shelved"
	})
	if got := f.status(t, artist, "shelved", 0).Usage; got.Tokens != 50000 || got.ToolCalls != 1 {
		t.Errorf("两次缺报后的单项用量 %+v，期望 50000 token 和 1 次工具调用", got)
	}
	f.noModelRequest(t)
	usage := f.progress(t).Today
	if usage.InputTokens != 28993 || usage.OutputTokens != 7 {
		t.Errorf("缺报的请求不应写入共享日账：%+v", usage)
	}
}

// 按协议实际收到的 JSON 逐段计算输入开销，包含提示、工具 schema 和上下文。
func artistWebWireInput(raw []byte) int64 {
	var total int64
	for _, part := range regexp.MustCompile(`[A-Za-z0-9]+|(?s:.)`).FindAll(raw, -1) {
		if len(part) > 1 && part[0] >= 0x80 {
			total += int64((len(part) + 1) / 2)
		} else {
			total += int64((len(part) + 3) / 4)
		}
	}
	return total
}

func TestPostgreSQLArtistWebSearchCandidateAtTokenCap(t *testing.T) {
	f := newControlledArtistWebSearch(t, nil)
	artist := f.addArtist(t, webSearchArtistName)
	item := f.startArtist(t, artist)
	f.next(t, false).tool(t, "web_search", map[string]string{"q": webSearchArtistName})
	request := f.next(t, false)
	request.inputTokens = 49973
	request.tool(t, "resolve_artist_identity", webSearchIdentityProposal(item.ID, "musicbrainz", artistMediaMBID, f.endpoint+"/searxng/artists/coast"))
	f.waitArtistResolution(t, item.ID, "suggestion_recorded")
	status := f.status(t, artist, "waiting_human", 1)
	if status.Usage.Tokens != 50000 || status.Candidates[0].ExternalID != artistMediaMBID {
		t.Errorf("到上限时的候选和用量 %+v", status)
	}
	f.noModelRequest(t)
}
