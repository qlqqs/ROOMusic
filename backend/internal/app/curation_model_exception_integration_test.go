package app

import (
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPostgreSQLCurationExceptionStore(t *testing.T) {
	f := newCurationFixture(t)
	ctx := t.Context()
	conversation := modelConversation(t, f)
	item := modelClaim(t, f, conversation, modelPending(t, f, f.a, "release_match", "exception"))
	risk := "medium"
	spec := curation.ModelException{
		PendingItemID: item.ID, PlanItemID: uuid.New(), Tool: "resolve_release_match", Provider: "Synthetic/Model",
		Input: []byte(`{"external_id":"合成发行 ID"}`), Preview: []byte(`{"changes":[]}`), ConversationID: &conversation,
		Review: curation.ExceptionReview{ID: uuid.New(), Model: "合成审查模型", Decision: "needs_human_confirmation",
			Source: "model", Risk: &risk, Reasons: []string{"候选需要人工核对"}},
	}
	matchID, err := f.writer.CreateException(ctx, spec)
	requireCurationOK(t, err)
	replayID, err := f.writer.CreateException(ctx, spec)
	requireCurationOK(t, err)
	if !uuid.Valid(matchID) || replayID != matchID {
		t.Fatalf("创建例外=%s，重放=%s", matchID, replayID)
	}
	second := spec
	second.PlanItemID, second.Tool = uuid.New(), "correct_release"
	second.Review.Decision = "rejected"
	fieldID, err := f.writer.CreateException(ctx, second)
	requireCurationOK(t, err)
	waiting, err := f.writer.PendingItem(ctx, item.ID)
	requireCurationOK(t, err)
	if waiting.Status != "resolved" || waiting.Resolution == nil || *waiting.Resolution != "suggestion_recorded" || waiting.ConversationID != conversation || waiting.NextAttemptAt != nil {
		t.Fatalf("创建例外后待定项为 %+v", waiting)
	}

	read := func(id string) curation.ModelException {
		t.Helper()
		got, err := f.writer.Exception(ctx, id)
		requireCurationOK(t, err)
		return got
	}
	first := read(matchID)
	if first.PendingItemID != item.ID || first.Kind != item.Kind || first.PlanItemID != spec.PlanItemID ||
		first.Status != "open" || first.DecidedBy != nil || first.DecidedAt != nil || len(first.OperationIDs) != 0 ||
		first.Provider != "synthetic-model" || first.ConversationID == nil || *first.ConversationID != conversation ||
		first.Tool != spec.Tool || first.ReasonCode != "review_uncertain" || !first.CanAccept || !slices.Equal(first.Reasons, spec.Review.Reasons) || first.Release.ID != f.a || first.Release.Title != "Album A" || !reflect.DeepEqual(first.Review, spec.Review) {
		t.Fatalf("存下的例外为 %+v", first)
	}
	requireExternalJSON(t, first.Input, spec.Input)
	requireExternalJSON(t, first.Preview, spec.Preview)

	mustExec(t, f.application, `UPDATE curation_model_exceptions SET created_at=now()-interval '1 day' WHERE id=$1`, matchID)
	expectPage := func(filter string, page, size int, total int64, ids ...string) {
		t.Helper()
		items, gotTotal, err := f.writer.ListExceptions(ctx, filter, page, size)
		requireCurationOK(t, err)
		var got []string
		for _, item := range items {
			got = append(got, item.ID)
		}
		if gotTotal != total || !slices.Equal(got, ids) {
			t.Fatalf("筛选 %s 第 %d 页为 %v，共 %d 项；期望 %v，共 %d 项", filter, page, got, gotTotal, ids, total)
		}
	}
	expectPage("open", 1, 1, 2, fieldID)
	expectPage("open", 2, 1, 2, matchID)

	other := f.application.createUser(t, "admin")
	type claimResult struct {
		actor string
		err   error
	}
	start := make(chan struct{})
	ready := make(chan struct{}, 2)
	results := make(chan claimResult, 2)
	for _, actor := range []string{f.admin.id, other.id} {
		go func() {
			ready <- struct{}{}
			<-start
			results <- claimResult{actor, f.writer.ClaimException(ctx, matchID, actor)}
		}()
	}
	<-ready
	<-ready
	close(start)
	var winner, loser string
	for range 2 {
		result := <-results
		if result.err == nil {
			winner = result.actor
		} else {
			curationCRequireProblem(t, result.err, 409, "exception_decided")
			loser = result.actor
		}
	}
	if winner == "" || loser == "" {
		t.Fatalf("领取应有一人成功、一人冲突，得到成功者 %q、冲突者 %q", winner, loser)
	}
	requireCurationOK(t, f.writer.ClaimException(ctx, matchID, winner))
	claimed := read(matchID)
	if claimed.Status != "accepting" || claimed.DecidedBy == nil || *claimed.DecidedBy != winner || claimed.DecidedAt != nil {
		t.Fatalf("领取后的例外为 %+v", claimed)
	}
	expectPage("open", 1, 5, 1, fieldID)

	// 结论与处理人的组合由数据库约束保障。
	for _, statement := range []string{
		`UPDATE curation_model_exceptions SET decided_by=NULL WHERE id=$1`,
		`UPDATE curation_model_exceptions SET decided_at=now() WHERE id=$1`,
		`UPDATE curation_model_exceptions SET status='accepted' WHERE id=$1`,
	} {
		_, err := f.application.database.ExecContext(ctx, statement, matchID)
		var failure interface{ SQLState() string }
		if !errors.As(err, &failure) || failure.SQLState() != "23514" {
			t.Fatalf("结论字段约束未生效，语句 %q，错误 %v", statement, err)
		}
	}

	finish := func(id, actor, decision string, operations []string, status int, code string) {
		t.Helper()
		tx, err := f.application.database.BeginTx(ctx, nil)
		requireCurationOK(t, err)
		defer tx.Rollback()
		err = curation.FinishException(ctx, tx, id, actor, decision, operations)
		if status != 0 {
			curationCRequireProblem(t, err, status, code)
			return
		}
		requireCurationOK(t, err)
		requireCurationOK(t, tx.Commit())
	}
	finish(matchID, winner, "open", nil, 400, "invalid_input")
	finish(matchID, loser, "accepted", nil, 409, "exception_decided")
	finish(fieldID, winner, "declined", nil, 0, "")
	declined := read(fieldID)
	if declined.Status != "declined" || declined.DecidedBy == nil || *declined.DecidedBy != winner || declined.DecidedAt == nil || len(declined.OperationIDs) != 0 {
		t.Fatalf("不采纳后的例外为 %+v", declined)
	}
	op := uuid.New()
	finish(matchID, winner, "accepted", []string{op}, 0, "")
	accepted := read(matchID)
	if accepted.Status != "accepted" || accepted.DecidedBy == nil || *accepted.DecidedBy != winner || accepted.DecidedAt == nil ||
		!slices.Equal(accepted.OperationIDs, []string{op}) {
		t.Fatalf("采纳后的例外为 %+v", accepted)
	}
	expectPage("open", 1, 5, 0)
	expectPage("all", 1, 5, 2, fieldID, matchID)
	resolved, err := f.writer.PendingItem(ctx, item.ID)
	requireCurationOK(t, err)
	if resolved.Status != "resolved" || resolved.Resolution == nil || *resolved.Resolution != "suggestion_recorded" || resolved.ResolvedAt == nil {
		t.Fatalf("例外处理完毕后的待定项为 %+v", resolved)
	}
}

func TestPostgreSQLCurationModelDailyUsage(t *testing.T) {
	f := newCurationFixture(t)
	ctx := t.Context()
	// 服务器本地日期已跨日，UTC 日期仍在前一天。
	today := time.Date(2026, 11, 23, 0, 30, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	yesterday := today.AddDate(0, 0, -1)
	tomorrow := today.AddDate(0, 0, 1)
	requireCurationOK(t, f.writer.AddModelUsage(ctx, yesterday, 41, 13))

	const workers = 12
	start := make(chan struct{})
	ready := make(chan struct{}, workers)
	results := make(chan error, workers)
	for range workers {
		go func() {
			ready <- struct{}{}
			<-start
			results <- f.writer.AddModelUsage(ctx, today, 7, 3)
		}()
	}
	for range workers {
		<-ready
	}
	close(start)
	for range workers {
		if err := <-results; err != nil {
			t.Errorf("并发累加用量失败 %v", err)
		}
	}
	for _, tc := range []struct {
		at   time.Time
		want curation.ModelUsage
	}{
		{yesterday, curation.ModelUsage{Date: "2026-11-22", InputTokens: 41, OutputTokens: 13}},
		{today, curation.ModelUsage{Date: "2026-11-23", InputTokens: workers * 7, OutputTokens: workers * 3}},
		{tomorrow, curation.ModelUsage{Date: "2026-11-24"}},
	} {
		got, err := f.writer.TodayUsage(ctx, tc.at)
		requireCurationOK(t, err)
		if got != tc.want {
			t.Errorf("每日用量=%+v，期望 %+v", got, tc.want)
		}
	}
}

func TestPostgreSQLCurationModelSettings(t *testing.T) {
	f := newCurationFixture(t)
	f.enable(t)
	ctx := t.Context()
	mustExec(t, f.application, `UPDATE curation_settings SET updated_at='2026-01-01T00:00:00Z',
		model_updated_at='2026-01-02T00:00:00Z' WHERE id=1`)
	readTimestamps := func() (programTime, modelTime time.Time) {
		t.Helper()
		err := f.application.database.QueryRowContext(ctx, `SELECT updated_at,model_updated_at FROM curation_settings WHERE id=1`).Scan(&programTime, &modelTime)
		requireCurationOK(t, err)
		return
	}
	oldProgram, oldModel := readTimestamps()
	initial, err := f.writer.Settings(ctx)
	requireCurationOK(t, err)
	if initial.Model != "" || initial.DailyTokenLimit == nil || *initial.DailyTokenLimit != 10_000_000 {
		t.Fatalf("模型默认设置 %+v", initial)
	}
	model, limit := "Synthetic/Model", int64(40000)
	patch := curation.ModelSettingsPatch{Model: &model, SetDailyTokenLimit: true, DailyTokenLimit: &limit}
	updated, err := f.writer.UpdateModelSettings(ctx, f.admin.id, nil, nil, patch)
	requireCurationOK(t, err)
	if updated.Model != model || updated.DailyTokenLimit == nil || *updated.DailyTokenLimit != limit {
		t.Fatalf("模型设置写入结果 %+v", updated)
	}
	saved, err := f.writer.Settings(ctx)
	requireCurationOK(t, err)
	if !reflect.DeepEqual(saved, updated) || !saved.Active || saved.EnabledBy == nil || saved.EnabledBy.ID != f.admin.id {
		t.Fatalf("读回模型设置 %+v，写入结果 %+v", saved, updated)
	}
	programTime, modelTime := readTimestamps()
	if !programTime.Equal(oldProgram) || !modelTime.After(oldModel) {
		t.Fatalf("程序设置时间 %v → %v，模型设置时间 %v → %v", oldProgram, programTime, oldModel, modelTime)
	}
	patch = curation.ModelSettingsPatch{SetDailyTokenLimit: true}
	_, err = f.writer.UpdateModelSettings(ctx, f.admin.id, nil, nil, patch)
	requireCurationOK(t, err)
	saved, err = f.writer.Settings(ctx)
	requireCurationOK(t, err)
	if saved.Model != model || saved.DailyTokenLimit != nil {
		t.Fatalf("保存不限用量后模型设置 %+v", saved)
	}
	programTime, modelTime = readTimestamps()
	if !programTime.Equal(oldProgram) || !modelTime.After(oldModel) {
		t.Fatalf("设置不限用量后程序时间 %v、模型时间 %v", programTime, modelTime)
	}
}
