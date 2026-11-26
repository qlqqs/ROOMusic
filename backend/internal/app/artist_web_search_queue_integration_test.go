package app

import (
	"encoding/json"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/curation/model"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

const artistWebQueuePath = curationBase + "/artist-web-search"

func (f *artistWebSearchFixture) queue(t *testing.T, method string) curation.ArtistWebQueueStatus {
	t.Helper()
	r := f.application.request(t, method, artistWebQueuePath, nil, &f.admin, nil)
	want := http.StatusOK
	if method == http.MethodPost {
		want = http.StatusAccepted
	}
	if r.Code != want {
		t.Fatalf("队列 %s HTTP %d %s", method, r.Code, r.Body)
	}
	var q curation.ArtistWebQueueStatus
	requireCurationOK(t, json.Unmarshal(r.Body.Bytes(), &q))
	return q
}

func (f *artistWebSearchFixture) noModelRequest(t *testing.T) {
	t.Helper()
	select {
	case q := <-f.requests:
		t.Fatalf("收到不应发出的模型请求：%s", curationRulePrompt(t, q))
	case <-time.After(250 * time.Millisecond):
	}
}

func (f *artistWebSearchFixture) addNamedRelease(t *testing.T, artist string) string {
	t.Helper()
	release, _ := insertGroupedRelease(t, f.application, f.rootID, groupedRelease{
		title: "合成专辑 " + artist, artist: artist, tracks: []string{"present"},
	})
	track, _ := externalTrack(t, f.application, release)
	return track
}

func TestPostgreSQLArtistWebSearchAutomaticIsolation(t *testing.T) {
	f := newArtistWebSearchFixture(t, "identity", true, true, nil)
	f.configureMock(t)
	f.addNamedRelease(t, "应保持未查找的艺人")
	f.addNamedRelease(t, webSearchArtistName)
	f.addNamedRelease(t, "历史等待艺人")
	publishSearch(t, f.application)
	legacy := f.artistID(t, webSearchArtistName)
	item := f.pendingArtist(t, legacy, webSearchArtistName)
	// 有会话的历史候选和仍在等待的历史项分别验证。
	conversation := modelConversation(t, f.curationFixture)
	mustExec(t, f.application, `UPDATE curation_pending_items SET status='in_progress',conversation_id=$2,claimed_at=now() WHERE id=$1`, item.ID, conversation)
	input, err := json.Marshal(webSearchIdentityProposal(item.ID, "musicbrainz", artistMediaMBID, "https://mock.example/artist"))
	requireCurationOK(t, err)
	id, err := f.writer.RecordSuggestion(t.Context(), curation.ModelException{
		PendingItemID: item.ID, Tool: "resolve_artist_identity", Input: input, ConversationID: &conversation,
		Preview: json.RawMessage(`{"changes":[{"after":{"name":"纸屿"}}]}`), Provider: "mock-model",
	})
	requireCurationOK(t, err)
	waiting := f.pendingArtist(t, f.artistID(t, "历史等待艺人"), "历史等待艺人")
	f.enable(t)
	f.clock.advance(t, time.Minute)
	f.clock.advance(t, time.Minute)
	if got := f.status(t, legacy, "waiting_human", 1).Candidates[0].ExceptionID; got != id {
		t.Fatalf("历史候选 %s，期望 %s", got, id)
	}
	ctx := curation.WithArtistWebActor(t.Context(), f.admin.id)
	ready, err := f.writer.PeekModelKind(ctx, f.clock.options().Now(), 10, string(model.ArtistWeb), []string{})
	requireCurationOK(t, err)
	if len(ready) != 0 || f.item(t, waiting.ID).ConversationID != "" {
		t.Fatalf("新调度器领取了历史项：%+v", ready)
	}
	b3RequireCount(t, f.application, 2, `SELECT count(*) FROM curation_pending_items WHERE kind='artist_identity'`)
	if calls := f.mockCalls(t, "searxng"); len(calls) != 0 {
		t.Fatalf("未手动启动时有网页出站请求：%+v", calls)
	}
}

func TestPostgreSQLArtistWebSearchOrderedQueue(t *testing.T) {
	f := newControlledArtistWebSearch(t, nil)
	f.update(t, http.MethodPut, curationBase+"/settings", map[string]int{"model_concurrency": 1})
	app := f.application
	for _, name := range []string{"普通专辑甲", "普通专辑乙", "已有对应的艺人", "已合并的艺人", "已查完且没有变化"} {
		f.addNamedRelease(t, name)
	}
	compilation, _ := insertGroupedRelease(t, app, f.rootID, groupedRelease{
		title: "合成合辑", tracks: []string{"present", "present", "present"},
	})
	mustExec(t, app, `UPDATE tracks t SET artist=(ARRAY['合辑甲','合辑乙','合辑丙'])[t.position] FROM media m WHERE m.id=t.medium_id AND m.release_id=$1`, compilation)
	publishSearch(t, app)
	albumArtists := []string{f.artistID(t, "普通专辑甲"), f.artistID(t, "普通专辑乙")}
	trackArtists := []string{f.artistID(t, "合辑甲"), f.artistID(t, "合辑乙"), f.artistID(t, "合辑丙")}
	slices.Sort(albumArtists)
	slices.Sort(trackArtists)
	linked := f.artistID(t, "已有对应的艺人")
	linkArtistMedia(t, app, linked, artistMediaMBID)
	merged := f.artistID(t, "已合并的艺人")
	merge := &artistMergeFixture{programFixture: &programFixture{curationFixture: f.curationFixture}}
	merge.manualMerge(t, linked, merged)
	unchanged := f.artistID(t, "已查完且没有变化")
	last := f.startArtist(t, unchanged)
	f.next(t, false).tool(t, "shelve_pending_item", map[string]string{"item_id": last.ID, "reason": "没有身份候选"})
	f.waitArtistResolution(t, last.ID, "no_result")

	start := f.queue(t, http.MethodPost)
	if start.Total != 5 {
		t.Errorf("全量入队 %+v，期望共五位", start)
	}
	q := f.next(t, false)
	repeat := f.queue(t, http.MethodPost)
	if repeat.Generation != start.Generation || repeat.Total != 5 {
		t.Errorf("重复启动改变队列：开始 %+v，重复 %+v", start, repeat)
	}
	want := append(albumArtists, trackArtists...)
	for i, artist := range want {
		item := f.artistItem(t, artist)
		requireCurationRuleItems(t, q, item.ID)
		status := f.status(t, artist, "running", 0)
		if status.Queue == nil || status.Queue.Position != i+1 || status.Queue.Total != 5 {
			t.Errorf("领取第 %d 位时队列位置 %+v", i+1, status.Queue)
		}
		q.tool(t, "shelve_pending_item", map[string]string{"item_id": item.ID, "reason": "没有身份候选"})
		f.waitArtistResolution(t, item.ID, "no_result")
		if i+1 < len(want) {
			q = f.next(t, false)
		}
	}
	f.noModelRequest(t)
	b3RequireCount(t, app, 0, `SELECT count(*) FROM curation_pending_items WHERE artist_id IN ($1,$2)`, linked, merged)
	b3RequireCount(t, app, 1, `SELECT count(*) FROM curation_pending_items WHERE artist_id=$1`, unchanged)
}

func TestPostgreSQLArtistWebSearchStopResume(t *testing.T) {
	f := newControlledArtistWebSearch(t, nil)
	f.update(t, http.MethodPut, curationBase+"/settings", map[string]int{"model_concurrency": 1})
	f.addNamedRelease(t, "先查的艺人")
	track := f.addNamedRelease(t, "后查的艺人")
	publishSearch(t, f.application)
	second := f.artistID(t, "后查的艺人")
	item := f.startArtist(t, f.artistID(t, "先查的艺人"))
	request := f.next(t, false)
	requireCurationRuleItems(t, request, item.ID)
	started := f.queue(t, http.MethodPost)
	queued := f.artistItem(t, second)
	if started.Queued != 1 || started.Running != 1 {
		t.Fatalf("停止前队列 %+v，期望一位在途、一位等待", started)
	}

	var commands sync.WaitGroup
	for _, method := range []string{http.MethodPost, http.MethodDelete, http.MethodPost, http.MethodDelete} {
		commands.Go(func() {
			r := f.application.request(t, method, artistWebQueuePath, nil, &f.admin, nil)
			if r.Code != http.StatusOK && r.Code != http.StatusAccepted {
				t.Errorf("并发队列 %s 返回 HTTP %d %s", method, r.Code, r.Body)
			}
		})
	}
	commands.Wait()
	stopped := f.queue(t, http.MethodDelete)
	if stopped.Generation != started.Generation || stopped.Total != 2 || stopped.State != "stopped" {
		t.Fatalf("并发启停后 %+v，开始时 %+v", stopped, started)
	}
	request.tool(t, "shelve_pending_item", map[string]string{"item_id": item.ID, "reason": "完成已出站的请求"})
	f.waitArtistResolution(t, item.ID, "no_result")
	if got := f.progress(t).Today; got.InputTokens != 13 || got.OutputTokens != 7 {
		t.Errorf("停止后的在途用量 %+v，期望 13+7", got)
	}
	f.noModelRequest(t)
	if got := f.item(t, queued.ID); got.Status != "waiting_model" || got.ConversationID != "" {
		t.Fatalf("等待项没有保留 %+v", got)
	}

	resumed := f.queue(t, http.MethodPost)
	if resumed.Generation != started.Generation || resumed.Total != 2 {
		t.Errorf("接续生成新队列：开始 %+v，接续 %+v", started, resumed)
	}
	request = f.next(t, false)
	requireCurationRuleItems(t, request, queued.ID)
	request.tool(t, "shelve_pending_item", map[string]string{"item_id": queued.ID, "reason": "第一轮没有候选"})
	f.waitArtistResolution(t, queued.ID, "no_result")
	mustExec(t, f.application, `UPDATE tracks SET title='本地曲目更新' WHERE id=$1`, track)
	next := f.startArtist(t, second)
	if next.InputDigest == queued.InputDigest || next.ItemTokens != 0 || next.ItemToolCalls != 0 {
		t.Errorf("输入变化时没有重新计次：原项 %+v，新项 %+v", queued, next)
	}
	f.next(t, false).tool(t, "shelve_pending_item", map[string]string{"item_id": next.ID, "reason": "再次检查没有候选"})
	f.waitArtistResolution(t, next.ID, "no_result")
}

func TestPostgreSQLArtistWebSearchConfiguration(t *testing.T) {
	f := newArtistWebSearchFixture(t, "", false, false, nil)
	const single = "/api/v1/artists/"
	path := single + f.addArtist(t, "配置测试艺人") + "/web-search"
	check := func(configured, canStart bool, code string) {
		t.Helper()
		var state curation.ArtistWebSearchStatus
		getJSON(t, f.application, &f.admin, path, &state)
		queue := f.queue(t, http.MethodGet)
		if state.Configured != configured || state.CanStart != canStart || queue.Configured != configured || queue.CanStart != canStart {
			t.Errorf("配置状态艺人=%+v 队列=%+v，期望 configured=%t can_start=%t", state, queue, configured, canStart)
		}
		if code != "" {
			for _, endpoint := range []string{path, artistWebQueuePath} {
				requireAPIError(t, f.application.request(t, http.MethodPost, endpoint, nil, &f.admin, nil), 409, code)
			}
		}
	}
	f.stewardSettings(t, &f.admin, map[string]any{"search_source": "searxng", "searxng_url": f.endpoint + "/searxng"})
	check(false, false, "model_not_configured")
	f.stewardSettings(t, &f.admin, map[string]any{
		"endpoint": f.endpoint + "/openai/v1", "protocol": "responses", "model": "mock-model", "search_source": "none",
	})
	check(false, false, "search_unconfigured")
	f.configureMock(t)
	f.update(t, http.MethodPut, curationBase+"/sources/web_search", map[string]bool{"enabled": false})
	check(true, false, "source_disabled")
	f.update(t, http.MethodPut, curationBase+"/sources/web_search", map[string]bool{"enabled": true})
	check(true, true, "")
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		requireAPIError(t, f.application.request(t, method, artistWebQueuePath, nil, &f.ordinary, nil), 403, "forbidden")
	}
	requireAPIError(t, f.application.request(t, http.MethodGet, single+uuid.New()+"/web-search", nil, &f.ordinary, nil), 403, "forbidden")
	if q := f.queue(t, http.MethodPost); q.State != "running" || q.Total != 1 {
		t.Errorf("整理关闭时独立队列未启动：%+v", q)
	}
}
