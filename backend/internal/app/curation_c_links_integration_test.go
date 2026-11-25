package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func curationCLink(kind, id, externalID string) curation.LinkIntent {
	return curation.LinkIntent{ObjectType: kind, ObjectID: id, ExternalID: externalID}
}

func curationCLinkRequest(release, batch string, links ...curation.LinkIntent) curation.LinkRequest {
	return curation.LinkRequest{
		ReleaseID: release, BatchID: batch, Key: uuid.New(), Links: links,
		Source: curation.Source{Kind: "source", Provider: "musicbrainz", Basis: "合成条码匹配"},
		Method: "barcode", Confidence: "high",
		External: curation.ExternalSummary{Title: "合成外部发行", Artist: "合成外部艺人", Date: "2001", Country: "JP"},
	}
}

func curationCApplyLinks(t *testing.T, f *curationFixture, req curation.LinkRequest) curation.LinkApplied {
	t.Helper()
	got, err := f.writer.ApplyLinks(t.Context(), req)
	if err != nil || !uuid.Valid(got.OperationID) || len(got.Written) == 0 {
		t.Fatalf("写入对应=%+v，错误=%v", got, err)
	}
	return got
}

func curationCRequireProblem(t *testing.T, err error, status int, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望 %d %s，实际成功", status, code)
	}
	if gotStatus, gotCode, message := operations.Problem(err); gotStatus != status || gotCode != code {
		t.Fatalf("实际错误 %d %s %q，期望 %d %s", gotStatus, gotCode, message, status, code)
	}
}

func curationCRequireLink(t *testing.T, f *curationFixture, l curation.LinkIntent, owner string) {
	t.Helper()
	got, err := curation.LinksOf(t.Context(), f.application.database, l.ObjectType, []string{l.ObjectID})
	if err != nil {
		t.Fatal(err)
	}
	if owner == "" {
		if len(got) != 0 {
			t.Fatalf("对象 %+v 应没有对应，实际 %+v", l, got)
		}
		return
	}
	want := curation.Link{Source: "musicbrainz", ObjectType: l.ObjectType, ObjectID: l.ObjectID,
		ExternalID: l.ExternalID, Method: "barcode", Confidence: "high", OperationID: owner}
	if !slices.Equal(got, []curation.Link{want}) {
		t.Fatalf("对应=%+v，期望 %+v", got, want)
	}
}

func curationCChanges(req curation.LinkRequest, written []curation.LinkIntent) map[string]any {
	return map[string]any{"source": req.Source.Provider, "method": req.Method, "confidence": req.Confidence,
		"external": req.External, "links": written}
}

