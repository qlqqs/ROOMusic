package app

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/curation/program"
	curationtools "github.com/qlqq/roomusic/backend/internal/curation/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

const releaseMergeProposalTool = "merge_releases"

func seedReleaseMergePair(t *testing.T, f *curationFixture) (string, curation.PendingItem) {
	t.Helper()
	external := uuid.New()
	mustExec(t, f.application, `UPDATE tracks SET disc_number=2 WHERE medium_id IN (SELECT id FROM media WHERE release_id=$1)`, f.b)
	seedReleaseMergeLink(t, f, f.a, external, uuid.New(), 2, 2)
	seedReleaseMergeLink(t, f, f.b, external, uuid.New(), 0, 0)
	discoverReleaseMerge(t, f, external)
	return external, requireReleaseMergeItem(t, f, f.a, f.b, external, false)
}

func releaseMergeProposal(item string) map[string]any {
	return map[string]any{"item_id": item, "basis": map[string]string{"note": "两张发行对应不同录音"}}
}

func startReleaseMergeModel(t *testing.T, f *schedulerFixture) (string, curation.PendingItem) {
	t.Helper()
	f.enable(t)
	external, item := seedReleaseMergePair(t, f.curationFixture)
	f.configure(t, &f.admin)
	return external, item
}

func approveReleaseMerge(t *testing.T, f *schedulerFixture, item curation.PendingItem) (op, batch string) {
	t.Helper()
	f.next(t, false).tool(t, releaseMergeProposalTool, releaseMergeProposal(item.ID))
	f.next(t, true).review(t, "approved")
	last := f.next(t, false)
	if result := last.lastToolResult(t); !strings.Contains(result, "operation_id") {
		t.Fatalf("模型合并结果 %s", result)
	}
	last.complete(t)
	requireReleaseMergeResolution(t, f.curationFixture, item.ID, "model_merged")
	op = programString(t, f.application, `SELECT operation_id FROM release_merges WHERE source_release_id=$1 AND target_release_id=$2`, f.b, f.a)
	batch = programString(t, f.application, `SELECT batch_id FROM operations WHERE id=$1`, op)
	schedulerWait(t, "关闭发行合并的模型批次", func() bool {
		return countRows(t, f.application, `SELECT count(*) FROM curation_batches WHERE id=$1 AND closed_at IS NOT NULL`, batch) == 1
	})
	return op, batch
}

func TestPostgreSQLCurationModelMergeReleases(t *testing.T) {
	t.Run("模型提出并经审查写入", func(t *testing.T) {
		f := newSchedulerFixture(t)
		want := []string{"correct_release", "correct_track", "get_music_metadata", "merge_releases", "read_pending_items",
			"resolve_release_match", "search_music_metadata", "shelve_pending_item"}
		for _, name := range want {
			if !backgroundToolAllowed(name) {
				t.Errorf("后台白名单缺少 %s", name)
			}
		}
		var dedicated []string
		for _, tool := range curationtools.StewardTools(f.writer, nil) {
			dedicated = append(dedicated, tool.Name)
		}
		slices.Sort(dedicated)
		if !slices.Equal(dedicated, []string{"merge_releases", "read_pending_items", "resolve_release_match", "shelve_pending_item"}) {
			t.Fatalf("后台专用工具 %v", dedicated)
		}
		_, item := startReleaseMergeModel(t, f)
		op, batch := approveReleaseMerge(t, f, item)
		b3RequireCount(t, f.application, 1, `SELECT count(*) FROM operations o JOIN curation_batches b ON b.id=o.batch_id
			JOIN steward_reviews r ON r.id::text=o.origin->>'review_id'
			WHERE o.id=$1 AND b.id=$2 AND b.tier='model' AND b.actor_id=$3 AND o.actor_id=$3 AND o.target_id=$4
			AND o.operation_type='release_auto_merge' AND o.origin->>'source'='curation' AND o.origin->>'provider_kind'='model'
			AND o.origin->>'provider'='test-main' AND o.origin->>'review_decision'='approved' AND r.policy_version=4`, op, batch, f.admin.id, f.a)
		b3RequireCount(t, f.application, 2, `SELECT count(*) FROM operations_release_targets WHERE operation_id=$1 AND release_id IN ($2,$3)`, op, f.a, f.b)
		b3RequireCount(t, f.application, 0, `SELECT count(*) FROM curation_model_exceptions`)
	})
	t.Run("人工合并结束涉及目标或候选的待定项", func(t *testing.T) {
		f := newCurationFixture(t)
		f.enable(t)
		_, first := seedReleaseMergePair(t, f)
		third, _ := insertGroupedRelease(t, f.application, f.rootID, groupedRelease{title: "其他候选目标", artist: "Artist A", tracks: []string{"present"}})
		second := modelPending(t, f, third, "release_merge", "合成待定项")
		candidate, err := json.Marshal([]curation.MergeCandidate{{ReleaseID: f.b, Title: "Album B", DiscCount: 1, TrackCount: 1}})
		requireCurationOK(t, err)
		mustExec(t, f.application, `UPDATE curation_pending_items SET candidates=$2 WHERE id=$1`, second.ID, candidate)
		manualReleaseMerge(t, f, f.a, f.b)
		requireReleaseMergeResolution(t, f, first.ID, "merged")
		requireReleaseMergeResolution(t, f, second.ID, "merged")
	})
}

