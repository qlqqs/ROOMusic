package program

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/musicbrainz"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
)

type pendingRefreshFixture struct {
	db              *sql.DB
	writer          *curation.Writer
	adminID, rootID string
}

func newPendingRefreshFixture(t *testing.T) pendingRefreshFixture {
	t.Helper()
	db, err := postgres.Open(t.Context(), postgrestest.NewSchema(t, "roomusic_test_"))
	requirePendingRefreshOK(t, err)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	dir := t.TempDir()
	f := pendingRefreshFixture{db: db, writer: curation.NewWriter(db, dir, nil), adminID: uuid.New(), rootID: uuid.New()}
	f.exec(t, `INSERT INTO users(id,username,password_hash,role) VALUES($1,'curation-admin','test-only','admin')`, f.adminID)
	f.exec(t, `INSERT INTO library_roots(id,path,status,revision) VALUES($1,$2,'active',1)`, f.rootID, dir)
	return f
}

func requirePendingRefreshOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func (f pendingRefreshFixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	_, err := f.db.ExecContext(t.Context(), query, args...)
	requirePendingRefreshOK(t, err)
}

func (f pendingRefreshFixture) release(t *testing.T, title, artist string) (string, string) {
	t.Helper()
	release, group, medium, firstTrack := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	f.exec(t, `INSERT INTO release_groups(id) VALUES($1)`, group)
	f.exec(t, `INSERT INTO releases(id,group_id,title,artist,source_root_id,relative_directory,candidate_anchor,candidate_kind)
		VALUES($1,$2,$3,$4,$5,$1::uuid::text,$1::uuid::text,'ordinary_directory')`, release, group, title, artist, f.rootID)
	f.exec(t, `INSERT INTO media(id,release_id,position,title) VALUES($1,$2,1,'合成碟')`, medium, release)
	f.exec(t, `INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,source_kind,source_identity)
		VALUES($1,$3,1,'曲1',$4,1,$5,$1::uuid::text || '.flac','present',now(),'physical',$1::uuid::text),
		      ($2,$3,2,'曲2',$4,1,$5,$2::uuid::text || '.flac','present',now(),'physical',$2::uuid::text)`,
		firstTrack, uuid.New(), medium, artist, f.rootID)
	return release, firstTrack
}

func (f pendingRefreshFixture) claim(t *testing.T, release string) curation.PendingItem {
	t.Helper()
	ctx := t.Context()
	conversation, err := runtime.NewStore(f.db).CreateCurationConversation(ctx, f.adminID, "合成自动整理", "synthetic")
	requirePendingRefreshOK(t, err)
	items, err := f.writer.ClaimPendingItems(ctx, conversation.ID, time.Now(), 1)
	requirePendingRefreshOK(t, err)
	if len(items) != 1 || items[0].TargetID != release || items[0].Kind != "release_match" ||
		items[0].Status != "in_progress" || items[0].ConversationID != conversation.ID || items[0].ClaimedAt == nil {
		t.Fatalf("会话 %s 应领取发行 %s 的 release_match，得到 %+v", conversation.ID, release, items)
	}
	return items[0]
}

func TestPostgreSQLCurationPendingUpsert(t *testing.T) {
	for _, tc := range []struct {
		name, status, digest, wantStatus string
		refresh                          bool
	}{
		{"in_progress", "in_progress", "new", "in_progress", true},
		{"waiting_model", "waiting_model", "new", "waiting_model", true},
		{"shelved_changed", "shelved", "new", "waiting_model", true},
		{"shelved_same", "shelved", "old", "shelved", false},
		{"waiting_human", "waiting_human", "new", "waiting_human", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPendingRefreshFixture(t)
			ctx := t.Context()
			release, _ := f.release(t, tc.name, "合成乐队")
			item := curation.PendingItem{
				TargetID: release, Kind: "release_match", RuleVersion: StoredRuleVersion, InputDigest: "old",
				Evidence: json.RawMessage(`{"title":"旧资料"}`), Candidates: json.RawMessage(`[{"title":"旧候选"}]`),
				Conflicts: json.RawMessage(`["search_only"]`),
			}
			requirePendingRefreshOK(t, curation.UpsertPendingItem(ctx, f.db, item))
			var id string
			if tc.status == "in_progress" || tc.status == "waiting_human" {
				id = f.claim(t, release).ID
			} else {
				err := f.db.QueryRowContext(ctx, `SELECT id FROM curation_pending_items WHERE target_id=$1`, release).Scan(&id)
				requirePendingRefreshOK(t, err)
			}
			// 写入非零的重试字段与原因，确保刷新时能区分保留和清空。
			f.exec(t, `UPDATE curation_pending_items SET status=$2,attempts=2,review_retries=1,
				next_attempt_at=now()+interval '1 hour',shelved_reason='unhandled',updated_at=now()-interval '1 day' WHERE id=$1`,
				id, tc.status)
			before, err := f.writer.PendingItem(ctx, id)
			requirePendingRefreshOK(t, err)

			item.InputDigest, item.RuleVersion = tc.digest, StoredRuleVersion+1
			item.Evidence = json.RawMessage(`{"title":"新资料"}`)
			item.Candidates = json.RawMessage(`[{"title":"新候选"}]`)
			item.Conflicts = json.RawMessage(`["disc_fuzzy"]`)
			requirePendingRefreshOK(t, curation.UpsertPendingItem(ctx, f.db, item))
			got, err := f.writer.PendingItem(ctx, id)
			requirePendingRefreshOK(t, err)
			if !tc.refresh {
				if !reflect.DeepEqual(got, before) {
					t.Fatalf("待定项应保持原样，更新前 %+v，更新后 %+v", before, got)
				}
				return
			}

			if !got.UpdatedAt.After(before.UpdatedAt) {
				t.Errorf("待定项更新时间未推进，更新前 %v，更新后 %v", before.UpdatedAt, got.UpdatedAt)
			}
			for _, field := range []struct {
				name      string
				got, want json.RawMessage
			}{
				{"evidence", got.Evidence, item.Evidence},
				{"candidates", got.Candidates, item.Candidates},
				{"conflicts", got.Conflicts, item.Conflicts},
			} {
				var compact bytes.Buffer
				requirePendingRefreshOK(t, json.Compact(&compact, field.got))
				if !bytes.Equal(compact.Bytes(), field.want) {
					t.Errorf("%s = %s，期望 %s", field.name, field.got, field.want)
				}
			}
			want := before
			want.Status, want.InputDigest, want.RuleVersion, want.UpdatedAt = tc.wantStatus, item.InputDigest, item.RuleVersion, got.UpdatedAt
			want.Evidence, want.Candidates, want.Conflicts = got.Evidence, got.Candidates, got.Conflicts
			if tc.status != "in_progress" {
				want.ConversationID, want.ClaimedAt, want.ReviewRetries, want.ShelvedReason = "", nil, 0, nil
			}
			if tc.status == "shelved" {
				want.Attempts, want.NextAttemptAt = 0, nil
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("刷新后的待定项为 %+v，期望 %+v", got, want)
			}
		})
	}
}