func TestPostgreSQLCurationCLinksApply(t *testing.T) {
	f := newCurationFixture(t)
	app := f.application
	release := curationCLink("release", f.a, "fixture-release")
	release.ExternalGroupID = "fixture-group"
	group := curationCLink("release_group", f.groupA, release.ExternalGroupID)
	trackID, _ := externalTrack(t, app, f.a)
	track := curationCLink("track", trackID, "fixture-recording")

	req := curationCLinkRequest(f.a, "", release)
	_, err := f.writer.ApplyLinks(t.Context(), req)
	curationCRequireProblem(t, err, 409, "curation_disabled")
	f.enable(t)
	req.BatchID = f.batch(t, "program")
	// 第一次撤销留下曲目标记，后续请求既跳过该曲目也能建立另外两个对应。
	first := curationCApplyLinks(t, f, curationCLinkRequest(f.a, req.BatchID, track))
	revertExternal(t, f, first.OperationID, f.a, 1)
	req.Links = []curation.LinkIntent{track, group, release}
	wantWritten := []curation.LinkIntent{release, group}
	got := curationCApplyLinks(t, f, req)
	if !slices.Equal(got.Written, wantWritten) || !maps.Equal(got.Skipped, map[string]string{"track:" + trackID: "suppressed"}) {
		t.Fatalf("多项写入=%+v，期望写入 %+v 并跳过曲目", got, wantWritten)
	}
	record := externalRecord(t, f, got.OperationID, f.a, operations.TypeExternalLink, req.BatchID, req.Source)
	if record.ExpectedRevision != 3 || record.ResultRevision != 4 {
		t.Fatalf("多项写入的自动版本=%d→%d，期望 3→4", record.ExpectedRevision, record.ResultRevision)
	}
	requireExternalJSON(t, record.Before, map[string]any{})
	requireExternalJSON(t, record.After, curationCChanges(req, wantWritten))
	for _, link := range wantWritten {
		curationCRequireLink(t, f, link, got.OperationID)
	}
	curationCRequireLink(t, f, track, "")
	if _, edit, _ := f.state(t, f.a); edit != 1 {
		t.Fatalf("人工版本=%d，期望 1", edit)
	}

	req.Links = []curation.LinkIntent{release, group, track}
	replayed := curationCApplyLinks(t, f, req)
	if !reflect.DeepEqual(replayed, got) {
		t.Fatalf("同键重放=%+v，期望 %+v", replayed, got)
	}
	occupied := release
	occupied.ExternalID = "another-release"
	fresh := track
	fresh.ExternalID = "another-recording"
	mixed := curationCApplyLinks(t, f, curationCLinkRequest(f.a, req.BatchID, occupied, group, fresh))
	if !slices.Equal(mixed.Written, []curation.LinkIntent{fresh}) || !maps.Equal(mixed.Skipped, map[string]string{
		"release:" + f.a: "occupied", "release_group:" + f.groupA: "unchanged",
	}) {
		t.Fatalf("已有对应时写入=%+v", mixed)
	}
	curationCRequireLink(t, f, release, got.OperationID)
	curationCRequireLink(t, f, group, got.OperationID)
	curationCRequireLink(t, f, fresh, mixed.OperationID)
	_, err = f.writer.ApplyLinks(t.Context(), curationCLinkRequest(f.a, req.BatchID, occupied, group, track))
	curationCRequireProblem(t, err, 409, "nothing_to_apply")

	foreignTrack, _ := externalTrack(t, app, f.b)
	_, _, before := f.state(t, f.a)
	for _, foreign := range []curation.LinkIntent{
		curationCLink("release", f.b, "foreign-release"),
		curationCLink("release_group", f.groupB, "foreign-group"),
		curationCLink("track", foreignTrack, "foreign-track"),
	} {
		_, err := f.writer.ApplyLinks(t.Context(), curationCLinkRequest(f.a, req.BatchID, release, foreign))
		curationCRequireProblem(t, err, 400, "invalid_input")
		curationCRequireLink(t, f, foreign, "")
	}
	if err := f.writer.CloseBatch(t.Context(), req.BatchID); err != nil {
		t.Fatal(err)
	}
	_, err = f.writer.ApplyLinks(t.Context(), curationCLinkRequest(f.a, req.BatchID, release))
	curationCRequireProblem(t, err, 409, "batch_closed")
	if _, _, after := f.state(t, f.a); after != before {
		t.Fatalf("拒绝写入前后操作数=%d→%d", before, after)
	}
}

func curationCRequireMarker(t *testing.T, f *curationFixture, kind, id, subject, identity, revert string, want any) {
	t.Helper()
	var actor, hash, owner string
	var value json.RawMessage
	err := f.application.database.QueryRow(`SELECT value_hash,value,operation_id::text,created_by::text
 FROM curation_suppressions WHERE target_type=$1 AND target_id=$2 AND subject=$3`, kind, id, subject).Scan(&hash, &value, &owner, &actor)
	if wantHash := sha256.Sum256([]byte(identity)); err != nil || hash != hex.EncodeToString(wantHash[:]) || owner != revert || actor != f.admin.id {
		t.Fatalf("标记 %s/%s=%s %s %s，期望哈希 %x、撤销 %s、操作者 %s，错误=%v",
			kind, subject, hash, owner, actor, wantHash, revert, f.admin.id, err)
	}
	requireExternalJSON(t, value, want)
}