func TestPostgreSQLCurationMergeBlocked(t *testing.T) {
	for _, tc := range []struct{ name, code string }{
		{"录音重叠", "no_strong_evidence"},
		{"无发行对应", "no_strong_evidence"},
		{"对应没有操作 ID", "no_strong_evidence"},
		{"无对应操作", "no_strong_evidence"},
		{"对应操作已撤销", "no_strong_evidence"},
		{"不再提出标记", "nothing_to_apply"},
		{"人工分组", "manual_structure"},
		{"链式", "merge_chain"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCurationFixture(t)
			f.enable(t)
			external, item := seedReleaseMergePair(t, f)
			conversation := modelConversation(t, f)
			item = modelClaim(t, f, conversation, item)
			batch := f.batch(t, "model")
			preview, err := f.writer.PreviewReleaseMerge(t.Context(), item)
			requireCurationOK(t, err)
			ctx := curation.WithModelRun(t.Context(), &curation.ModelRun{ConversationID: conversation, Model: "test-model", ActorID: f.admin.id, PendingItemID: item.ID})
			switch tc.name {
			case "录音重叠":
				left, _ := externalTrack(t, f.application, f.a)
				right, _ := externalTrack(t, f.application, f.b)
				mustExec(t, f.application, `UPDATE external_links SET external_id=(SELECT external_id FROM external_links WHERE source='musicbrainz' AND object_type='track' AND object_id=$1)
					WHERE source='musicbrainz' AND object_type='track' AND object_id=$2`, left, right)
			case "无发行对应":
				mustExec(t, f.application, `DELETE FROM external_links WHERE object_type='release' AND object_id=$1`, f.b)
			case "对应没有操作 ID":
				mustExec(t, f.application, `UPDATE external_links SET operation_id=NULL WHERE object_type='release' AND object_id=$1`, f.b)
			case "无对应操作":
				// 保留有效对应行，以隔离对操作类型和后态的校验。
				mustExec(t, f.application, `UPDATE operations SET after_state=after_state-'links' WHERE id=(SELECT operation_id FROM external_links WHERE object_type='release' AND object_id=$1)`, f.b)
			case "对应操作已撤销":
				linkOp := programString(t, f.application, `SELECT operation_id FROM external_links WHERE object_type='release' AND object_id=$1`, f.b)
				links, err := curation.LinksOf(t.Context(), f.application.database, "release", []string{f.b})
				requireCurationOK(t, err)
				track, _ := externalTrack(t, f.application, f.b)
				recording := programString(t, f.application, `SELECT external_id FROM external_links WHERE object_type='track' AND object_id=$1`, track)
				f.undo(t, linkOp)
				link := links[0]
				mustExec(t, f.application, `INSERT INTO external_links(id,source,object_type,object_id,external_id,method,confidence,operation_id)
					VALUES($1,'musicbrainz','release',$2,$3,$4,$5,$6)`, uuid.New(), f.b, external, link.Method, link.Confidence, linkOp)
				mustExec(t, f.application, `INSERT INTO external_links(id,source,object_type,object_id,external_id,method,confidence,operation_id)
					VALUES($1,'musicbrainz','track',$2,$3,$4,$5,$6)`, uuid.New(), track, recording, link.Method, link.Confidence, linkOp)
			case "不再提出标记":
				tx, err := f.application.database.BeginTx(t.Context(), nil)
				requireCurationOK(t, err)
				defer tx.Rollback()
				requireCurationOK(t, f.writer.SuppressReleaseMerge(t.Context(), tx, f.b, f.a, "", f.admin.id))
				requireCurationOK(t, tx.Commit())
			case "人工分组":
				_, group := insertGroupedRelease(t, f.application, f.rootID, groupedRelease{title: "人工选定专辑", artist: "Artist B", tracks: []string{"present"}})
				f.join(t, f.b, 1, group)
			case "链式":
				third, _ := insertGroupedRelease(t, f.application, f.rootID, groupedRelease{title: "被并入的第三碟", artist: "Artist B", tracks: []string{"present"}})
				mustExec(t, f.application, `UPDATE tracks SET disc_number=3 WHERE medium_id IN (SELECT id FROM media WHERE release_id=$1)`, third)
				manualReleaseMerge(t, f, f.b, third)
			}
			_, err = f.writer.MergeReleases(ctx, curation.ReleaseAutoMerge{ItemID: item.ID, Token: preview.Token, BatchID: batch,
				Key: uuid.New(), Source: curation.Source{Kind: "model", Provider: "test-model", Basis: "合成并入依据"}})
			curationCRequireProblem(t, err, 409, tc.code)
			discoverReleaseMerge(t, f, external)
			b3RequireCount(t, f.application, 0, `SELECT count(*) FROM curation_pending_items WHERE kind='release_merge' AND status<>'resolved'`)
			b3RequireCount(t, f.application, 0, `SELECT count(*) FROM operations WHERE operation_type='release_auto_merge'`)
		})
	}
}

