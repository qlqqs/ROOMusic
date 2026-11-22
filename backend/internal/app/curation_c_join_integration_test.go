package app

import (
	"maps"
	"slices"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

const curationCExternalGroup = "fixture-external-group"

func curationCJoinFixture(t *testing.T, externalGroup string) (*curationFixture, curation.JoinRequest, string) {
	t.Helper()
	f := newCurationFixture(t)
	f.enable(t)
	batch := f.batch(t, "program")
	release := curationCLink("release", f.a, "fixture-release")
	release.ExternalGroupID = externalGroup
	req := curationCLinkRequest(f.a, batch, release)
	link := curationCApplyLinks(t, f, req).OperationID
	// 目标组的旧对应可以独立于程序建立，只有发行对应需要操作后态。
	mustExec(t, f.application, `INSERT INTO external_links(id,source,object_type,object_id,external_id,method,confidence)
 VALUES($1,'musicbrainz','release_group',$2,$3,'barcode','high')`, uuid.New(), f.groupB, curationCExternalGroup)
	return f, curation.JoinRequest{ReleaseID: f.a, GroupID: f.groupB, Source: req.Source, BatchID: batch, Key: uuid.New()}, link
}

func TestPostgreSQLCurationCJoinGroup(t *testing.T) {
	for _, tc := range []struct {
		name, externalGroup, objectType, invalidateSQL string
	}{
		{"missing_release_link", curationCExternalGroup, "release",
			`DELETE FROM external_links WHERE object_type=$1 AND object_id=$2`},
		{"missing_group_in_operation", "", "", ""},
		{"missing_link_owner", curationCExternalGroup, "release",
			`UPDATE external_links SET operation_id=NULL WHERE object_type=$1 AND object_id=$2`},
		{"missing_target_link", curationCExternalGroup, "release_group",
			`DELETE FROM external_links WHERE object_type=$1 AND object_id=$2`},
		{"different_target_link", curationCExternalGroup, "release_group",
			`UPDATE external_links SET external_id='other-group' WHERE object_type=$1 AND object_id=$2`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, req, _ := curationCJoinFixture(t, tc.externalGroup)
			if tc.invalidateSQL != "" {
				id := f.a
				if tc.objectType == "release_group" {
					id = f.groupB
				}
				mustExec(t, f.application, tc.invalidateSQL, tc.objectType, id)
			}
			_, revision, operationsBefore := f.state(t, f.a)
			_, err := f.writer.JoinGroup(t.Context(), req)
			curationCRequireProblem(t, err, 409, "no_strong_evidence")
			if group, edit, n := f.state(t, f.a); group != f.groupA || edit != revision || n != operationsBefore {
				t.Fatalf("拒绝归组后组=%s、人工版本=%d、操作数=%d", group, edit, n)
			}
		})
	}

	t.Run("manual_structure", func(t *testing.T) {
		f, req, _ := curationCJoinFixture(t, curationCExternalGroup)
		_, groupC := insertGroupedRelease(t, f.application, f.rootID, groupedRelease{title: "Album C", artist: "Artist C", tracks: []string{"present"}})
		manual, _ := f.join(t, f.a, 1, groupC)
		wantGroup, wantEdit, wantOperations := f.state(t, f.a)
		_, err := f.writer.JoinGroup(t.Context(), req)
		curationCRequireProblem(t, err, 409, "manual_structure")
		if group, edit, n := f.state(t, f.a); group != wantGroup || edit != wantEdit || n != wantOperations {
			t.Fatalf("人工归组被拒后组=%s、人工版本=%d、操作数=%d", group, edit, n)
		}
		f.undo(t, manual)
		if got, err := f.writer.JoinGroup(t.Context(), req); err != nil || got.GroupID != f.groupB {
			t.Fatalf("人工结构撤销后归组=%+v，错误=%v", got, err)
		}
	})

	t.Run("automatic_revert_and_marker", func(t *testing.T) {
		f, req, _ := curationCJoinFixture(t, curationCExternalGroup)
		got, err := f.writer.JoinGroup(t.Context(), req)
		if err != nil || got.ReleaseID != f.a || got.GroupID != f.groupB || got.PreviousGroupID != f.groupA || got.Revision != 2 || !uuid.Valid(got.OperationID) {
			t.Fatalf("强证据归组=%+v，错误=%v", got, err)
		}
		if group, revision, _ := f.state(t, f.a); group != f.groupB || revision != got.Revision {
			t.Fatalf("归组持久状态=(%s,%d)，响应=%+v", group, revision, got)
		}
		record := externalRecord(t, f, got.OperationID, f.a, operations.TypeReleaseGroupChange, req.BatchID, req.Source)
		requireExternalJSON(t, record.After, map[string]string{"action": "join", "group_id": f.groupB, "group_title": "Album B"})
		reverted := f.undo(t, got.OperationID)["operation_id"].(string)
		curationCRequireMarker(t, f, "release", f.a, "group_join", f.groupB, reverted, map[string]string{"group_id": f.groupB})
		group, edit, before := f.state(t, f.a)
		if group != f.groupA || edit != 3 {
			t.Fatalf("归组撤销后组=%s、人工版本=%d，期望 %s、3", group, edit, f.groupA)
		}
		req.Key = uuid.New()
		_, err = f.writer.JoinGroup(t.Context(), req)
		curationCRequireProblem(t, err, 409, "nothing_to_apply")
		if _, _, after := f.state(t, f.a); after != before {
			t.Fatalf("被标记挡住前后操作数=%d→%d", before, after)
		}
	})

	t.Run("manual_revert_has_no_marker", func(t *testing.T) {
		f, req, _ := curationCJoinFixture(t, curationCExternalGroup)
		manual, _ := f.join(t, f.a, 1, f.groupB)
		f.undo(t, manual)
		// 从管家进入的人工归组带有 origin，也要按来源判断。
		change := governance.GroupChangeRequest{ReleaseID: f.a, GroupID: f.groupB, Action: governance.GroupJoin, ExpectedRevision: 3}
		fromChat, err := operations.Run(t.Context(), f.application.database, operations.Spec{
			ActorID: f.admin.id, Type: operations.TypeReleaseGroupChange, Key: uuid.New(),
			Fingerprint: change.Fingerprint(), Origin: &operations.Origin{Source: "chat"},
		}, governance.GroupChangeExecutor(change))
		if err != nil {
			t.Fatal(err)
		}
		f.undo(t, fromChat.OperationID)
		if n := countRows(t, f.application, `SELECT count(*) FROM curation_suppressions WHERE subject='group_join'`); n != 0 {
			t.Fatalf("人工归组撤销后有 %d 条归组标记，期望 0", n)
		}
		if got, err := f.writer.JoinGroup(t.Context(), req); err != nil || got.GroupID != f.groupB {
			t.Fatalf("人工归组撤销后自动归组=%+v，错误=%v", got, err)
		}
	})

	t.Run("provider_revert", func(t *testing.T) {
		f, req, link := curationCJoinFixture(t, curationCExternalGroup)
		unrelated := f.apply(t, f.a, "other-source", "", map[string]any{"label": "保留"})
		got, err := f.writer.JoinGroup(t.Context(), req)
		if err != nil {
			t.Fatal(err)
		}
		result := f.scope(t, curation.Scope{Kind: "provider", Provider: req.Source.Provider}, "", uuid.New(), uuid.New())
		if result.Reverted != 2 || len(result.Skipped) != 0 || result.NextCursor != nil {
			t.Fatalf("按来源撤销=%+v，期望归组和发行对应两条", result)
		}
		for _, id := range []string{got.OperationID, link} {
			record, err := operations.Load(t.Context(), f.application.database, id)
			if err != nil || record.RevertedByID == "" {
				t.Fatalf("按来源未撤销 %s：%+v，错误=%v", id, record, err)
			}
		}
		f.textOwner(t, "label", "保留", unrelated)
		if group, _, _ := f.state(t, f.a); group != f.groupA {
			t.Fatalf("按来源撤销后组=%s，期望 %s", group, f.groupA)
		}
	})
}

