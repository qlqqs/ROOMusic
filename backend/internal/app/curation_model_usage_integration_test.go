package app

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
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

	_, _, item := f.matchFixture(t)
	item = modelRouteFixture(t, f, item, "")
	const highInput = 120001
	const limit = highInput + 7
	f.update(t, "PUT", curationBase+"/settings", map[string]any{"enabled": true, "daily_token_limit": limit, "model_concurrency": 1})
	first := f.next(t, false)
	second := f.pending(t, f.b)
	first.inputTokens = highInput
	first.selectCandidate(t, item.ID, f.remote.ID)
	modelWaitResolution(t, f, item.ID, "judge_submitted")
	if usage := f.progress(t).Today; usage.InputTokens != highInput || usage.OutputTokens != 7 {
		t.Fatalf("主模型用量 %+v", usage)
	}
	checkpoint, err := f.writer.BeforeModelItem(t.Context(), f.admin.id, f.clock.options().Now())
	requireCurationOK(t, err)
	nextDay := f.clock.options().Now().Add(24 * time.Hour)
	if checkpoint.Reason != "limit_reached" || checkpoint.RetryAt == nil || !checkpoint.RetryAt.Equal(nextDay) {
		t.Fatalf("达到上限后下一项应等到次日：%+v", checkpoint)
	}

	schedulerWait(t, "当前项完成后停在日限", func() bool {
		p := f.progress(t)
		return p.State == "limit_reached" && p.Running == 0
	})
	if got := f.item(t, item.ID); got.Resolution == nil || *got.Resolution != "judge_submitted" {
		t.Fatalf("达到上限后已开始的项未能写入：%+v", got)
	}
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_work_states WHERE target_id=$1 AND result->'judge'->>'item_id'=$2`, item.TargetID, item.ID)
	if got := f.item(t, second.ID); got.Status != "waiting_model" || got.ConversationID != "" || got.Attempts != 0 {
		t.Fatalf("下一项应原样留在队列：%+v", got)
	}
	got := f.progress(t)
	if got.Today.InputTokens != highInput || got.Today.OutputTokens != 7 || got.Today.Date != "2026-01-02" {
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
	schedulerWait(t, "空答安排下一次尝试", func() bool { return f.item(t, second.ID).NextAttemptAt != nil })
	f.clock.now.Store(f.item(t, second.ID).NextAttemptAt.UnixNano())
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
	// 三类待办均已入队，缺配置时程序仍能写入它识别出的对应关系。
	selection := curationRulePending(t, f, "未配置的识别判断")
	local := curationRulePending(t, f, "未配置的本地辅助")
	mustExec(t, f.application, `UPDATE curation_pending_items SET kind='local_cleanup',evidence='{"directory_key":"合成目录"}' WHERE id=$1`, local.ID)
	verification := curationRulePending(t, f, "未配置的名字核对")
	mustExec(t, f.application, `UPDATE curation_pending_items SET kind='verify_name' WHERE id=$1`, verification.ID)
	b3RequireCount(t, f.application, 3, `SELECT count(*) FROM curation_pending_items WHERE id IN ($1,$2,$3) AND status='waiting_model' AND conversation_id IS NULL`, local.ID, selection.ID, verification.ID)
	requireCurationRuleRequestCount(t, f, 0)
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
	mustExec(t, f.application, `UPDATE curation_pending_items SET attempts=1,empty_count=1,single_request=true,review_retries=2,next_attempt_at=now()+interval '30 minutes' WHERE id=$1`, item.ID)
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
	if got.Attempts != 1 || got.EmptyCount != 1 || !got.SingleRequest || got.ClaimedAt != nil || got.ConversationID != "" || got.NextAttemptAt != nil || got.ReviewRetries != 0 {
		t.Fatalf("恢复后待定项 %+v", got)
	}
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM steward_plans WHERE id=$1 AND status='superseded'`, plan.ID)
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_batches WHERE id=$1 AND closed_at IS NOT NULL`, batch)
}

func TestPostgreSQL整理自查的摘要计入当天总额度(t *testing.T) {
	f := newSchedulerFixture(t)
	f.configure(t, &f.admin)
	item := f.pending(t, f.a)
	f.update(t, "PUT", curationBase+"/settings", map[string]any{"enabled": true, "daily_token_limit": 120008})
	q := f.next(t, false)
	q.inputTokens = 120001
	q.undetermined(t, item.ID, "无需工具查询")
	modelWaitResolution(t, f, item.ID, "undetermined")
	p := f.progress(t)
	if p.State != "limit_reached" || p.Today.InputTokens != 120001 || p.Today.OutputTokens != 7 {
		t.Fatalf("单次识别的实耗 %+v", p)
	}
	requireCurationRuleRequestCount(t, f, 1)
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM steward_reviews`)
}

