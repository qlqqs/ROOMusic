package program

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/musicbrainz"
)

type pendingSearchFinder struct {
	b3MatchFinder
	recordingSearch   []sources.Recording
	recordingSearches int
	completePage      bool
}

func (f *pendingSearchFinder) SearchRecordings(context.Context, sources.RecordingQuery) ([]sources.Recording, error) {
	f.recordingSearches++
	return f.recordingSearch, nil
}

func (f *pendingSearchFinder) SearchReleasePage(ctx context.Context, q curation.ReleaseSearch) (curation.ReleasePage, error) {
	rows, err := f.SearchReleases(ctx, sources.ReleaseQuery{Title: q.Title, Artist: q.Artist, TrackCount: q.TrackCount})
	info := curation.PageInfo{}
	if f.completePage {
		count, offset := len(rows), 0
		info = curation.PageInfo{Count: &count, Offset: &offset, Limit: 100, Returned: count, Complete: true}
	}
	return curation.ReleasePage{Items: rows, PageInfo: info}, err
}

type shelvedPendingFixture struct {
	pendingRefreshFixture
	finder          *pendingSearchFinder
	worker          *Worker
	matcher         *ModelMatcher
	remote          sources.Release
	releaseID, kind string
}

func newShelvedPendingFixture(t *testing.T, kind string) shelvedPendingFixture {
	t.Helper()
	f := newPendingRefreshFixture(t)
	_, err := f.writer.SetSettings(t.Context(), f.adminID, true)
	requirePendingRefreshOK(t, err)
	_, remote := b3Album(2)
	remote.ID, remote.Group.ID, remote.Artists[0].ArtistID = uuid.New(), uuid.New(), uuid.New()
	for i := range remote.Media[0].Tracks {
		remote.Media[0].Tracks[i].ID, remote.Media[0].Tracks[i].RecordingID = uuid.New(), uuid.New()
	}
	releaseID, trackID := f.release(t, remote.Title, remote.Artists[0].Name)
	if kind == "recording_match" {
		f.exec(t, "UPDATE releases SET candidate_kind='loose_unknown' WHERE id=$1", releaseID)
		f.exec(t, "UPDATE tracks SET source_status='missing' WHERE medium_id=(SELECT medium_id FROM tracks WHERE id=$1) AND id<>$1", trackID)
	}
	finder := &pendingSearchFinder{b3MatchFinder: b3MatchFinder{releases: map[string]sources.Release{remote.ID: remote}}}
	gate := curation.NewSources(f.db, curation.SourceRegistration{
		Declaration: musicbrainz.Declaration(), Releases: finder, Recordings: finder,
	})
	return shelvedPendingFixture{
		pendingRefreshFixture: f, finder: finder, matcher: NewModelMatcher(f.db, f.writer, gate),
		worker: NewWorker(f.db, f.writer, gate, func(context.Context) (bool, error) { return false, nil }, nil, Options{}),
		remote: remote, releaseID: releaseID, kind: kind,
	}
}

func (f shelvedPendingFixture) load(t *testing.T, id string) curation.PendingItem {
	t.Helper()
	item, err := f.writer.PendingItem(t.Context(), id)
	requirePendingRefreshOK(t, err)
	return item
}

func (f shelvedPendingFixture) pending(t *testing.T) curation.PendingItem {
	t.Helper()
	var id string
	err := f.db.QueryRowContext(t.Context(), "SELECT id FROM curation_pending_items WHERE target_id=$1 AND kind=$2 AND status<>'resolved'", f.releaseID, f.kind).Scan(&id)
	requirePendingRefreshOK(t, err)
	return f.load(t, id)
}

func (f shelvedPendingFixture) rejudge(t *testing.T) {
	t.Helper()
	// 消费已登记的补查机会，用真实外部结果检查搁置项是否应重开。
	f.exec(t, "UPDATE curation_work_states SET requery_match_version=$2 WHERE subject='release' AND target_id=$1", f.releaseID, MatchRuleVersion)
	requirePendingRefreshOK(t, f.worker.walk(t.Context(), false, nil, false))
	states, err := curation.LoadWorkStates(t.Context(), f.db, "release", []string{f.releaseID})
	requirePendingRefreshOK(t, err)
	if state := states[f.releaseID]; state.RequeryMatchVersion != 0 {
		t.Fatalf("程序未消费补查机会，工作状态为 %+v", state)
	}
}