func TestPostgreSQLCurationModelPlanAfterClaimRefresh(t *testing.T) {
	f := newPendingRefreshFixture(t)
	ctx := t.Context()
	_, err := f.writer.SetSettings(ctx, f.adminID, true)
	requirePendingRefreshOK(t, err)
	_, remote := b3Album(2)
	remote.ID, remote.Group.ID, remote.Artists[0].ArtistID = uuid.New(), uuid.New(), uuid.New()
	for i := range remote.Media[0].Tracks {
		remote.Media[0].Tracks[i].ID, remote.Media[0].Tracks[i].RecordingID = uuid.New(), uuid.New()
	}
	finder := &b3MatchFinder{releases: map[string]sources.Release{remote.ID: remote}, search: []sources.Release{remote}}
	gate := curation.NewSources(f.db, curation.SourceRegistration{
		Declaration: musicbrainz.Declaration(), Releases: finder, Recordings: finder,
	})
	worker := NewWorker(f.db, f.writer, gate, func(context.Context) (bool, error) { return false, nil }, nil, Options{})
	release, track := f.release(t, remote.Title, remote.Artists[0].Name)

	requirePendingRefreshOK(t, worker.walk(ctx, false, nil, false))
	claimed := f.claim(t, release)
	before, err := curation.LoadWorkStates(ctx, f.db, "release", []string{release})
	requirePendingRefreshOK(t, err)
	if state := before[release]; state.Status != "uncertain" || state.InputDigest != claimed.InputDigest {
		t.Fatalf("初次判定状态为 %+v，领取的待定项为 %+v", state, claimed)
	}

	lyrics, err := f.writer.ApplyLyrics(ctx, curation.LyricsRequest{
		TrackID: track, Text: "合成的外部歌词", Source: curation.Source{Kind: "source", Provider: "lrclib"},
		ExternalID: "42", Key: uuid.New(), RequestID: "pending-refresh-lyrics",
	})
	requirePendingRefreshOK(t, err)
	if lyrics.OperationID == "" || lyrics.Skipped != "" {
		t.Fatalf("外部歌词写入结果为 %+v", lyrics)
	}
	requirePendingRefreshOK(t, worker.walk(ctx, false, nil, false))
	after, err := curation.LoadWorkStates(ctx, f.db, "release", []string{release})
	requirePendingRefreshOK(t, err)
	if state := after[release]; state.Status != "uncertain" || state.InputDigest == claimed.InputDigest ||
		state.Generation != before[release].Generation+1 {
		t.Fatalf("写入歌词后应使用新摘要重判，重判前 %+v，重判后 %+v", before[release], state)
	}

	items, err := f.writer.ClaimedItems(ctx, claimed.ConversationID)
	requirePendingRefreshOK(t, err)
	if len(items) != 1 || items[0].ID != claimed.ID {
		t.Fatalf("原会话应仍领取待定项 %s，得到 %+v", claimed.ID, items)
	}
	plan, err := NewModelMatcher(f.db, f.writer, gate).Plan(ctx, items[0], remote.ID)
	if err != nil {
		t.Fatalf("原领取会话的匹配预览失败（%s） %v", failureCode(err), err)
	}
	if plan.Target != remote.Title || len(plan.Changes) == 0 {
		t.Fatalf("未生成发行 %q 的匹配预览，得到 %+v", remote.Title, plan)
	}
}