func TestPostgreSQLCurationMergeRevert(t *testing.T) {
	for _, route := range []string{"batch", "provider", "skipped"} {
		t.Run(route, func(t *testing.T) {
			f := newSchedulerFixture(t)
			external, item := startReleaseMergeModel(t, f)
			op, batch := approveReleaseMerge(t, f, item)
			scope := curation.Scope{Kind: "batch", ID: batch}
			switch route {
			case "provider":
				scope = curation.Scope{Kind: "provider", Provider: "test-main"}
			case "skipped":
				_, group := insertGroupedRelease(t, f.application, f.rootID, groupedRelease{title: "人工归入的专辑", artist: "Artist A", tracks: []string{"present"}})
				f.join(t, f.a, 1, group)
			}
			got := f.scope(t, scope, "", uuid.New(), uuid.New())
			if route == "skipped" {
				if got.Reverted != 0 || !slices.Equal(got.Skipped, []curation.SkippedRevert{{OperationID: op, Code: "revert_conflict"}}) {
					t.Fatalf("人工归组后的撤销 %+v", got)
				}
				b3RequireCount(t, f.application, 1, `SELECT count(*) FROM release_merges WHERE operation_id=$1`, op)
				b3RequireCount(t, f.application, 0, `SELECT count(*) FROM curation_suppressions WHERE subject='release_merge'`)
				return
			}
			if got.Reverted != 1 || len(got.Skipped) != 0 {
				t.Fatalf("范围撤销 %+v", got)
			}
			b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_suppressions s JOIN operations undo ON undo.id=s.operation_id
				WHERE undo.reverts_operation_id=$1 AND s.subject='release_merge' AND s.target_type='release'
				AND s.target_id=$2 AND s.value->>'release_id'=$3 AND s.created_by=$4`, op, min(f.a, f.b), max(f.a, f.b), f.admin.id)
			b3RequireCount(t, f.application, 0, `SELECT count(*) FROM release_merges`)
			b3RequireCount(t, f.application, 2, `SELECT count(*) FROM external_links WHERE object_type='release' AND external_id=$1`, external)
			discoverReleaseMerge(t, f.curationFixture, external)
			b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_pending_items WHERE kind='release_merge'`)
		})
	}
}