func requirePendingCandidates(t *testing.T, item curation.PendingItem, wantIDs ...string) {
	t.Helper()
	var candidates []Candidate
	requirePendingRefreshOK(t, json.Unmarshal(item.Candidates, &candidates))
	var gotIDs []string
	for _, candidate := range candidates {
		gotIDs = append(gotIDs, candidate.ExternalID)
	}
	if !slices.Equal(gotIDs, wantIDs) {
		t.Fatalf("待定项 %s 的候选 ID 为 %v，期望 %v", item.ID, gotIDs, wantIDs)
	}
}

// RunShelvedPendingCases 复用包内工作者夹具，人工决定由外部测试调用模型层入口。
func RunShelvedPendingCases(t *testing.T, decline func(context.Context, *sql.DB, *curation.Writer, *ModelMatcher, string, string) (curation.ModelException, error)) {
	t.Run("new_candidates", func(t *testing.T) {
		f := newShelvedPendingFixture(t, "release_match")
		ctx := t.Context()
		oldCandidate := sources.Release{ID: uuid.New(), Title: "旧合成候选"}
		f.finder.search = []sources.Release{oldCandidate}
		requirePendingRefreshOK(t, f.worker.walk(ctx, false, nil, false))
		first := f.claim(t, f.releaseID, f.kind)
		requirePendingCandidates(t, first, oldCandidate.ID)
		requirePendingRefreshOK(t, f.writer.ReleaseClaimed(ctx, first.ConversationID, true, nil))
		claimed := f.claim(t, f.releaseID, f.kind)
		const reason = "旧候选证据不足"
		requirePendingRefreshOK(t, f.writer.ShelvePendingItem(ctx, claimed.ID, claimed.ConversationID, reason))
		shelved := f.load(t, first.ID)
		if shelved.Status != "shelved" || shelved.Attempts != 1 || shelved.ShelvedReason == nil || *shelved.ShelvedReason != reason {
			t.Fatalf("搁置后应保留一次未处理次数及搁置原因，得到 %+v", shelved)
		}

		candidate := sources.Release{ID: uuid.New(), Title: "新合成候选"}
		f.finder.search = []sources.Release{candidate}
		f.rejudge(t)
		got := f.pending(t)
		if f.finder.searches != 2 || got.ID != shelved.ID || got.IdentityDigest != shelved.IdentityDigest ||
			got.InputDigest != shelved.InputDigest || got.Status != "waiting_model" || got.Attempts != 0 || got.ShelvedReason != nil {
			t.Fatalf("同摘要下出现新候选应重开原项 %s，清零次数并清空原因，得到状态 %s、摘要 %s、次数 %d、原因 %v",
				shelved.ID, got.Status, got.InputDigest, got.Attempts, got.ShelvedReason)
		}
		requirePendingCandidates(t, got, candidate.ID)
	})

	for _, kind := range []string{"release_match", "recording_match"} {
		t.Run("same_candidates/"+kind, func(t *testing.T) {
			f := newShelvedPendingFixture(t, kind)
			ctx := t.Context()
			firstID, secondID := uuid.New(), uuid.New()
			if secondID < firstID {
				firstID, secondID = secondID, firstID
			}
			f.finder.search = []sources.Release{{ID: secondID, Title: "第二个发行"}, {ID: firstID, Title: "第一个发行"}}
			f.finder.recordingSearch = []sources.Recording{{ID: secondID, Title: "第二段录音"}, {ID: firstID, Title: "第一段录音"}}
			requirePendingRefreshOK(t, f.worker.walk(ctx, false, nil, false))
			claimed := f.claim(t, f.releaseID, kind)
			requirePendingCandidates(t, claimed, firstID, secondID)
			requirePendingRefreshOK(t, f.writer.ShelvePendingItem(ctx, claimed.ID, claimed.ConversationID, "搜索候选不足以确认"))
			shelved := f.load(t, claimed.ID)

			slices.Reverse(f.finder.search)
			slices.Reverse(f.finder.recordingSearch)
			f.rejudge(t)
			if searches := f.finder.searches + f.finder.recordingSearches; searches != 2 {
				t.Fatalf("应实际重新搜索以核对换序，搜索 %d 次", searches)
			}
			if got := f.pending(t); !reflect.DeepEqual(got, shelved) {
				t.Fatalf("外部结果只换序时应保留搁置项，重判前状态 %s、候选 %s、冲突 %s，重判后状态 %s、候选 %s、冲突 %s",
					shelved.Status, shelved.Candidates, shelved.Conflicts, got.Status, got.Candidates, got.Conflicts)
			}

			// 识别输入真正改变后，即使候选集合相同也应重新开放。
			if kind == "release_match" {
				f.exec(t, "UPDATE releases SET title='新专辑标题' WHERE id=$1", f.releaseID)
			} else {
				f.exec(t, "UPDATE tracks SET title='新录音标题' WHERE source_status='present' AND medium_id IN (SELECT id FROM media WHERE release_id=$1)", f.releaseID)
			}
			requirePendingRefreshOK(t, f.worker.walk(ctx, false, nil, false))
			updated := f.pending(t)
			if updated.ID != claimed.ID || updated.Status != "waiting_model" || updated.IdentityDigest == claimed.IdentityDigest ||
				updated.Attempts != 0 || updated.ShelvedReason != nil {
				t.Fatalf("识别输入变化应重开原待定项，原项 %+v，更新后 %+v", shelved, updated)
			}
			requirePendingCandidates(t, updated, firstID, secondID)
		})
	}

	t.Run("declined_exception", func(t *testing.T) {
		f := newShelvedPendingFixture(t, "release_match")
		ctx := t.Context()
		f.finder.search = []sources.Release{f.remote}
		f.finder.completePage = true
		candidateID := f.remote.ID
		requirePendingRefreshOK(t, f.worker.walk(ctx, false, nil, false))
		item := f.claim(t, f.releaseID, f.kind)
		plan, err := f.matcher.Plan(ctx, item, candidateID)
		requirePendingRefreshOK(t, err)
		exceptionID, err := f.writer.CreateException(ctx, curation.ModelException{
			PendingItemID: item.ID, PlanItemID: uuid.New(), Tool: "resolve_release_match", Provider: "synthetic",
			ConversationID: &item.ConversationID,
			Input:          jsonValue(map[string]string{"item_id": item.ID, "external_id": candidateID}),
			Preview:        jsonValue(plan),
			Review: curation.ExceptionReview{ID: uuid.New(), Model: "合成审查模型", Decision: "needs_human_confirmation",
				Source: "model", Reasons: []string{"候选需要人工核对"}},
		})
		requirePendingRefreshOK(t, err)
		decided, err := decline(ctx, f.db, f.writer, f.matcher, exceptionID, f.adminID)
		requirePendingRefreshOK(t, err)
		if decided.Status != "declined" {
			t.Fatalf("人工不采纳后的例外状态为 %s", decided.Status)
		}
		resolved := f.load(t, item.ID)
		if resolved.Status != "resolved" || resolved.Resolution == nil || *resolved.Resolution != "human_declined" {
			t.Fatalf("人工不采纳后的待定项为 %+v", resolved)
		}

		// 人工不采纳写入的不再提出标记会触发程序重判。
		requirePendingRefreshOK(t, f.worker.walk(ctx, false, nil, false))
		states, err := curation.LoadWorkStates(ctx, f.db, "release", []string{f.releaseID})
		requirePendingRefreshOK(t, err)
		if state := states[f.releaseID]; state.Status != "no_usable_candidates" {
			t.Fatalf("唯一候选已被拒绝，工作状态为 %+v", state)
		}
		items, err := f.writer.ClaimPendingItems(ctx, item.ConversationID, f.worker.opts.Now(), 10)
		requirePendingRefreshOK(t, err)
		if len(items) != 0 {
			t.Fatalf("不应再次领取已被拒绝的候选：%+v", items)
		}
		if got := f.load(t, item.ID); !reflect.DeepEqual(got, resolved) {
			t.Fatalf("原待定项应保留人工决定，重判后 %+v", got)
		}
	})
}