func TestPostgreSQLCurationCLinksRevert(t *testing.T) {
	t.Run("deletes_and_suppresses", func(t *testing.T) {
		f := newCurationFixture(t)
		f.enable(t)
		track, _ := externalTrack(t, f.application, f.a)
		links := []curation.LinkIntent{curationCLink("release", f.a, "fixture-release"), curationCLink("track", track, "fixture-track")}
		op := curationCApplyLinks(t, f, curationCLinkRequest(f.a, "", links...)).OperationID
		revert := revertExternal(t, f, op, f.a, 1)
		for _, l := range links {
			curationCRequireLink(t, f, l, "")
			curationCRequireMarker(t, f, l.ObjectType, l.ObjectID, "external_link", "musicbrainz\x00"+l.ExternalID,
				revert, map[string]string{"source": "musicbrainz", "external_id": l.ExternalID})
		}
		record, err := operations.Load(t.Context(), f.application.database, revert)
		if err != nil || record.ExpectedRevision != 2 || record.ResultRevision != 3 {
			t.Fatalf("撤销的自动版本=%d→%d，期望 2→3，错误=%v", record.ExpectedRevision, record.ResultRevision, err)
		}
	})
	t.Run("changed_owner", func(t *testing.T) {
		f := newCurationFixture(t)
		f.enable(t)
		track, _ := externalTrack(t, f.application, f.a)
		release := curationCLink("release", f.a, "fixture-release")
		recording := curationCLink("track", track, "fixture-track")
		original := curationCApplyLinks(t, f, curationCLinkRequest(f.a, "", release, recording)).OperationID
		replacement := f.apply(t, f.a, "fixture", "", map[string]any{"label": "另一次写入"})
		// 同一外部 ID 换了写入者。曲目排在发行之后，可检查冲突时先处理的行也保留。
		mustExec(t, f.application, `UPDATE external_links SET operation_id=$1 WHERE source='musicbrainz' AND object_type='track' AND object_id=$2`, replacement, track)
		_, _, before := f.state(t, f.a)
		r := f.revert(t, &f.admin, original, uuid.New())
		requireAPIError(t, r, 409, "revert_conflict")
		if !strings.Contains(r.Body.String(), replacement) {
			t.Errorf("冲突未指出改写操作 %s：%s", replacement, r.Body)
		}
		curationCRequireLink(t, f, release, original)
		curationCRequireLink(t, f, recording, replacement)
		if _, _, after := f.state(t, f.a); after != before {
			t.Errorf("冲突撤销前后操作数=%d→%d", before, after)
		}
		if n := countRows(t, f.application, `SELECT count(*) FROM curation_suppressions WHERE subject='external_link'`); n != 0 {
			t.Errorf("冲突撤销留下 %d 条标记", n)
		}
	})
	t.Run("batch", func(t *testing.T) {
		f := newCurationFixture(t)
		f.enable(t)
		batch := f.batch(t, "program")
		a := curationCLink("release", f.a, "fixture-a")
		b := curationCLink("release", f.b, "fixture-b")
		keep := curationCLink("release_group", f.groupA, "fixture-other-batch")
		curationCApplyLinks(t, f, curationCLinkRequest(f.a, batch, a))
		curationCApplyLinks(t, f, curationCLinkRequest(f.b, batch, b))
		outside := curationCApplyLinks(t, f, curationCLinkRequest(f.a, f.batch(t, "model"), keep)).OperationID
		if err := f.writer.CloseBatch(t.Context(), batch); err != nil {
			t.Fatal(err)
		}
		got := f.scope(t, curation.Scope{Kind: "batch", ID: batch}, "", uuid.New(), uuid.New())
		if got.Reverted != 2 || len(got.Skipped) != 0 || got.NextCursor != nil {
			t.Fatalf("按批撤销=%+v，期望仅撤销批次的两条操作", got)
		}
		curationCRequireLink(t, f, a, "")
		curationCRequireLink(t, f, b, "")
		curationCRequireLink(t, f, keep, outside)
	})
}

