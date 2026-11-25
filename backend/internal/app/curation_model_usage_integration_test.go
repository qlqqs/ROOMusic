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

	item := f.pending(t, f.a)
	const highInput = 120001
	const limit = highInput + 13 + 13 + 7*3
	f.update(t, "PUT", curationBase+"/settings", map[string]any{"enabled": true, "daily_token_limit": limit})
	first := f.next(t, false)
	conversation := f.item(t, item.ID).ConversationID
	second := f.pending(t, f.b)
	// 为本次运行准备可压缩的历史；六个保留回合之外还有首个用户回合。
	store := runtime.NewStore(f.application.database)
	detail, err := store.GetConversation(t.Context(), f.admin.id, conversation)
	requireCurationOK(t, err)
	for range 6 {
		_, err = store.AppendMessage(t.Context(), f.admin.id, conversation, runtime.MessageInput{Role: "user", Content: detail.Messages[0].Content})
		requireCurationOK(t, err)
	}
	first.inputTokens = highInput
	first.tool(t, "correct_release", schedulerCorrection(f.a, 1, "edition", setChange("合成版本")))
	review := f.next(t, true)
	if usage := f.progress(t).Today; usage.InputTokens != highInput || usage.OutputTokens != 7 {
		t.Fatalf("主模型用量 %+v", usage)
	}
	review.review(t, "approved")
	summary := f.next(t, false)
	if !strings.Contains(string(summary.Messages[0].Content), "对话摘要整理器") {
		t.Fatalf("未收到摘要请求")
	}
	if usage := f.progress(t).Today; usage.InputTokens != highInput+13 || usage.OutputTokens != 14 {
		t.Fatalf("审查后用量 %+v", usage)
	}
	summary.complete(t)
	// 摘要用量恰好达到上限。本项仍可继续提交，第二项等到次日。
	finish := f.next(t, false)
	if usage := f.progress(t).Today; usage.InputTokens != highInput+26 || usage.OutputTokens != 21 {
		t.Fatalf("摘要后用量 %+v", usage)
	}
	checkpoint, err := f.writer.BeforeModelItem(t.Context(), f.admin.id, f.clock.options().Now())
	requireCurationOK(t, err)
	nextDay := f.clock.options().Now().Add(24 * time.Hour)
	if checkpoint.Reason != "limit_reached" || checkpoint.RetryAt == nil || !checkpoint.RetryAt.Equal(nextDay) {
		t.Fatalf("此时开始下一项应等到次日：%+v", checkpoint)
	}
	finish.tool(t, "shelve_pending_item", map[string]string{"item_id": item.ID, "reason": "资料不足"})
	f.next(t, false).complete(t)
	schedulerWait(t, "当前项提交后达到上限", func() bool { return f.progress(t).State == "limit_reached" })
	if got := f.item(t, item.ID); got.Status != "shelved" {
		t.Fatalf("已开始的项未能提交：%+v", got)
	}
	if got := f.item(t, second.ID); got.Status != "waiting_model" || got.ConversationID != "" || got.Attempts != 0 {
		t.Fatalf("下一项应原样留在队列：%+v", got)
	}
	got := f.progress(t)
	if got.Today.InputTokens != highInput+52 || got.Today.OutputTokens != 35 || got.Today.Date != "2026-01-02" {
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
