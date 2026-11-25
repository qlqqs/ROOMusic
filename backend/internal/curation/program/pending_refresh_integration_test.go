package program

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/testsupport"
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

func (f pendingRefreshFixture) claim(t *testing.T, release, kind string) curation.PendingItem {
	t.Helper()
	ctx := t.Context()
	conversation, err := runtime.NewStore(f.db).CreateCurationConversation(ctx, f.adminID, "合成自动整理", "synthetic")
	requirePendingRefreshOK(t, err)
	items, err := f.writer.ClaimPendingItems(ctx, conversation.ID, time.Now(), 1)
	requirePendingRefreshOK(t, err)
	if len(items) != 1 || items[0].TargetID != release || items[0].Kind != kind ||
		items[0].Status != "in_progress" || items[0].ConversationID != conversation.ID || items[0].ClaimedAt == nil {
		t.Fatalf("会话 %s 应领取发行 %s 的 %s，得到 %+v", conversation.ID, release, kind, items)
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
				id = f.claim(t, release, item.Kind).ID
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
			if tc.name != "shelved_same" {
				item.Candidates = json.RawMessage(`[{"title":"新候选"}]`)
				item.Conflicts = json.RawMessage(`["disc_fuzzy"]`)
			}
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
	claimed := f.claim(t, release, "release_match")
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
		state.GapDigest == before[release].GapDigest || state.IdentityDigest != before[release].IdentityDigest ||
		state.Generation != before[release].Generation || state.Attempts != before[release].Attempts || finder.searches != 1 {
		t.Errorf("写入歌词应只刷新本地证据，刷新前 %+v，刷新后 %+v，搜索 %d 次", before[release], state, finder.searches)
	}

	items, err := f.writer.ClaimedItems(ctx, claimed.ConversationID)
	requirePendingRefreshOK(t, err)
	if len(items) != 1 || items[0].ID != claimed.ID {
		t.Fatalf("原会话应仍领取待定项 %s，得到 %+v", claimed.ID, items)
	}
	if items[0].InputDigest != after[release].InputDigest || items[0].IdentityDigest != claimed.IdentityDigest {
		t.Errorf("原待定项应更新完整摘要并保留识别摘要，得到 %+v", items[0])
	}
	plan, err := NewModelMatcher(f.db, f.writer, gate).Plan(ctx, items[0], remote.ID)
	if err != nil {
		t.Fatalf("原领取会话的匹配预览失败（%s） %v", failureCode(err), err)
	}
	if plan.Target != remote.Title || len(plan.Changes) == 0 {
		t.Fatalf("未生成发行 %q 的匹配预览，得到 %+v", remote.Title, plan)
	}
}

func TestPostgreSQLCurationExternalRuleUpgradePending(t *testing.T) {
	for _, tc := range []struct {
		name           string
		withCandidates bool
	}{
		{"有保存候选时离线重判", true},
		{"零候选只登记和消费一次补查", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newShelvedPendingFixture(t, "release_match")
			ctx := t.Context()
			f.finder.search = []sources.Release{f.remote}
			requirePendingRefreshOK(t, f.worker.walk(ctx, false, nil, false))
			pending := f.pending(t)
			requirePendingCandidates(t, pending, f.remote.ID)
			if !tc.withCandidates {
				// 101 曾把零候选交给模型。构造该历史行以验证离线升级与补查。
				f.exec(t, "UPDATE curation_pending_items SET candidates='[]',conflicts='[]',evidence=evidence-'match_snapshot',route=NULL WHERE id=$1", pending.ID)
				pending = f.pending(t)
				f.finder.search = nil
			}
			read := func() curation.WorkState {
				t.Helper()
				states, err := curation.LoadWorkStates(ctx, f.db, "release", []string{f.releaseID})
				requirePendingRefreshOK(t, err)
				return states[f.releaseID]
			}
			before := read()
			old := MatchRuleVersion - 1
			f.exec(t, "UPDATE curation_work_states SET match_rule_version=$2 WHERE subject='release' AND target_id=$1", f.releaseID, old)
			f.exec(t, "UPDATE curation_pending_items SET match_rule_version=$2 WHERE id=$1", pending.ID, old)
			requirePendingRefreshOK(t, f.worker.walk(ctx, false, nil, false))
			upgrade := read()
			if f.finder.searches != 1 || len(f.finder.releaseReads) != 0 || upgrade.MatchRuleVersion != MatchRuleVersion ||
				upgrade.Generation != before.Generation || upgrade.Attempts != before.Attempts || upgrade.Status != before.Status {
				t.Fatalf("外部规则升级轮应离线复核并保留原结论，之前 %+v，之后 %+v，搜索 %d 次，读取 %v",
					before, upgrade, f.finder.searches, f.finder.releaseReads)
			}
			got := f.pending(t)
			if got.ID != pending.ID || got.MatchRuleVersion != MatchRuleVersion || got.IdentityDigest != pending.IdentityDigest ||
				got.Status != pending.Status || !bytes.Equal(got.Candidates, pending.Candidates) {
				t.Errorf("升级应更新原待定项版本并保留证据，之前 %+v，之后 %+v", pending, got)
			}
			expectedRequery, expectedSearches, newConclusions := int16(0), 1, 0
			if !tc.withCandidates {
				expectedRequery, expectedSearches, newConclusions = MatchRuleVersion, 3, 1
			}
			if upgrade.RequeryMatchVersion != expectedRequery {
				t.Errorf("登记补查版本 %d，期望 %d", upgrade.RequeryMatchVersion, expectedRequery)
			}
			requirePendingRefreshOK(t, f.worker.walk(ctx, false, nil, false))
			after := read()
			if !tc.withCandidates {
				resolved := f.load(t, pending.ID)
				if after.Status != "unlisted" || resolved.Status != "resolved" {
					t.Fatalf("历史零候选补查后应结清并记为未收录：状态 %+v，待定项 %+v", after, resolved)
				}
			}
			requirePendingRefreshOK(t, f.worker.walk(ctx, false, nil, false))
			if f.finder.searches != expectedSearches || len(f.finder.releaseReads) != 0 {
				t.Errorf("后续轮累计搜索 %d 次，期望 %d 次，读取 %v", f.finder.searches, expectedSearches, f.finder.releaseReads)
			}
			if after.RequeryMatchVersion != 0 || after.Generation != before.Generation+newConclusions ||
				after.Attempts != before.Attempts+newConclusions || !reflect.DeepEqual(read(), after) {
				t.Errorf("补查后的状态应稳定，补查版本 %d，代次 %d -> %d，尝试 %d -> %d",
					after.RequeryMatchVersion, before.Generation, after.Generation, before.Attempts, after.Attempts)
			}
		})
	}
	t.Run("完整快照直接定版且零出站", func(t *testing.T) {
		f := newShelvedPendingFixture(t, "release_match")
		ctx := t.Context()
		requirePendingRefreshOK(t, testsupport.SetArtistProjectionVersionCurrent(ctx, f.db))
		f.exec(t, `UPDATE tracks SET duration_seconds=100 WHERE medium_id IN (SELECT id FROM media WHERE release_id=$1)`, f.releaseID)
		f.finder.search = []sources.Release{f.remote}
		requirePendingRefreshOK(t, f.worker.walk(ctx, false, nil, false))
		pending := f.pending(t)
		f.remote.Complete = true
		count, offset := 1, 0
		snapshot := MatchSnapshot{Version: 1, Method: "search", Pages: []SearchPage{{
			PageInfo: curation.PageInfo{Count: &count, Offset: &offset, Limit: 100, Returned: 1, Complete: true},
			Kind:     "release", IDs: []string{f.remote.ID},
		}}}
		candidates := releaseCandidates("search", []sources.Release{f.remote})
		old := MatchRuleVersion - 1
		f.exec(t, `UPDATE curation_pending_items SET match_rule_version=$2,candidates=$3,conflicts='[]',
 evidence=jsonb_set(evidence,'{match_snapshot}',$4::jsonb) WHERE id=$1`, pending.ID, old, jsonValue(candidates), jsonValue(snapshot))
		f.exec(t, `UPDATE curation_work_states SET match_rule_version=$2 WHERE subject='release' AND target_id=$1`, f.releaseID, old)
		requirePendingRefreshOK(t, f.worker.walk(ctx, false, nil, false))
		requirePendingRefreshOK(t, f.worker.walk(ctx, false, nil, false))
		states, err := curation.LoadWorkStates(ctx, f.db, "release", []string{f.releaseID})
		requirePendingRefreshOK(t, err)
		state := states[f.releaseID]
		if state.Status != "matched" || state.MatchRuleVersion != MatchRuleVersion || f.finder.searches != 1 ||
			len(f.finder.releaseReads) != 0 || f.finder.recordingSearches != 0 || len(f.finder.recordingReads) != 0 {
			t.Fatalf("离线重判应写入版本且零外部请求：状态 %+v，调用 %+v", state, f.finder)
		}
		got := f.load(t, pending.ID)
		if got.Status != "resolved" || got.Resolution == nil || *got.Resolution != "program_matched" {
			t.Fatalf("离线定版后原待定项没有结清：%+v", got)
		}
		var linked string
		requirePendingRefreshOK(t, f.db.QueryRowContext(ctx, `SELECT external_id FROM external_links WHERE object_type='release' AND object_id=$1`, f.releaseID).Scan(&linked))
		if linked != f.remote.ID {
			t.Fatalf("离线写入版本 %s，期望 %s", linked, f.remote.ID)
		}
	})
}
