package app

import (
	"context"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
)

func TestPostgreSQLCurationPendingClaim(t *testing.T) {
	f := newCurationFixture(t)
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	third, _ := insertGroupedRelease(t, f.application, f.rootID, groupedRelease{title: "合成发行 C", artist: "合成艺人", tracks: []string{"present"}})
	future, _ := insertGroupedRelease(t, f.application, f.rootID, groupedRelease{title: "未到期发行", artist: "合成艺人", tracks: []string{"present"}})
	hiddenItem := modelPending(t, f, f.hidden, "release_match", "hidden")
	futureItem := modelPending(t, f, future, "release_match", "future")
	oldestItem := modelPending(t, f, f.a, "release_match", "oldest")
	secondItem := modelPending(t, f, f.b, "release_match", "second")
	thirdItem := modelPending(t, f, third, "release_match", "third")
	for i, item := range []curation.PendingItem{hiddenItem, futureItem, oldestItem, secondItem, thirdItem} {
		mustExec(t, f.application, `UPDATE curation_pending_items SET created_at=$2 WHERE id=$1`, item.ID, now.Add(time.Duration(i-10)*time.Minute))
	}
	mustExec(t, f.application, `UPDATE curation_pending_items SET next_attempt_at=$2 WHERE id=$1`, futureItem.ID, now.Add(time.Second))
	mustExec(t, f.application, `UPDATE curation_pending_items SET next_attempt_at=$2 WHERE id=$1`, secondItem.ID, now)
	firstConversation, secondConversation := modelConversation(t, f), modelConversation(t, f)

	lock, err := f.application.database.BeginTx(t.Context(), nil)
	requireCurationOK(t, err)
	defer lock.Rollback()
	var lockedID string
	err = lock.QueryRowContext(t.Context(), `SELECT id FROM curation_pending_items WHERE id=$1 FOR UPDATE`, oldestItem.ID).Scan(&lockedID)
	requireCurationOK(t, err)

	// 另一事务应在第一事务仍持锁时返回。若没跳过行锁，调用会超时。
	claimCtx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	claimed, err := f.writer.ClaimPendingItems(claimCtx, firstConversation, now, 5)
	requireCurationOK(t, err)
	assertClaims := func(items []curation.PendingItem, conversation string, ids ...string) {
		t.Helper()
		var got []string
		for _, item := range items {
			got = append(got, item.ID)
			if item.Status != "in_progress" || item.ConversationID != conversation ||
				item.ClaimedAt == nil || !item.ClaimedAt.Equal(now) || item.NextAttemptAt != nil {
				t.Errorf("领取后的待定项为 %+v", item)
			}
		}
		if !slices.Equal(got, ids) {
			t.Fatalf("领取顺序 %v，期望 %v", got, ids)
		}
	}
	assertClaims(claimed, firstConversation, secondItem.ID, thirdItem.ID)
	requireCurationOK(t, lock.Commit())

	claimed, err = f.writer.ClaimPendingItems(t.Context(), secondConversation, now, 5)
	requireCurationOK(t, err)
	assertClaims(claimed, secondConversation, oldestItem.ID)

	requireCurationOK(t, f.writer.ReleaseClaimed(t.Context(), firstConversation, false, nil))
	requireCurationOK(t, f.writer.ReleaseClaimed(t.Context(), secondConversation, false, nil))
	type claimResult struct {
		items []curation.PendingItem
		err   error
	}
	start := make(chan struct{})
	results := make(chan claimResult, 2)
	for _, conversation := range []string{firstConversation, secondConversation} {
		go func() {
			<-start
			items, err := f.writer.ClaimPendingItems(t.Context(), conversation, now, 2)
			results <- claimResult{items, err}
		}()
	}
	close(start)
	seen := map[string]int{}
	for range 2 {
		result := <-results
		requireCurationOK(t, result.err)
		for _, item := range result.items {
			seen[item.ID]++
		}
	}
	want := map[string]int{oldestItem.ID: 1, secondItem.ID: 1, thirdItem.ID: 1}
	if !maps.Equal(seen, want) {
		t.Fatalf("并发领取各项次数 %v，期望 %v", seen, want)
	}
}