func TestPostgreSQLCurationExceptionMergeAcceptDecline(t *testing.T) {
	for _, action := range []string{"accept", "stale", "decline"} {
		t.Run(action, func(t *testing.T) {
			f := newSchedulerFixture(t)
			external, item := startReleaseMergeModel(t, f)
			reviewer := f.application.createUser(t, "admin")
			ex := f.proposeException(t, releaseMergeProposalTool, releaseMergeProposal(item.ID))
			f.pause(t)
			switch action {
			case "accept":
				e := readExceptionDecision(t, f.decide(t, &reviewer, ex.ID, "accept"), "accepted", reviewer.id)
				b3RequireCount(t, f.application, 1, `SELECT count(*) FROM operations o JOIN release_merges m ON m.operation_id=o.id
					WHERE o.id=$1 AND o.actor_id=$2 AND o.batch_id IS NULL AND o.operation_type='release_auto_merge'
					AND o.origin->>'provider_kind'='model' AND o.origin->>'provider'='test-main' AND o.origin->>'review_decision'='human'
					AND o.origin->>'review_id'=$3 AND m.target_release_id=$4 AND m.source_release_id=$5`, e.OperationIDs[0], reviewer.id, ex.Review.ID, f.a, f.b)
				requireReleaseMergeResolution(t, f.curationFixture, item.ID, "human_accepted")
			case "stale":
				before, err := f.writer.PreviewReleaseMerge(t.Context(), item)
				requireCurationOK(t, err)
				// 扫描改变来源碟的原位置，触发重算令牌。
				mustExec(t, f.application, `UPDATE media SET position=position+1 WHERE release_id=$1`, f.b)
				after, err := f.writer.PreviewReleaseMerge(t.Context(), item)
				requireCurationOK(t, err)
				if before.Token == after.Token {
					t.Fatal("来源碟序变化后预览令牌未改变")
				}
				requireAPIError(t, f.decide(t, &reviewer, ex.ID, "accept"), 409, "exception_stale")
				b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_model_exceptions WHERE id=$1 AND status='open' AND decided_by IS NULL`, ex.ID)
				b3RequireCount(t, f.application, 0, `SELECT count(*) FROM release_merges`)
			case "decline":
				readExceptionDecision(t, f.decide(t, &reviewer, ex.ID, "decline"), "declined", reviewer.id)
				requireReleaseMergeResolution(t, f.curationFixture, item.ID, "human_declined")
				b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_suppressions WHERE subject='release_merge'
					AND target_type='release' AND target_id=$1 AND value->>'release_id'=$2 AND created_by=$3`, min(f.a, f.b), max(f.a, f.b), reviewer.id)
				discoverReleaseMerge(t, f.curationFixture, external)
				b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_pending_items WHERE kind='release_merge'`)
			}
		})
	}
}

func recordReleaseMergeCurationSamples(t *testing.T, samples *apiSampleRecorder) {
	t.Helper()
	f := newCurationFixture(t)
	samples.forbid(f.admin.token)
	samples.forbid(f.application.allowedRoot)
	samples.forbid(f.application.dataDirectory)
	f.enable(t)
	// 固定两个发行的相对 ID，标记始终展示同一方的标题。
	f.a, _ = insertGroupedRelease(t, f.application, f.rootID, groupedRelease{
		id: "00000000-0000-4000-8000-000000000100", title: "合成第一碟", artist: "合成艺人", tracks: []string{"present"},
	})
	f.b, _ = insertGroupedRelease(t, f.application, f.rootID, groupedRelease{
		id: "00000000-0000-4000-8000-000000000200", title: "合成第二碟", artist: "合成艺人", tracks: []string{"present"},
	})
	conversation := modelConversation(t, f)
	_, item := seedReleaseMergePair(t, f)
	mustExec(t, f.application, `UPDATE curation_pending_items SET status='in_progress',conversation_id=$2,claimed_at=now() WHERE id=$1`, item.ID, conversation)
	preview, err := governance.NewStore(f.application.database).PreviewReleaseMerge(t.Context(), f.a, f.b)
	requireCurationOK(t, err)
	raw, err := json.Marshal(map[string]any{"changes": program.ReleaseMergeChanges(preview)})
	requireCurationOK(t, err)
	input, err := json.Marshal(releaseMergeProposal(item.ID))
	requireCurationOK(t, err)
	id, err := f.writer.CreateException(t.Context(), curation.ModelException{
		PendingItemID: item.ID, PlanItemID: uuid.New(), ConversationID: &conversation, Tool: releaseMergeProposalTool,
		Input: input, Preview: raw, Provider: "sample-model",
		Review: curation.ExceptionReview{ID: uuid.New(), Model: "sample-review", Decision: "needs_human_confirmation", Source: "model", Reasons: []string{"并入发行需要人工核对"}},
	})
	requireCurationOK(t, err)
	samples.record("curation/exceptions-merge", f.application.request(t, "GET", curationBase+"/exceptions?status=open", nil, &f.admin, nil), 200)
	response := f.application.request(t, "POST", curationBase+"/exceptions/"+id+"/decline", nil, &f.admin, nil)
	readExceptionDecision(t, response, "declined", f.admin.id)
	samples.record("curation/suppressions-release-merge", f.application.request(t, "GET", curationBase+"/suppressions?release_id="+f.a, nil, &f.admin, nil), 200)
}