func TestPostgreSQLCurationModelConcurrentDailyLimits(t *testing.T) {

	f := newSchedulerFixture(t)
	f.configure(t, &f.admin)
	configureModelWeb(t, f)
	first := f.pending(t, f.a)

	f.update(t, "PUT", curationBase+"/settings", map[string]any{"enabled": true, "model_concurrency": 2, "daily_token_limit": 20})

	a := f.next(t, false)
	second := f.pending(t, f.b)
	b := f.next(t, false)
	if strings.Contains(string(a.Messages[len(a.Messages)-1].Content), second.ID) {
		a, b = b, a
	}
	if !strings.Contains(string(a.Messages[len(a.Messages)-1].Content), first.ID) ||
		!strings.Contains(string(b.Messages[len(b.Messages)-1].Content), second.ID) {
		t.Fatal("模型首条消息与两项领取不符")
	}
	if p := f.progress(t); p.Running != 2 || p.Today.InputTokens != 0 || p.Today.OutputTokens != 0 {
		t.Fatalf("两项开始时的进度 %+v", p)
	}

	thirdRelease, _ := insertGroupedRelease(t, f.application, f.rootID, groupedRelease{
		title: "第三张专辑", artist: "第三位艺人", tracks: []string{"present"},
	})
	third := f.pending(t, thirdRelease)

	// 首项的 13 个输入、7 个输出 token 正好达到上限。
	a.undetermined(t, first.ID, "未找到发行依据")
	modelWaitResolution(t, f, first.ID, "undetermined")
	schedulerWait(t, "第一项达到日额后仍有一项运行", func() bool {
		p := f.progress(t)
		return p.Running == 1 && p.Today.InputTokens == 13 && p.Today.OutputTokens == 7
	})
	if p := f.item(t, third.ID); p.Status != "waiting_model" || p.ConversationID != "" {
		t.Fatalf("达到上限后提前领取第三项 %+v", p)
	}

	b.inputTokens = 33
	b.undetermined(t, second.ID, "已在途的发行完成核对")
	modelWaitResolution(t, f, second.ID, "undetermined")
	schedulerWait(t, "在途项完成且腾出全部并发名额", func() bool { return f.progress(t).Running == 0 })
	p := f.progress(t)
	if p.Today.InputTokens != 46 || p.Today.OutputTokens != 14 || p.State != "limit_reached" {
		t.Fatalf("应只超出第二项的 40 个 token，进度 %+v", p)
	}
	if pending := f.item(t, third.ID); pending.Status != "waiting_model" || pending.Attempts != 0 || pending.ConversationID != "" {
		t.Fatalf("第三项应保留原状 %+v", pending)
	}
	b3RequireCount(t, f.application, 2, `SELECT count(*) FROM steward_conversations WHERE kind='curation'`)
}

func TestPostgreSQLCurationModelRulesSharedDailyBudget(t *testing.T) {
	f := newSchedulerFixture(t)
	f.configure(t, &f.admin)
	chat := f.startChat(t)
	f.next(t, false).complete(t)
	finishSchedulerChat(t, chat)
	now := f.clock.options().Now()
	if usage, err := f.writer.TodayUsage(t.Context(), now); err != nil || usage.InputTokens != 0 || usage.OutputTokens != 0 {
		t.Fatalf("普通管家对话不进入共享额度：%+v %v", usage, err)
	}

	// 独立艺人队列按合同直接写共享账，不需要整理会话或待定项。
	requireCurationOK(t, f.writer.AddModelUsage(t.Context(), now, 100, 25))
	a, b := f.pending(t, f.a), f.pending(t, f.b)
	f.update(t, "PUT", curationBase+"/settings", map[string]any{
		"enabled": true, "daily_token_limit": 125, "model_concurrency": 1,
	})
	nextDay := curation.NextBudgetDay(now)
	p := requireCurationRulePause(t, f, "daily_limit", &nextDay)
	if p.CanRetry || p.Today.InputTokens != 100 || p.Today.OutputTokens != 25 || p.Items.WaitingModel != 2 {
		t.Fatalf("本地日限应读到艺人用量，保留原项且不允许探测：%+v", p)
	}
	f.update(t, "POST", curationBase+"/model/retry", nil)
	requireCurationRuleRequestCount(t, f, 1)
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM steward_conversations WHERE kind='curation'`)

	f.update(t, "PUT", curationBase+"/settings", map[string]int{"daily_token_limit": 130})
	first := f.next(t, false)
	requireCurationRuleItems(t, first, a.ID, b.ID)
	first.undetermined(t, a.ID, "合成证据不足")
	modelWaitResolution(t, f, a.ID, "undetermined")
	p = requireCurationRulePause(t, f, "daily_limit", &nextDay)
	if p.Today.InputTokens != 113 || p.Today.OutputTokens != 32 {
		t.Fatalf("已在途的实耗应完整写进同一天：%+v", p)
	}
	schedulerWait(t, "第二项留队并等待重试", func() bool { return f.item(t, b.ID).Status == "waiting_model" })
	f.clock.now.Store(nextDay.Add(-time.Second).UnixNano())
	requireCurationRulePause(t, f, "daily_limit", &nextDay)
	requireCurationRuleRequestCount(t, f, 2)
	f.clock.now.Store(nextDay.UnixNano())
	resumed := f.next(t, false)
	requireCurationRuleItems(t, resumed, b.ID)
	resumed.undetermined(t, b.ID, "合成候选结构不同")
	modelWaitResolution(t, f, b.ID, "undetermined")
	p = f.progress(t)
	if p.Today.Date != nextDay.Format(time.DateOnly) || p.Today.InputTokens != 13 || p.Today.OutputTokens != 7 {
		t.Fatalf("次日日界应使用新日账：%+v", p)
	}
}
