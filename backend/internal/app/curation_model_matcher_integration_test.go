package app

import (
	"slices"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/curation/program"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
)

func requireCurationOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func modelConversation(t *testing.T, f *curationFixture) string {
	t.Helper()
	c, err := runtime.NewStore(f.application.database).CreateCurationConversation(t.Context(), f.admin.id, "合成自动整理", "synthetic")
	requireCurationOK(t, err)
	return c.ID
}

func modelPending(t *testing.T, f *curationFixture, target, kind, digest string) curation.PendingItem {
	t.Helper()
	db := f.application.database
	item := curation.PendingItem{TargetID: target, Kind: kind, InputDigest: digest, RuleVersion: program.StoredRuleVersion,
		Evidence: []byte(`{"title":"合成资料"}`), Candidates: []byte(`[{"title":"外部资料"}]`), Conflicts: []byte(`["search_only"]`)}
	requireCurationOK(t, curation.UpsertPendingItem(t.Context(), db, item))
	id := programString(t, f.application, `SELECT id FROM curation_pending_items WHERE target_id=$1 AND kind=$2 AND status<>'resolved'`, target, kind)
	item, err := f.writer.PendingItem(t.Context(), id)
	requireCurationOK(t, err)
	return item
}

func modelDigest(t *testing.T, f *programFixture) string {
	t.Helper()
	evidence, err := catalog.LoadCurationEvidence(t.Context(), f.application.database, []string{f.a}, f.writer.ArtworkExists)
	requireCurationOK(t, err)
	if evidence[f.a].ID != f.a {
		t.Fatalf("没有找到可见发行 %s", f.a)
	}
	return program.ReleaseDigest(evidence[f.a])
}

func modelClaim(t *testing.T, f *curationFixture, conversation string, want curation.PendingItem) curation.PendingItem {
	t.Helper()
	items, err := f.writer.ClaimPendingItems(t.Context(), conversation, time.Now(), 1)
	requireCurationOK(t, err)
	if len(items) != 1 || items[0].ID != want.ID {
		t.Fatalf("领取=%+v，期望 %s", items, want.ID)
	}
	return items[0]
}

func modelSuppressLink(t *testing.T, f *programFixture, objectType, objectID, externalID string) {
	t.Helper()
	tx, err := f.application.database.BeginTx(t.Context(), nil)
	requireCurationOK(t, err)
	defer tx.Rollback()
	requireCurationOK(t, curation.SuppressExternalLink(t.Context(), tx, objectType, objectID, "musicbrainz", externalID, "", f.admin.id))
	requireCurationOK(t, tx.Commit())
}

func TestPostgreSQLCurationModelPlan(t *testing.T) {
	for _, code := range []string{"track_count", "media_count_mismatch", "existing_link", "suppressed", "item_stale"} {
		t.Run(code, func(t *testing.T) {
			f := newProgramFixture(t)
			f.enable(t)
			remote := programAlbum("Album A", "Artist A")
			switch code {
			case "track_count":
				remote.Media[0].Tracks = append(remote.Media[0].Tracks,
					sources.Track{Position: 2, ID: uuid.New(), RecordingID: uuid.New()})
			case "media_count_mismatch":
				remote.Media = append(remote.Media, sources.Medium{Position: 2})
			case "existing_link":
				curationCApplyLinks(t, f.curationFixture, curationCLinkRequest(f.a, "", curationCLink("release", f.a, uuid.New())))
			case "suppressed":
				modelSuppressLink(t, f, "release", f.a, remote.ID)
			}
			f.source.releases[remote.ID] = remote
			item := modelPending(t, f.curationFixture, f.a, "release_match", modelDigest(t, f))
			if code == "item_stale" {
				mustExec(t, f.application, `UPDATE releases SET barcode='12345' WHERE id=$1`, f.a)
			}
			_, _, before := f.state(t, f.a)
			matcher := program.NewModelMatcher(f.application.database, f.writer, f.gate)
			_, err := matcher.Plan(t.Context(), item, remote.ID)
			curationCRequireProblem(t, err, 409, code)
			_, _, after := f.state(t, f.a)
			if before != after {
				t.Fatalf("预览前后操作数 %d → %d", before, after)
			}
		})
	}
}

