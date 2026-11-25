package app

import (
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/operations"
)

func scanAutoLayerTags(t *testing.T, f *curationFixture, album, title string) {
	t.Helper()
	path := filepath.Join(f.application.allowedRoot, "自动层重扫", "01.flac")
	writeSyntheticTrack(t, path, 73, "ALBUM="+album, "ALBUMARTIST=合成艺人", "ARTIST=合成艺人", "TITLE="+title, "TRACKNUMBER=1")
	runScan(t, f.application, &f.admin)
}

func requireAutoLayerTitles(t *testing.T, f *curationFixture, release, track, album, title string) {
	t.Helper()
	var detail catalog.ReleaseDetail
	getJSON(t, f.application, &f.ordinary, "/api/v1/releases/"+release, &detail)
	if detail.Title != album || len(detail.Media) != 1 || len(detail.Media[0].Tracks) != 1 {
		t.Fatalf("发行标题 %q，介质 %+v，期望标题 %q 和一首曲目", detail.Title, detail.Media, album)
	}
	if got := detail.Media[0].Tracks[0]; got.ID != track || got.Title != title {
		t.Fatalf("曲目 %s 的标题 %q，期望 %s 的标题 %q", got.ID, got.Title, track, title)
	}
}

func TestPostgreSQL自动值随标签重扫作废且可撤销(t *testing.T) {
	f := newCurationFixture(t)
	f.enable(t)
	app := f.application
	scanAutoLayerTags(t, f, "原发行标题", "原曲目标题")
	release := programString(t, app, `SELECT id FROM releases WHERE relative_directory='自动层重扫'`)
	track, _ := externalTrack(t, app, release)
	releaseOp := f.apply(t, release, "fixture", "", map[string]any{"title": "自动发行标题"})
	trackOp := applyTrackAuto(t, f, trackAutoRequest(track, release, "fixture", "", map[string]any{"title": "自动曲目标题"}))
	requireAutoLayerTitles(t, f, release, track, "自动发行标题", "自动曲目标题")

	type operationItem struct {
		ID         string
		Type       string
		Changes    json.RawMessage
		Revertible bool
		Reverts    *string
	}
	list := func(want int) []operationItem {
		t.Helper()
		var page struct{ Items []operationItem }
		getJSON(t, app, &f.admin, "/api/v1/operations?provider=scan&release_id="+release, &page)
		if len(page.Items) != want {
			t.Fatalf("扫描作废记录有 %d 条，期望 %d 条", len(page.Items), want)
		}
		return page.Items
	}
	readEvidence := func() {
		t.Helper()
		_, err := f.writer.LoadCurationEvidence(t.Context(), []string{release})
		requireCurationOK(t, err)
	}
	readEvidence()
	list(0)
	scanAutoLayerTags(t, f, "新发行标题", "新曲目标题")
	// 重扫写入基础表，整理证据读取复核依据并登记作废。
	readEvidence()
	requireAutoLayerTitles(t, f, release, track, "新发行标题", "新曲目标题")
	b3RequireCount(t, app, 0, `SELECT count(*) FROM release_auto_values WHERE release_id=$1`, release)
	b3RequireCount(t, app, 0, `SELECT count(*) FROM track_auto_values WHERE track_id=$1`, track)
	checkSuppressions := func() {
		t.Helper()
		b3RequireCount(t, app, 0, `SELECT count(*) FROM curation_suppressions WHERE target_id IN ($1,$2)`, release, track)
	}
	checkSuppressions()
	byType := map[string]operationItem{}
	for _, item := range list(2) {
		byType[item.Type] = item
	}
	for _, tc := range []struct {
		kind, typ, owner, scanned, current, automatic string
		track                                         map[string]string
	}{
		{governance.TypeReleaseAutoInvalidation, operations.TypeReleaseAutoCorrection, releaseOp, "原发行标题", "新发行标题", "自动发行标题", nil},
		{governance.TypeTrackAutoInvalidation, governance.TypeTrackAutoCorrection, trackOp, "原曲目标题", "新曲目标题", "自动曲目标题", map[string]string{"id": track, "title": "新曲目标题"}},
	} {
		item := byType[tc.typ]
		if item.ID == "" || !item.Revertible || item.Reverts != nil {
			t.Fatalf("%s 应有可撤销的作废记录，实际 %+v", tc.typ, item)
		}
		want := map[string]any{"kind": tc.kind, "fields": []any{map[string]any{
			"field": "title",
			"before": map[string]any{
				"auto": map[string]any{"mode": "set", "value": tc.automatic, "operation_id": tc.owner,
					"writer_level": catalog.LevelProgramExternal, "scan_basis": tc.scanned},
				"effective": tc.automatic, "scan_value": tc.current,
			},
			"after": map[string]any{"auto": nil, "effective": tc.current, "scan_value": tc.current},
		}}}
		if tc.track != nil {
			want["track"] = tc.track
		}
		requireExternalJSON(t, item.Changes, want)
		f.undo(t, item.ID)
	}
	checkSuppressions()
	readEvidence()
	requireAutoLayerTitles(t, f, release, track, "自动发行标题", "自动曲目标题")
	b3RequireCount(t, app, 1, `SELECT count(*) FROM release_auto_values
 WHERE release_id=$1 AND operation_id=$2 AND writer_level=4 AND scan_basis=to_jsonb('新发行标题'::text)`, release, releaseOp)
	b3RequireCount(t, app, 1, `SELECT count(*) FROM track_auto_values
 WHERE track_id=$1 AND operation_id=$2 AND writer_level=4 AND scan_basis=to_jsonb('新曲目标题'::text)`, track, trackOp)
	list(2)
}