func TestPostgreSQLCurationCOnlyEmpty(t *testing.T) {
	f := newCurationFixture(t)
	f.enable(t)
	f.correct(t, f.a, 1, map[string]any{"title": setChange("人工标题"), "year": nullChange})
	old := f.apply(t, f.a, "fixture", "", map[string]any{"label": "自动厂牌", "edition": nil})
	req := autoRequest(f.a, "fixture", "", map[string]any{
		"title": "新标题", "year": 2020, "label": "新厂牌", "edition": "新版本", "barcode": "1234",
	})
	req.OnlyEmpty = true
	got, err := f.writer.Apply(t.Context(), req)
	if err != nil || !uuid.Valid(got.OperationID) || !slices.Equal(got.Written, []string{"barcode"}) || !maps.Equal(got.Skipped,
		map[string]string{"title": "occupied", "year": "occupied", "label": "occupied", "edition": "occupied"}) {
		t.Fatalf("OnlyEmpty=%+v，期望只写条码，人工和自动层的非空值与显式空占位均跳过，错误=%v", got, err)
	}
	f.textOwner(t, "title", "", "")
	f.textOwner(t, "year", "", "")
	f.textOwner(t, "label", "自动厂牌", old)
	f.textOwner(t, "barcode", "1234", got.OperationID)
	if n := countRows(t, f.application, `SELECT count(*) FROM release_auto_values
 WHERE release_id=$1 AND field='edition' AND mode='null' AND operation_id=$2`, f.a, old); n != 1 {
		t.Fatalf("自动设空的原行保留数=%d，期望 1", n)
	}
}