func TestPostgreSQLCurationModelApply(t *testing.T) {
	f := newProgramFixture(t)
	f.enable(t)
	remote := programAlbum("Album A", "Artist A")
	f.source.releases[remote.ID] = remote
	_, otherGroup := insertGroupedRelease(t, f.application, f.rootID,
		groupedRelease{title: "已有版本", artist: "Artist A", tracks: []string{"present"}})
	other := programString(t, f.application, `SELECT id FROM releases WHERE group_id=$1`, otherGroup)
	curationCApplyLinks(t, f.curationFixture, curationCLinkRequest(other, "", curationCLink("release_group", otherGroup, remote.Group.ID)))
	mustExec(t, f.application, `UPDATE releases SET label='扫描厂牌' WHERE id=$1`, f.a)
	conversation := modelConversation(t, f.curationFixture)
	item := modelClaim(t, f.curationFixture, conversation, modelPending(t, f.curationFixture, f.a, "release_match", modelDigest(t, f)))
	ctx := t.Context()
	db := f.application.database
	matcher := program.NewModelMatcher(db, f.writer, f.gate)
	plan, err := matcher.Plan(ctx, item, remote.ID)
	requireCurationOK(t, err)
	model := curation.NormalizeProvider("ACME/Model: X")
	batch := f.batch(t, "model")
	opt := program.ApplyOptions{Key: uuid.New(), Batch: batch, PendingItemID: item.ID,
		Source: curation.Source{Kind: "model", Provider: model, Basis: "合成比对依据"}}
	got, err := matcher.Apply(ctx, plan, opt)
	requireCurationOK(t, err)
	if len(got.OperationIDs) != 3 || got.OperationID != got.OperationIDs[0] {
		t.Fatalf("匹配操作=%+v", got)
	}
	for i, kind := range []string{operations.TypeExternalLink, "release_auto_correction", "release_group_change"} {
		f.assertCount(t, 1, `SELECT count(*) FROM operations WHERE id=$1 AND operation_type=$2 AND batch_id=$3
			AND origin->>'provider_kind'='model' AND origin->>'provider'='acme-model--x' AND origin->>'conversation_id'=$4`,
			got.OperationIDs[i], kind, batch, item.ConversationID)
	}
	track, _ := externalTrack(t, f.application, f.a)
	f.assertCount(t, 1, `SELECT count(*) FROM external_links WHERE object_type='release' AND object_id=$1 AND source='musicbrainz' AND external_id=$2`, f.a, remote.ID)
	f.assertCount(t, 1, `SELECT count(*) FROM external_links WHERE object_type='track' AND object_id=$1 AND source='musicbrainz' AND external_id=$2`, track, remote.Media[0].Tracks[0].RecordingID)
	f.assertCount(t, 1, `SELECT count(*) FROM release_auto_values WHERE release_id=$1 AND field='barcode' AND text_value=$2`, f.a, remote.Barcode)
	f.assertCount(t, 0, `SELECT count(*) FROM release_auto_values WHERE release_id=$1 AND field='label'`, f.a)
	f.assertCount(t, 1, `SELECT count(*) FROM releases WHERE id=$1 AND group_id=$2`, f.a, otherGroup)

	state := f.assertRelease(t, f.a, "matched", "model")
	if digest := modelDigest(t, f); state.InputDigest != digest || state.InputDigest == item.InputDigest {
		t.Fatalf("工作摘要=%s，当前证据摘要=%s，写入前=%s", state.InputDigest, digest, item.InputDigest)
	}
	f.assertCount(t, 1, `SELECT count(*) FROM curation_pending_items WHERE id=$1 AND status='resolved' AND resolution='model_matched'`, item.ID)
	replay, err := matcher.Apply(ctx, plan, opt)
	requireCurationOK(t, err)
	if replay.OperationID != got.OperationID || !slices.Equal(replay.OperationIDs, got.OperationIDs) {
		t.Fatalf("重放=%+v，首次=%+v", replay, got)
	}
	f.assertCount(t, 3, `SELECT count(*) FROM operations WHERE batch_id=$1`, batch)
	f.run(t)
	if next := f.assertRelease(t, f.a, "matched", "model"); next.Attempts != state.Attempts || next.Generation != state.Generation {
		t.Fatalf("程序重跑前后的工作状态为 %+v 和 %+v", state, next)
	}
	f.undo(t, got.OperationID)
	f.assertCount(t, 0, `SELECT count(*) FROM external_links WHERE operation_id=$1`, got.OperationID)
	requireCurationOK(t, f.writer.CloseBatch(ctx, batch))
	result := f.scope(t, curation.Scope{Kind: "batch", ID: batch}, "", uuid.New(), uuid.New())
	if result.Reverted != 2 || len(result.Skipped) != 0 {
		t.Fatalf("按批撤销=%+v，期望撤销余下两条", result)
	}
	f.assertCount(t, 1, `SELECT count(*) FROM releases WHERE id=$1 AND group_id=$2`, f.a, f.groupA)
	f.assertCount(t, 0, `SELECT count(*) FROM release_auto_values WHERE operation_id=$1`, got.OperationIDs[1])
}

func TestPostgreSQLCurationModelRecording(t *testing.T) {
	f := newProgramFixture(t)
	f.enable(t)
	ctx := t.Context()
	recording := sources.Recording{ID: uuid.New(), Title: "合成单曲"}
	f.source.recordings[recording.ID] = recording
	conversation := modelConversation(t, f.curationFixture)
	item := modelClaim(t, f.curationFixture, conversation, modelPending(t, f.curationFixture, f.a, "recording_match", modelDigest(t, f)))
	matcher := program.NewModelMatcher(f.application.database, f.writer, f.gate)
	plan, err := matcher.Plan(ctx, item, recording.ID)
	requireCurationOK(t, err)

	// 模拟在计划生成后，同一个录音对应已由前一轮写入。
	track, _ := externalTrack(t, f.application, f.a)
	link := curationCLink("track", track, recording.ID)
	original := curationCApplyLinks(t, f.curationFixture, curationCLinkRequest(f.a, "", link))
	_, err = matcher.Apply(ctx, plan, program.ApplyOptions{Key: uuid.New(), Batch: f.batch(t, "model"),
		Source: curation.Source{Kind: "model", Provider: "synthetic", Basis: "合成录音依据"}})
	requireCurationOK(t, err)
	curationCRequireLink(t, f.curationFixture, link, original.OperationID)
	f.assertCount(t, 0, `SELECT count(*) FROM external_links WHERE object_type='release' AND object_id=$1`, f.a)
	f.assertCount(t, 1, `SELECT count(*) FROM curation_pending_items WHERE id=$1 AND status='resolved' AND resolution='model_matched'`, item.ID)
	f.assertRelease(t, f.a, "matched", "model")
}
