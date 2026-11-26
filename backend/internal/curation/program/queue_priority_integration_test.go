package program

import (
	"context"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/sources/musicbrainz"
)

func TestPostgreSQL整理队列五档取项与模型查询门槛(t *testing.T) {
	f := newPendingRefreshFixture(t)
	ctx := t.Context()
	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	_, err := f.writer.SetSettings(ctx, f.adminID, true)
	requirePendingRefreshOK(t, err)
	f.exec(t, `UPDATE curation_settings SET queue_baseline_at=$1 WHERE id=1`, now)
	// 按相反顺序插入，并故意让低优先级拥有更晚的入库时间。
	rows := []struct {
		name, class string
		rank        int
		added       time.Duration
		hasID       bool
		unlinked    bool
		gap         []int32
	}{
		{name: "完整且艺人已有对应", class: "local_complete", rank: 4, added: -time.Second},
		{name: "新入库且本地齐全", class: "local_complete", rank: 4, added: time.Second},
		{name: "完整且艺人缺对应", class: "local_complete", rank: 4, added: -8 * time.Second, unlinked: true},
		{name: "次要缺口", class: "has_gaps", rank: 3, added: -2 * time.Second, gap: []int32{0, 4, 1, 1}},
		{name: "身份缺口", class: "has_gaps", rank: 3, added: -9 * time.Second, gap: []int32{1, 0, 0, 0}},
		{name: "有可检索编号", class: "has_gaps", rank: 2, added: -3 * time.Second, hasID: true, gap: []int32{0, 0, 1, 1}},
		{name: "新入库", class: "has_gaps", rank: 1, added: time.Second},
		{name: "手动重查", class: "local_complete", rank: 0, added: -10 * time.Second},
	}
	ids := make([]string, len(rows))
	for i, row := range rows {
		id, _ := f.release(t, row.name, "合成艺人")
		ids[i] = id
		gap := row.gap
		if gap == nil {
			gap = []int32{0, 0, 0, 0}
		}
		f.exec(t, `INSERT INTO curation_work_states
			(subject,target_id,queue_release_id,queue_class,queue_has_id,queue_artist_unlinked,queue_gap,
			 queue_added_at,rule_version,input_digest,status,needs_work)
			VALUES ('release',$1,$1,$2,$3,$4,$5,$6,$7,'fixture','pending',true)`,
			id, row.class, row.hasID, row.unlinked, gap, now.Add(row.added), MatchRuleVersion)
	}
	manual := ids[len(ids)-1]
	f.exec(t, `UPDATE curation_work_states SET status='unmatched',attempts=1,needs_work=false WHERE target_id=$1`, manual)
	requirePendingRefreshOK(t, f.writer.RecheckUnlisted(ctx, manual))

	gate := curation.NewSources(f.db, curation.SourceRegistration{Declaration: musicbrainz.Declaration()})
	w := NewWorker(f.db, f.writer, gate, func(context.Context) (bool, error) { return false, nil }, nil, Options{
		Now: func() time.Time { return now },
	})
	marker, err := w.projectionMarker(ctx, now)
	requirePendingRefreshOK(t, err)
	w.marker = &marker
	for i := len(rows) - 1; i >= 0; i-- {
		want := rows[i]
		item, found, err := w.nextQueueItem(ctx, "release")
		requirePendingRefreshOK(t, err)
		if !found || item.TargetID != ids[i] || item.Rank != want.rank {
			t.Fatalf("期望领取%s（档 %d），实际 %+v，找到=%v", want.name, want.rank, item, found)
		}
		requirePendingRefreshOK(t, w.acknowledgeQueueItem(ctx, item))
	}

}