func TestPostgreSQL没有扫描依据的旧自动值保留(t *testing.T) {
	f := newCurationFixture(t)
	f.enable(t)
	app := f.application
	scanAutoLayerTags(t, f, "原发行标题", "原曲目标题")
	release := programString(t, app, `SELECT id FROM releases WHERE relative_directory='自动层重扫'`)
	track, _ := externalTrack(t, app, release)
	releaseOp := f.apply(t, release, "fixture", "", map[string]any{"title": "旧自动发行标题"})
	trackOp := applyTrackAuto(t, f, trackAutoRequest(track, release, "fixture", "", map[string]any{"title": "旧自动曲目标题"}))
	// 模拟 0088 给已有自动值补列后的空值。
	mustExec(t, app, `UPDATE release_auto_values SET writer_level=NULL, scan_basis=NULL WHERE release_id=$1`, release)
	mustExec(t, app, `UPDATE track_auto_values SET writer_level=NULL, scan_basis=NULL WHERE track_id=$1`, track)

	scanAutoLayerTags(t, f, "新发行标题", "新曲目标题")
	b3RequireCount(t, app, 1, `SELECT count(*) FROM releases WHERE id=$1 AND title='新发行标题'`, release)
	b3RequireCount(t, app, 1, `SELECT count(*) FROM tracks WHERE id=$1 AND title='新曲目标题'`, track)
	evidence, err := f.writer.LoadCurationEvidence(t.Context(), []string{release})
	requireCurationOK(t, err)
	if entry, found := evidence[release]; !found || !entry.Automatic["title"] || len(entry.StaleAuto) != 0 {
		t.Fatalf("旧发行的整理证据未保留自动标题：%+v", entry)
	}
	requireAutoLayerTitles(t, f, release, track, "旧自动发行标题", "旧自动曲目标题")
	b3RequireCount(t, app, 1, `SELECT count(*) FROM release_auto_values
 WHERE release_id=$1 AND operation_id=$2 AND writer_level IS NULL AND scan_basis IS NULL`, release, releaseOp)
	b3RequireCount(t, app, 1, `SELECT count(*) FROM track_auto_values
 WHERE track_id=$1 AND operation_id=$2 AND writer_level IS NULL AND scan_basis IS NULL`, track, trackOp)
	b3RequireCount(t, app, 0, `SELECT count(*) FROM operations WHERE target_id=$1 AND origin->>'provider'='scan'`, release)
}

