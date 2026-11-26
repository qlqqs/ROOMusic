package program

import (
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPostgreSQLJudgeDifferentArtistsRememberUnorderedNames(t *testing.T) {
	ctx := t.Context()
	f := newPendingRefreshFixture(t)
	a, b := uuid.New(), uuid.New()
	f.exec(t, `INSERT INTO artists(id,initial_name) VALUES($1,'合成甲团'),($2,'合成乙团')`, a, b)
	first, _ := f.release(t, "第一张", "合成甲团")
	second, _ := f.release(t, "第二张", "合成乙团")
	third, _ := f.release(t, "第三张", "合成乙团")
	queue := func(writer *curation.Writer, release, digest, before, after, left, right string) {
		t.Helper()
		tx, err := f.db.BeginTx(ctx, nil)
		requirePendingRefreshOK(t, err)
		defer tx.Rollback()
		v := curation.VerificationCandidate{ReleaseID: release, Before: before, After: after, ArtistA: left, ArtistB: right}
		requirePendingRefreshOK(t, curation.QueueVerification(ctx, tx, "verify_artist_merge", digest, v))
		requirePendingRefreshOK(t, tx.Commit())
	}
	queue(f.writer, first, "first", "合成甲团", "合成乙团", a, b)
	item := f.claim(t, first, "verify_artist_merge")
	queue(f.writer, second, "second", "合成乙团", "合成甲团", b, a)
	requirePendingRefreshOK(t, f.writer.FinishIdentityNegative(ctx, item, "同名线索指向不同乐团", "synthetic"))
	var decisions int
	requirePendingRefreshOK(t, f.db.QueryRowContext(ctx, `SELECT count(*) FROM curation_artist_negative_pairs WHERE model='synthetic'`).Scan(&decisions))
	if decisions != 1 {
		t.Fatalf("同一无序名字对期望保存一条否定结论，实际 %d", decisions)
	}
	requirePendingRefreshOK(t, f.db.QueryRowContext(ctx, `SELECT count(*) FROM curation_pending_items WHERE kind='verify_artist_merge' AND status='resolved' AND resolution='different' AND target_id IN ($1,$2)`, first, second).Scan(&decisions))
	if decisions != 2 {
		t.Fatalf("已领取项和反向重复项都应以不同人结案，实际 %d", decisions)
	}

	restarted := curation.NewWriter(f.db, t.TempDir(), nil)
	queue(restarted, third, "new-rule-digest", "合成乙团", "合成甲团", b, a)
	items, err := restarted.PeekModelKind(ctx, time.Now(), 10, "verify", nil)
	requirePendingRefreshOK(t, err)
	requirePendingRefreshOK(t, f.db.QueryRowContext(ctx, `SELECT count(*) FROM curation_pending_items WHERE target_id=$1 AND kind='verify_artist_merge'`, third).Scan(&decisions))
	if len(items) != 0 || decisions != 0 {
		t.Fatalf("重建写入器后新发行的反向名字对不应排队，候选=%+v，待办=%d", items, decisions)
	}
}