func TestPostgreSQLCurationCLinksOperations(t *testing.T) {
	f := newCurationFixture(t)
	f.enable(t)
	link := curationCLink("release", f.a, "fixture-release")
	link.ExternalGroupID = "fixture-group"
	group := curationCLink("release_group", f.groupA, link.ExternalGroupID)
	req := curationCLinkRequest(f.a, f.batch(t, "program"), link, group)
	op := curationCApplyLinks(t, f, req).OperationID
	staleLink := curationCLink("release", f.b, "fixture-other-release")
	stale := curationCApplyLinks(t, f, curationCLinkRequest(f.b, req.BatchID, staleLink)).OperationID
	mustExec(t, f.application, `UPDATE external_links SET operation_id=NULL WHERE object_id=$1`, f.b)

	want := curationCChanges(req, []curation.LinkIntent{link, group})
	want["kind"] = operations.TypeExternalLink
	record, err := operations.Load(t.Context(), f.application.database, op)
	if err != nil {
		t.Fatal(err)
	}
	kind := curation.ExternalLinkOperations(f.writer)
	described, err := kind.Describe(t.Context(), f.application.database, []operations.Record{record})
	description := described[op]
	if err != nil || !description.Revertible || description.Release == nil || description.Release.ID != f.a {
		t.Fatalf("Describe=%+v，错误=%v", described, err)
	}
	raw, err := json.Marshal(description.Changes)
	if err != nil {
		t.Fatal(err)
	}
	requireExternalJSON(t, raw, want)
	preview, err := kind.(operations.RevertPreviewer).PreviewRevert(t.Context(), f.application.database, record)
	if err != nil || len(preview) != 2 {
		t.Fatalf("撤销预览=%+v，错误=%v", preview, err)
	}
	for i, l := range []curation.LinkIntent{link, group} {
		raw, err := json.Marshal(preview[i].Before)
		if err != nil || preview[i].Field != l.ObjectType+":"+l.ObjectID || preview[i].After != nil {
			t.Fatalf("撤销预览项=%+v，错误=%v", preview[i], err)
		}
		requireExternalJSON(t, raw, map[string]string{"source": req.Source.Provider, "external_id": l.ExternalID})
	}

	type item struct {
		ID, Type   string
		Revertible bool
		Reverts    string
		RevertedBy string `json:"reverted_by"`
		Changes    json.RawMessage
	}
	list := func(wantCount int) map[string]item {
		t.Helper()
		var response struct{ Items []item }
		getJSON(t, f.application, &f.admin, "/api/v1/operations", &response)
		if len(response.Items) != wantCount {
			t.Fatalf("操作列表=%+v，期望 %d 项", response.Items, wantCount)
		}
		got := make(map[string]item, wantCount)
		for _, row := range response.Items {
			got[row.ID] = row
		}
		return got
	}
	items := list(2)
	if row := items[op]; row.Type != operations.TypeExternalLink || !row.Revertible || row.Reverts != "" || row.RevertedBy != "" {
		t.Fatalf("可撤销操作=%+v", row)
	}
	requireExternalJSON(t, items[op].Changes, want)
	if row := items[stale]; row.Type != operations.TypeExternalLink || row.Revertible {
		t.Fatalf("已换写入者的操作=%+v", row)
	}
	reverted := revertExternal(t, f, op, f.a, 1)
	items = list(3)
	if row := items[op]; row.Revertible || row.RevertedBy != reverted {
		t.Fatalf("已撤销的原操作=%+v", row)
	}
	if row := items[reverted]; row.Type != operations.TypeRevert || row.Revertible || row.Reverts != op {
		t.Fatalf("撤销记录=%+v", row)
	}
	requireExternalJSON(t, items[op].Changes, want)
	requireExternalJSON(t, items[reverted].Changes, want)
}

func recordCurationCLinkSamples(t *testing.T, samples *apiSampleRecorder) {
	t.Helper()
	f := newCurationFixture(t)
	f.enable(t)
	app := f.application
	mustExec(t, app, "UPDATE users SET username='curator' WHERE id=$1", f.admin.id)
	samples.forbid(f.admin.token)
	samples.forbid(app.allowedRoot)
	samples.forbid(app.dataDirectory)
	release := curationCLink("release", f.a, "fixture-release")
	release.ExternalGroupID = "fixture-group"
	group := curationCLink("release_group", f.groupA, release.ExternalGroupID)
	op := curationCApplyLinks(t, f, curationCLinkRequest(f.a, f.batch(t, "program"), release, group)).OperationID
	samples.record("operations/link-reverted", f.revert(t, &f.admin, op, uuid.New()), 200)
	groupMatch := curationCLink("release_group_match", f.a, "fixture-group-only")
	groupRequest := curationCLinkRequest(f.a, "", groupMatch)
	groupRequest.Method = "search"
	groupRequest.Source.Basis = "合成发行组匹配"
	curationCApplyLinks(t, f, groupRequest)
	writeAutoInvalidationSampleOperations(t, f)
	samples.record("operations/link-list", app.request(t, "GET", "/api/v1/operations?release_id="+f.a, nil, &f.admin, nil), 200)
}
