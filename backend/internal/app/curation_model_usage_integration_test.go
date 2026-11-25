package app

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation/model"
	"github.com/qlqq/roomusic/backend/internal/platform/config"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
)

func TestPostgreSQLCurationModelDailyLimit(t *testing.T) {
	f := newSchedulerFixture(t)
	f.configure(t, &f.admin)
	chat := f.startChat(t)
	f.next(t, false).complete(t)
	finishSchedulerChat(t, chat)
	if usage := f.progress(t).Today; usage.InputTokens != 0 || usage.OutputTokens != 0 {
		t.Fatalf("普通对话被计入整理用量 %+v", usage)
	}

	release, _, item := f.matchFixture(t)
	const highInput = 120001
	const limit = highInput + 7
	f.update(t, "PUT", curationBase+"/settings", map[string]any{"enabled": true, "daily_token_limit": limit})
	first := f.next(t, false)
	second := f.pending(t, f.b)
	first.inputTokens = highInput
	first.selectCandidate(t, item.ID, f.remote.ID)
	review := f.next(t, true)
	if usage := f.progress(t).Today; usage.InputTokens != highInput || usage.OutputTokens != 7 {
		t.Fatalf("主模型用量 %+v", usage)
	}
	checkpoint, err := f.writer.BeforeModelItem(t.Context(), f.admin.id, f.clock.options().Now())
	requireCurationOK(t, err)
	nextDay := f.clock.options().Now().Add(24 * time.Hour)
	if checkpoint.Reason != "limit_reached" || checkpoint.RetryAt == nil || !checkpoint.RetryAt.Equal(nextDay) {
		t.Fatalf("达到上限后下一项应等到次日：%+v", checkpoint)
	}
	review.review(t, "approved")
	schedulerWait(t, "当前项完成后停在日限", func() bool { return f.progress(t).State == "limit_reached" })
	if got := f.item(t, item.ID); got.Resolution == nil || *got.Resolution != "model_matched" {
		t.Fatalf("达到上限后已开始的项未能写入：%+v", got)
	}
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM external_links WHERE object_type='release' AND object_id=$1`, release)
	if got := f.item(t, second.ID); got.Status != "waiting_model" || got.ConversationID != "" || got.Attempts != 0 {
		t.Fatalf("下一项应原样留在队列：%+v", got)
	}
	got := f.progress(t)
	if got.Today.InputTokens != highInput+13 || got.Today.OutputTokens != 14 || got.Today.Date != "2026-01-02" {
		t.Fatalf("当日上限及总用量 %+v", got)
	}
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM steward_conversations WHERE kind='curation'`)
	f.clock.now.Add(int64(24 * time.Hour))
	tomorrow := f.next(t, false)
	if f.item(t, second.ID).Status != "in_progress" {
		t.Fatal("次日未领取第二项")
	}
	if usage := f.progress(t).Today; usage.Date != "2026-01-03" || usage.InputTokens != 0 || usage.OutputTokens != 0 {
		t.Fatalf("次日用量 %+v", usage)
	}
	f.update(t, "PUT", curationBase+"/settings", map[string]any{"daily_token_limit": nil})
	requireCurationOK(t, f.writer.AddModelUsage(t.Context(), f.clock.options().Now(), limit, 0))
	tomorrow.complete(t)
	again := f.next(t, false)
	if p := f.progress(t); p.DailyTokenLimit != nil || p.Today.InputTokens <= limit || p.State != "running" {
		t.Fatalf("不限额度时 %+v", p)
	}
	f.pause(t)
	again.complete(t)
}

func TestPostgreSQLCurationModelUnconfiguredEnabler(t *testing.T) {
	clock := newProgramClock()
	f := newSchedulerFixture(t, WithCurationProgram(true, clock.options()))
	clock.wait(t)
	mustExec(t, f.application, `UPDATE tracks SET source_status='missing'`)
	release, _ := insertGroupedRelease(t, f.application, f.rootID, groupedRelease{title: "合成正式专辑", artist: "合成正式艺人", tracks: []string{"present", "present"}})
	track := programString(t, f.application, `SELECT t.id FROM tracks t JOIN media m ON t.medium_id=m.id WHERE m.release_id=$1 AND t.position=1`, release)
	programTag(t, f.application, track, "mb_release", f.remote.ID)
	f.enable(t)
	clock.advance(t, time.Minute)
	p := f.progress(t)
	if p.State != "unavailable" || p.UnavailableReason == nil || *p.UnavailableReason != "not_configured" {
		t.Fatalf("未配置开启者的进度 %+v", p)
	}
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM external_links WHERE object_type='release' AND object_id=$1 AND external_id=$2`, release, f.remote.ID)
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM steward_conversations WHERE kind='curation'`)
}