func TestPostgreSQL自动写入四级先后矩阵(t *testing.T) {
	f := newCurationFixture(t)
	f.enable(t)
	app := f.application
	levels := []catalog.AutoWriterLevel{catalog.LevelModelLocal, catalog.LevelModelReference, catalog.LevelModelExternal, catalog.LevelProgramExternal}
	for index, low := range levels {
		for _, high := range levels[index:] {
			t.Run(fmt.Sprintf("%d级到%d级", low, high), func(t *testing.T) {
				release, _ := insertGroupedRelease(t, app, f.rootID, groupedRelease{title: t.Name(), artist: "合成艺人", tracks: []string{"present"}})
				track, _ := externalTrack(t, app, release)
				for _, target := range []struct {
					name, id, extra, ownerSQL string
					apply                     func(*testing.T, curation.Source, map[string]any) (curation.Applied, error)
				}{
					{"发行", release, "edition", `SELECT count(*) FROM release_auto_values WHERE release_id=$1 AND field='title'
 AND text_value=$2 AND operation_id=$3 AND writer_level=$4`, func(t *testing.T, source curation.Source, values map[string]any) (curation.Applied, error) {
						req := autoRequest(release, source.Provider, "", values)
						req.Source = source
						return f.writer.Apply(t.Context(), req)
					}},
					{"曲目", track, "artist", `SELECT count(*) FROM track_auto_values WHERE track_id=$1 AND field='title'
 AND text_value=$2 AND operation_id=$3 AND writer_level=$4`, func(t *testing.T, source curation.Source, values map[string]any) (curation.Applied, error) {
						req := trackAutoRequest(track, release, source.Provider, "", values)
						req.Source = source
						return f.writer.ApplyTrack(t.Context(), req)
					}},
				} {
					t.Run(target.name, func(t *testing.T) {
						source := func(level catalog.AutoWriterLevel) curation.Source {
							kind := "model"
							if level == catalog.LevelProgramExternal {
								kind = "source"
							}
							return curation.Source{Kind: kind, Provider: "fixture", WriterLevel: level}
						}
						writeTitle := func(level catalog.AutoWriterLevel, title string) string {
							t.Helper()
							got, err := target.apply(t, source(level), map[string]any{"title": title})
							requireCurationOK(t, err)
							if !slices.Equal(got.Written, []string{"title"}) || len(got.Skipped) != 0 {
								t.Fatalf("%d 级写入返回 %+v", level, got)
							}
							b3RequireCount(t, app, 1, target.ownerSQL, target.id, title, got.OperationID, level)
							return got.OperationID
						}
						writeTitle(low, "首次标题")
						owner := writeTitle(high, "替换标题")
						if low == high {
							return
						}

						wantSkipped := map[string]string{"title": "lower_priority"}
						denied, err := target.apply(t, source(low), map[string]any{"title": "低级标题"})
						curationCRequireProblem(t, err, 409, "nothing_to_apply")
						if denied.OperationID != "" || len(denied.Written) != 0 || !maps.Equal(denied.Skipped, wantSkipped) {
							t.Fatalf("全部拒绝的结果 %+v，期望跳过 %v", denied, wantSkipped)
						}
						partial, err := target.apply(t, source(low), map[string]any{"title": "低级标题", target.extra: "可写的值"})
						requireCurationOK(t, err)
						if partial.OperationID == "" || !slices.Equal(partial.Written, []string{target.extra}) || !maps.Equal(partial.Skipped, wantSkipped) {
							t.Fatalf("部分写入的结果 %+v，期望写入 %s 并跳过 %v", partial, target.extra, wantSkipped)
						}
						b3RequireCount(t, app, 1, target.ownerSQL, target.id, "替换标题", owner, high)
					})
				}
			})
		}
	}
}