func TestPostgreSQLCurationPendingLifecycle(t *testing.T) {
	f := newCurationFixture(t)
	ctx := t.Context()
	conversation := modelConversation(t, f)
	item := modelPending(t, f, f.a, "release_match", "first")
	load := func() curation.PendingItem {
		t.Helper()
		got, err := f.writer.PendingItem(ctx, item.ID)
		requireCurationOK(t, err)
		return got
	}
	expectWaiting := func(attempts int, next *time.Time) {
		t.Helper()
		got := load()
		if got.Status != "waiting_model" || got.Attempts != attempts || got.ConversationID != "" || got.ClaimedAt != nil ||
			got.ReviewRetries != 0 || got.ShelvedReason != nil ||
			(next == nil && got.NextAttemptAt != nil) || (next != nil && (got.NextAttemptAt == nil || !got.NextAttemptAt.Equal(*next))) {
			t.Fatalf("待定项=%+v，期望 waiting_model、次数 %d、下次时间 %v", got, attempts, next)
		}
	}
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	next := now.Add(24 * time.Hour)
	claim := func(at time.Time) {
		t.Helper()
		got, err := f.writer.ClaimPendingItems(ctx, conversation, at, 1)
		requireCurationOK(t, err)
		if len(got) != 1 || got[0].ID != item.ID {
			t.Fatalf("领取=%+v，期望 %s", got, item.ID)
		}
	}

	claim(now)
	requireCurationOK(t, f.writer.ReleaseClaimed(ctx, conversation, true, nil))
	expectWaiting(1, nil)
	claim(now)
	requireCurationOK(t, f.writer.ReleaseClaimed(ctx, conversation, false, &next))
	expectWaiting(1, &next)
	got, err := f.writer.ClaimPendingItems(ctx, conversation, now, 1)
	requireCurationOK(t, err)
	if len(got) != 0 {
		t.Fatalf("提前领取了延期项 %+v", got)
	}

	claim(next)
	reviewAt := next.Add(5 * time.Minute)
	requireCurationOK(t, f.writer.RetryClaimedReview(ctx, conversation, reviewAt))
	retrying := load()
	if retrying.Status != "in_progress" || retrying.Attempts != 1 || retrying.ReviewRetries != 1 ||
		retrying.ConversationID != conversation || retrying.NextAttemptAt == nil || !retrying.NextAttemptAt.Equal(reviewAt) {
		t.Fatalf("等待重审的待定项为 %+v", retrying)
	}
	requireCurationOK(t, f.writer.RecoverInProgress(ctx))
	expectWaiting(1, nil)

	claim(next)
	requireCurationOK(t, f.writer.ReleaseClaimed(ctx, conversation, true, nil))
	expectWaiting(2, nil)
	claim(next)
	requireCurationOK(t, f.writer.ReleaseClaimed(ctx, conversation, true, nil))
	assertShelved := func(got curation.PendingItem) {
		t.Helper()
		if got.ID != item.ID || got.Status != "shelved" || got.Attempts != 3 || got.ShelvedReason == nil || *got.ShelvedReason != "unhandled" ||
			got.ConversationID != "" || got.ClaimedAt != nil || got.NextAttemptAt != nil {
			t.Fatalf("待定项=%+v，期望第三次未处理后搁置", got)
		}
	}
	assertShelved(load())
	assertShelved(modelPending(t, f, f.a, item.Kind, "first"))
	reopened := modelPending(t, f, f.a, item.Kind, "changed")
	if reopened.ID != item.ID || reopened.InputDigest != "changed" {
		t.Fatalf("应在原项 %s 更新摘要，得到 %+v", item.ID, reopened)
	}
	expectWaiting(0, nil)

	claim(next)
	mustExec(t, f.application, `UPDATE curation_pending_items SET status='waiting_human' WHERE id=$1`, item.ID)
	requireCurationOK(t, f.writer.RecoverInProgress(ctx))
	if status := load().Status; status != "waiting_human" {
		t.Fatalf("恢复后状态为 %q，期望 waiting_human", status)
	}
	requireCurationOK(t, curation.ResolvePendingItems(ctx, f.application.database, f.a, item.Kind, "program_matched"))
	resolved := load()
	if resolved.Status != "resolved" || resolved.Resolution == nil || *resolved.Resolution != "program_matched" || resolved.ResolvedAt == nil {
		t.Fatalf("程序结案后待定项为 %+v", resolved)
	}
}