func TestPostgreSQLCurationModelRestartRecovery(t *testing.T) {
	f := newCurationFixture(t)
	f.enable(t)
	conversation := modelConversation(t, f)
	item := modelClaim(t, f, conversation, modelPending(t, f, f.a, "release_match", "恢复夹具"))
	mustExec(t, f.application, `UPDATE curation_pending_items SET attempts=1,review_retries=2,next_attempt_at=now()+interval '30 minutes' WHERE id=$1`, item.ID)
	plan, err := runtime.NewStore(f.application.database).CreatePlan(t.Context(), f.admin.id, conversation, nil, true)
	requireCurationOK(t, err)
	batch := f.batch(t, "model")
	f.application.shutdown()
	requireCurationOK(t, f.application.app.WaitForCurationProgram(t.Context()))
	ctx, stop := context.WithCancel(t.Context())
	cfg := config.Server{AllowedLibraryRoots: []string{f.application.allowedRoot}, DataDirectory: f.application.dataDirectory}
	app := New(ctx, cfg, f.application.database, testFrontend(), slog.New(slog.NewTextHandler(io.Discard, nil)), func(o *options) { o.pauseCatalogMaintenance = true; o.disableProgram = true }, WithCurationModel(true, model.Options{Now: newProgramClock().options().Now, PollInterval: 20 * time.Millisecond}))
	t.Cleanup(func() {
		stop()
		wait, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, join := range []func(context.Context) error{app.WaitForScanScheduler, app.WaitForScans, app.WaitForCurationProgram, app.WaitForCatalogMaintenance, app.WaitForZones, app.ShutdownSteward} {
			requireCurationOK(t, join(wait))
		}
	})
	schedulerWait(t, "启动后回收上次领取的项", func() bool {
		got, err := f.writer.PendingItem(t.Context(), item.ID)
		requireCurationOK(t, err)
		return got.Status == "waiting_model"
	})
	got, err := f.writer.PendingItem(t.Context(), item.ID)
	requireCurationOK(t, err)
	if got.Attempts != 1 || got.ClaimedAt != nil || got.ConversationID != "" || got.NextAttemptAt != nil || got.ReviewRetries != 0 {
		t.Fatalf("恢复后待定项 %+v", got)
	}
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM steward_plans WHERE id=$1 AND status='superseded'`, plan.ID)
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_batches WHERE id=$1 AND closed_at IS NOT NULL`, batch)
}

func TestPostgreSQL整理自查的摘要计入当天总额度(t *testing.T) {
	gate := &testLookupGate{}
	gate.ready.Store(true)
	f := newSchedulerFixtureWithSources(t, gate, nil)
	f.configure(t, &f.admin)
	_, _, item := f.matchFixture(t)
	item = modelRouteFixture(t, f, item, "lookup")
	const input = 120001
	const limit = input + 13 + 14
	f.update(t, "PUT", curationBase+"/settings", map[string]any{"enabled": true, "daily_token_limit": limit})
	first := f.next(t, false)
	conversation := f.item(t, item.ID).ConversationID
	store := runtime.NewStore(f.application.database)
	detail, err := store.GetConversation(t.Context(), f.admin.id, conversation)
	requireCurationOK(t, err)
	for range 7 {
		_, err = store.AppendMessage(t.Context(), f.admin.id, conversation, runtime.MessageInput{Role: "user", Content: detail.Messages[0].Content})
		requireCurationOK(t, err)
	}
	first.inputTokens = input
	first.tool(t, "get_music_metadata", map[string]any{"entity": "release", "mbid": f.remote.ID})
	summary := f.next(t, false)
	if !strings.Contains(string(summary.Messages[0].Content), "对话摘要整理器") {
		t.Fatal("未收到压缩自查历史的摘要请求")
	}
	if got := f.progress(t).Today; got.InputTokens != input || got.OutputTokens != 7 {
		t.Fatalf("摘要前用量 %+v", got)
	}
	summary.complete(t)
	submission := f.next(t, false)
	if got := f.progress(t).Today; got.InputTokens != input+13 || got.OutputTokens != 14 {
		t.Fatalf("摘要后用量 %+v", got)
	}
	checkpoint, err := f.writer.BeforeModelItem(t.Context(), f.admin.id, f.clock.options().Now())
	requireCurationOK(t, err)
	if checkpoint.Reason != "limit_reached" {
		t.Fatalf("摘要应耗完剩余的日额度：%+v", checkpoint)
	}
	submission.tool(t, "resolve_release_match", f.matchProposal(item.ID))
	f.next(t, true).review(t, "approved")
	modelWaitResolution(t, f, item.ID, "model_matched")
	if got := f.progress(t).Today; got.InputTokens != input+39 || got.OutputTokens != 28 {
		t.Fatalf("自查、摘要与审查总用量 %+v", got)
	}
}
