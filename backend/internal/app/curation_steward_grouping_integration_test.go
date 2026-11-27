package app

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/curation"
	curationtools "github.com/qlqq/roomusic/backend/internal/curation/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/operations"
	operationtools "github.com/qlqq/roomusic/backend/internal/operations/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

const stewardRegroupTool = "regroup_local_files"

const (
	stewardFirst  = "Mixed/alpha.flac"
	stewardSecond = "Mixed/bravo.flac"
	stewardThird  = "Mixed/charlie.flac"
)

type stewardGroupingTrack struct {
	ID, Release string
	Disc, Track int
}

func stewardGroupingTracks(t *testing.T, app *integrationTestApplication) map[string]stewardGroupingTrack {
	t.Helper()
	rows, err := app.database.QueryContext(t.Context(), `SELECT t.relative_path,t.id,m.release_id,m.position,t.position
		FROM tracks t JOIN media m ON m.id=t.medium_id WHERE t.source_status='present'`)
	requireCurationOK(t, err)
	defer rows.Close()
	tracks := make(map[string]stewardGroupingTrack)
	for rows.Next() {
		var path string
		var track stewardGroupingTrack
		requireCurationOK(t, rows.Scan(&path, &track.ID, &track.Release, &track.Disc, &track.Track))
		if _, duplicate := tracks[path]; duplicate {
			t.Fatalf("扫描返回了重复的曲目路径 %q", path)
		}
		tracks[path] = track
	}
	requireCurationOK(t, rows.Err())
	return tracks
}

func stewardMixedSample() metricSample {
	first := assistSong("alpha.flac", "Alpha", "1")
	first.Tags["DISCNUMBER"] = "1"
	return assistAlbum("mixed", "Mixed", "Synthetic Collection", "Harbor Ensemble",
		first, assistSong("bravo.flac", "Bravo", "2"), assistSong("charlie.flac", "Charlie", "3"))
}

func stewardMixedGrouping(root string) curation.StewardGrouping {
	return curation.StewardGrouping{RootID: root, Directory: "Mixed", Albums: []curation.StewardGroupAlbum{
		{Files: []curation.StewardGroupFile{{Path: stewardFirst, Disc: 1, Track: 1}, {Path: stewardSecond, Disc: 1, Track: 2}}},
		{Files: []curation.StewardGroupFile{{Path: stewardThird, Disc: 2, Track: 3}}},
	}}
}

func stewardGroupingCaller(admin integrationTestUser) tool.Caller {
	return tool.Caller{User: identity.User{ID: admin.id, Role: "admin"}, Source: tool.SourceChat,
		ConversationID: uuid.New(), Mode: "operator", SharedExecution: true, IdempotencyKey: uuid.New()}
}

func submitStewardGrouping(t *testing.T, writer *curation.Writer, caller tool.Caller, input curation.StewardGrouping) operations.Result {
	t.Helper()
	result, err := writer.SubmitStewardGrouping(t.Context(), input, caller.User.ID, caller.IdempotencyKey, "steward-grouping-test",
		operations.Origin{Source: caller.Source, Mode: caller.Mode, ConversationID: caller.ConversationID})
	requireCurationOK(t, err)
	if !uuid.Valid(result.OperationID) || !strings.Contains(string(result.Response), "下一次扫描完成后生效") {
		t.Fatalf("归组提交结果 %+v", result)
	}
	return result
}

func stewardGroupingRevert(t *testing.T, app *integrationTestApplication, caller tool.Caller, operationID string, kind operations.Kind) string {
	t.Helper()
	registry := tool.NewRegistry()
	for _, definition := range operationtools.RevertTools(app.database, kind) {
		requireCurationOK(t, registry.Register(definition))
	}
	caller.IdempotencyKey = uuid.New()
	result, err := registry.Invoke(t.Context(), caller, "revert_operation", c9JSON(map[string]string{"operation_id": operationID}))
	requireCurationOK(t, err)
	var response struct {
		OperationID string `json:"operation_id"`
	}
	requireCurationOK(t, json.Unmarshal(result.Output, &response))
	if !uuid.Valid(response.OperationID) {
		t.Fatalf("撤销工具返回 %s", result.Output)
	}
	b3RequireCount(t, app, 1, `SELECT count(*) FROM operations WHERE id=$1 AND reverts_operation_id=$2`, response.OperationID, operationID)
	return response.OperationID
}

func requireStewardGroupingLayout(t *testing.T, before, after map[string]stewardGroupingTrack, input curation.StewardGrouping, majority string) {
	t.Helper()
	if len(after) != len(before) {
		t.Fatalf("扫描前后曲目数不同，之前 %v，之后 %v", before, after)
	}
	releases := make(map[string]bool)
	for i, album := range input.Albums {
		release := after[album.Files[0].Path].Release
		if release == "" || releases[release] || i == 0 && release != majority {
			t.Errorf("第 %d 张的发行 ID 为 %q，已核对 %v，多数曲目的原发行 %s", i+1, release, releases, majority)
		}
		releases[release] = true
		for _, file := range album.Files {
			want := stewardGroupingTrack{ID: before[file.Path].ID, Release: release, Disc: file.Disc, Track: file.Track}
			if got := after[file.Path]; got.ID == "" || got != want {
				t.Errorf("%s 扫描后 %+v，期望 %+v", file.Path, got, want)
			}
		}
	}
}

func waitStewardGroupingScan(t *testing.T, app *integrationTestApplication, previous int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	requireCurationOK(t, app.app.WaitForScans(ctx))
	b3RequireCount(t, app, previous+1, `SELECT count(*) FROM scan_runs`)
	b3RequireCount(t, app, previous+1, `SELECT count(*) FROM scan_runs WHERE status='succeeded'`)
}

func TestPostgreSQLStewardGrouping(t *testing.T) {
	for round := 1; round <= 3; round++ {
		t.Run(fmt.Sprintf("第%d轮", round), func(t *testing.T) {
			t.Run("混放拆分和幂等及撤销", testStewardGroupingSplit)
			t.Run("拆出Bonus", testStewardGroupingBonus)
			t.Run("撤销保护后归组", testStewardGroupingProtected)
			t.Run("拒绝非法归组和未就绪状态", testStewardGroupingInvalid)
			t.Run("本地辅助保留人工结构", testStewardGroupingLocalAssist)
			t.Run("聊天工具与关闭自动整理", testStewardGroupingTool)
		})
	}
}

func testStewardGroupingSplit(t *testing.T) {
	app, admin := assistScanFixture(t, stewardMixedSample())
	root := programString(t, app, `SELECT id FROM library_roots`)
	input := stewardMixedGrouping(root)
	caller := stewardGroupingCaller(admin)
	writer := curation.NewWriter(app.database, app.dataDirectory, nil).WithGroupingConfirmation(app.app.library.StartCurationConfirmation)
	before := stewardGroupingTracks(t, app)
	original := before[stewardFirst].Release
	if len(before) != 3 || before[stewardSecond].Release != original || before[stewardThird].Release != original {
		t.Fatalf("混放素材初次扫描应在同一发行，实际 %v", before)
	}
	scans := countRows(t, app, `SELECT count(*) FROM scan_runs`)
	result := submitStewardGrouping(t, writer, caller, input)
	if result.Replayed {
		t.Fatal("首次提交被标记为重放")
	}
	waitStewardGroupingScan(t, app, scans)
	b3RequireCount(t, app, 1, `SELECT count(*) FROM scan_runs s JOIN operations o ON o.id=$1
		WHERE s.trigger_kind='scheduled' AND NOT s.whole_library AND s.status='succeeded'
		AND s.started_at>=o.created_at AND s.started_at<o.created_at+interval '60 seconds'`, result.OperationID)
	requireStewardGroupingLayout(t, before, stewardGroupingTracks(t, app), input, original)
	b3RequireCount(t, app, 1, `SELECT count(*) FROM operations WHERE id=$1 AND actor_id=$2
		AND operation_type='local_model_grouping' AND origin->>'source'='chat'
		AND origin->>'conversation_id'=$3 AND origin->>'confirmed_by'=($2::uuid)::text`, result.OperationID, admin.id, caller.ConversationID)

	replayed := submitStewardGrouping(t, writer, caller, input)
	if !replayed.Replayed || replayed.OperationID != result.OperationID {
		t.Fatalf("重放返回 %+v，原结果 %+v", replayed, result)
	}
	var originalResponse map[string]string
	requireCurationOK(t, json.Unmarshal(result.Response, &originalResponse))
	requireExternalJSON(t, replayed.Response, originalResponse)
	changed := stewardMixedGrouping(root)
	changed.Albums[1].Files[0].Disc = 1
	_, err := writer.SubmitStewardGrouping(t.Context(), changed, admin.id, caller.IdempotencyKey, "", operations.Origin{Source: "chat"})
	curationCRequireProblem(t, err, http.StatusConflict, "idempotency_conflict")
	b3RequireCount(t, app, 1, `SELECT count(*) FROM operations WHERE operation_type='local_model_grouping'`)
	b3RequireCount(t, app, scans+1, `SELECT count(*) FROM scan_runs`)

	revert := stewardGroupingRevert(t, app, caller, result.OperationID, curation.LocalGroupingOperations{Writer: writer})
	waitStewardGroupingScan(t, app, scans+1)
	if restored := stewardGroupingTracks(t, app); !maps.Equal(restored, before) {
		t.Errorf("撤销扫描后的发行、曲目和位次为 %v，期望 %v", restored, before)
	}
	b3RequireCount(t, app, 0, `SELECT count(*) FROM local_model_groupings`)
	b3RequireCount(t, app, 1, `SELECT count(*) FROM curation_suppressions s
		JOIN operations original ON original.id=$4
		WHERE s.target_type='library_root' AND s.target_id=$1 AND s.subject='local_model_grouping'
		AND s.operation_id=$2 AND s.created_by=$3 AND s.value=original.after_state->'assignments'`, root, revert, admin.id, result.OperationID)
}

func testStewardGroupingBonus(t *testing.T) {
	const cd1, cd2, bonus = "Box/CD1/one.flac", "Box/CD2/two.flac", "Box/Bonus/extra.flac"
	app, admin := assistScanFixture(t,
		assistAlbum("cd1", "Box/CD1", "Synthetic Box", "Harbor Ensemble", assistSong("one.flac", "One", "1")),
		assistAlbum("cd2", "Box/CD2", "Synthetic Box", "Harbor Ensemble", assistSong("two.flac", "Two", "1")),
		assistAlbum("bonus", "Box/Bonus", "Synthetic Box", "Harbor Ensemble", assistSong("extra.flac", "Extra", "1")))
	root := programString(t, app, `SELECT id FROM library_roots`)
	before := stewardGroupingTracks(t, app)
	original := before[cd1].Release
	if len(before) != 3 || before[cd2].Release != original || before[bonus].Release != original ||
		before[cd1].Disc != 1 || before[cd2].Disc != 2 || before[bonus].Disc != 3 {
		t.Fatalf("首扫应把 CD1、CD2、Bonus 排为同张发行的三个碟，实际 %v", before)
	}
	input := curation.StewardGrouping{RootID: root, Directory: "Box", Albums: []curation.StewardGroupAlbum{
		{Files: []curation.StewardGroupFile{{Path: cd1, Disc: 1, Track: 1}, {Path: cd2, Disc: 2, Track: 1}}},
		{Files: []curation.StewardGroupFile{{Path: bonus, Disc: 3, Track: 1}}},
	}}
	writer := curation.NewWriter(app.database, app.dataDirectory, nil)
	submitStewardGrouping(t, writer, stewardGroupingCaller(admin), input)
	requireCurationOK(t, app.app.library.StartCurationConfirmation(t.Context()))
	waitStewardGroupingScan(t, app, 2)
	requireStewardGroupingLayout(t, before, stewardGroupingTracks(t, app), input, original)
}

func requireStewardGroupingFailure(t *testing.T, writer *curation.Writer, caller tool.Caller, input curation.StewardGrouping, code string, details ...string) {
	t.Helper()
	_, err := writer.SubmitStewardGrouping(t.Context(), input, caller.User.ID, uuid.New(), "steward-grouping-test", operations.Origin{Source: "chat"})
	_, got, msg := operations.Problem(err)
	if err == nil || got != code {
		t.Fatalf("归组请求错误 = %v（%s %q），期望 %s", err, got, msg, code)
	}
	for _, detail := range details {
		if !strings.Contains(msg, detail) {
			t.Errorf("错误 %s 的原因 %q 缺少 %q", got, msg, detail)
		}
	}
}

func testStewardGroupingProtected(t *testing.T) {
	t.Run("外部对应", func(t *testing.T) {
		app, admin := assistScanFixture(t, stewardMixedSample())
		root := programString(t, app, `SELECT id FROM library_roots`)
		before := stewardGroupingTracks(t, app)
		release := before[stewardFirst].Release
		writer := curation.NewWriter(app.database, app.dataDirectory, nil)
		b3Enable(t, app, admin)
		link, err := writer.ApplyLinks(t.Context(), curationCLinkRequest(release, "", curationCLink("release", release, uuid.New())))
		requireCurationOK(t, err)
		b3RequireCount(t, app, 1, `SELECT count(*) FROM external_links WHERE object_id=$1 AND operation_id=$2`, release, link.OperationID)
		input, caller := stewardMixedGrouping(root), stewardGroupingCaller(admin)
		requireStewardGroupingFailure(t, writer, caller, input, "protected", release, "Synthetic Collection", "已有外部对应", "revert_operation")
		b3RequireCount(t, app, 0, `SELECT count(*) FROM local_model_groupings`)

		stewardGroupingRevert(t, app, caller, link.OperationID, curation.ExternalLinkOperations(writer))
		b3RequireCount(t, app, 0, `SELECT count(*) FROM external_links WHERE object_id=$1`, release)
		submitStewardGrouping(t, writer, caller, input)
		requireCurationOK(t, app.app.library.StartCurationConfirmation(t.Context()))
		waitStewardGroupingScan(t, app, 2)
		requireStewardGroupingLayout(t, before, stewardGroupingTracks(t, app), input, release)
	})

	t.Run("人工合并", func(t *testing.T) {
		main := stewardMixedSample()
		main.Local.Tracks = main.Local.Tracks[:2]
		main.Local.Tags = map[string]string{"ALBUMARTIST": "Harbor Ensemble"}
		other := assistAlbum("separate", "Mixed", "Synthetic Single", "Inland Quartet", assistSong("charlie.flac", "Charlie", "1"))
		other.Local.Tags = map[string]string{"ALBUMARTIST": "Inland Quartet"}
		app, admin := assistScanFixture(t, main, other)
		root := programString(t, app, `SELECT id FROM library_roots`)
		before := stewardGroupingTracks(t, app)
		target, source := before[stewardFirst].Release, before[stewardThird].Release
		if target == "" || source == "" || target == source || before[stewardSecond].Release != target {
			t.Fatalf("人工合并前应为两张发行，实际 %v", before)
		}
		path := "/api/v1/releases/" + target
		var preview struct{ Token string }
		getJSON(t, app, &admin, path+"/merge-preview?source="+source, &preview)
		resp := app.request(t, http.MethodPost, path+"/merge", map[string]string{"source_release_id": source, "token": preview.Token},
			&admin, idempotencyHeader(uuid.New()))
		if resp.Code != http.StatusOK {
			t.Fatalf("人工合并 HTTP %d %s", resp.Code, resp.Body)
		}
		merge := programString(t, app, `SELECT operation_id FROM release_merges WHERE target_release_id=$1 AND source_release_id=$2`, target, source)
		scanAndConfirmCuration(t, app, &admin)
		if merged := stewardGroupingTracks(t, app); merged[stewardThird].Release != target {
			t.Fatalf("合并扫描后应属于目标发行，实际 %v", merged)
		}
		input, caller := stewardMixedGrouping(root), stewardGroupingCaller(admin)
		input.Albums[1].Files[0].Track = 1
		input.Albums[0].Files = append(input.Albums[0].Files, input.Albums[1].Files...)
		input.Albums = input.Albums[:1]
		writer := curation.NewWriter(app.database, app.dataDirectory, nil)
		requireStewardGroupingFailure(t, writer, caller, input, "protected", target, "Synthetic Collection", "人工合并", "revert_operation")
		b3RequireCount(t, app, 0, `SELECT count(*) FROM local_model_groupings`)

		stewardGroupingRevert(t, app, caller, merge, governance.ReleaseMergeOperations(operations.TypeReleaseMerge, writer))
		b3RequireCount(t, app, 0, `SELECT count(*) FROM release_merges`)
		if restored := stewardGroupingTracks(t, app); !maps.Equal(restored, before) {
			t.Errorf("撤销人工合并后的曲目为 %v，期望 %v", restored, before)
		}
		scans := countRows(t, app, `SELECT count(*) FROM scan_runs`)
		submitStewardGrouping(t, writer, caller, input)
		requireCurationOK(t, app.app.library.StartCurationConfirmation(t.Context()))
		waitStewardGroupingScan(t, app, scans)
		requireStewardGroupingLayout(t, before, stewardGroupingTracks(t, app), input, target)
	})
}

func testStewardGroupingInvalid(t *testing.T) {
	const outside = "Separate/other.flac"
	app, admin := assistScanFixture(t, stewardMixedSample(),
		assistAlbum("outside", "Separate", "Unrelated Record", "Inland Quartet", assistSong("other.flac", "Other", "1")))
	root := programString(t, app, `SELECT id FROM library_roots`)
	before := stewardGroupingTracks(t, app)
	if len(before) != 4 || before[outside].Release == before[stewardFirst].Release {
		t.Fatalf("应有一首已扫描但范围外的曲目，实际 %v", before)
	}
	writer, caller := curation.NewWriter(app.database, app.dataDirectory, nil), stewardGroupingCaller(admin)
	for _, tc := range []struct {
		name    string
		details []string
		change  func(*curation.StewardGrouping)
	}{
		{"漏文件", []string{"遗漏文件", stewardThird}, func(in *curation.StewardGrouping) {
			in.Albums = in.Albums[:1]
		}},
		{"文件重复", []string{"文件", stewardFirst, "重复"}, func(in *curation.StewardGrouping) {
			in.Albums[1].Files = append(in.Albums[1].Files, in.Albums[0].Files[0])
		}},
		{"同碟曲号重复", []string{"碟 1 曲 1 重复", stewardFirst, stewardSecond}, func(in *curation.StewardGrouping) {
			in.Albums[0].Files[1].Track = 1
		}},
		{"真实碟号冲突", []string{stewardFirst, "真实碟号为 1", "与提交的 2 冲突"}, func(in *curation.StewardGrouping) {
			in.Albums[0].Files[0].Disc = 2
		}},
		{"真实曲号冲突", []string{stewardFirst, "真实曲号为 1", "与提交的 4 冲突"}, func(in *curation.StewardGrouping) {
			in.Albums[0].Files[0].Track = 4
		}},
		{"范围外文件", []string{outside, "不在该目录的连通范围内"}, func(in *curation.StewardGrouping) {
			in.Albums[1].Files = append(in.Albums[1].Files, curation.StewardGroupFile{Path: outside, Disc: 1, Track: 1})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := stewardMixedGrouping(root)
			tc.change(&input)
			requireStewardGroupingFailure(t, writer, caller, input, "invalid_grouping", tc.details...)
		})
	}
	t.Run("目录未扫描", func(t *testing.T) {
		input := stewardMixedGrouping(root)
		input.Directory = "Missing"
		requireStewardGroupingFailure(t, writer, caller, input, "not_found", "没有已扫描的音频文件")
	})
	t.Run("扫描进行中", func(t *testing.T) {
		scan := uuid.New()
		// 注入扫描的持久状态，避免短扫描完成和测试提交之间的竞争。
		mustExec(t, app, `INSERT INTO scan_runs(id,status,started_at) VALUES($1,'running',now())`, scan)
		requireStewardGroupingFailure(t, writer, caller, stewardMixedGrouping(root), "scan_running", "曲库扫描进行中")
	})
	t.Run("发行不稳定", func(t *testing.T) {
		mustExec(t, app, `UPDATE scan_runs SET status='succeeded',finished_at=now() WHERE status='running'`)
		mustExec(t, app, `UPDATE releases SET curation_confirmed=false,curation_changed_at=now() WHERE id=$1`, before[stewardFirst].Release)
		requireStewardGroupingFailure(t, writer, caller, stewardMixedGrouping(root), "not_stable", "尚未完成稳定性确认")
	})
	if after := stewardGroupingTracks(t, app); !maps.Equal(after, before) {
		t.Errorf("拒绝请求后曲目变为 %v，期望 %v", after, before)
	}
	b3RequireCount(t, app, 0, `SELECT count(*) FROM local_model_groupings`)
	b3RequireCount(t, app, 0, `SELECT count(*) FROM operations WHERE operation_type='local_model_grouping'`)
}

func testStewardGroupingLocalAssist(t *testing.T) {
	fixture := newSchedulerFixture(t)
	app, admin := fixture.application, &fixture.admin
	sample := stewardMixedSample()
	delete(sample.Local.Tracks[2].Tags, "TRACKNUMBER")
	writeMetricSample(t, app.allowedRoot, sample)
	scanAndConfirmCuration(t, app, admin)
	before := stewardGroupingTracks(t, app)
	original := before[stewardFirst].Release
	if len(before) != 3 || original == "" || before[stewardThird].Release != original || before[stewardThird].Track != 0 {
		t.Fatalf("辅助夹具应为第三首缺少曲号的同一张发行，实际 %v", before)
	}

	projectAssist(t, app, original)
	item := assistItem(t, app, original)
	fixture.configure(t, admin)
	fixture.enable(t)
	request := fixture.next(t, false)
	if len(request.Tools) != 1 || request.Tools[0].Function.Name != "submit_local_cleanup" {
		t.Fatalf("后台本地辅助工具仅应含 submit_local_cleanup，实际 %+v", request.Tools)
	}
	evidence := loadAssistEvidence(t, item.Evidence)
	if len(evidence.Releases) != 1 || len(evidence.Releases[0].Tracks) != 3 || !slices.Contains(evidence.Releases[0].Reasons, "missing_position") {
		t.Fatalf("脚本应领到三首曲以及缺曲号证据，实际 %+v", evidence)
	}

	// 模型已领取旧目录。等管家归组生效后，让脚本返回把三首都留在同一张的方案。
	input := stewardMixedGrouping(fixture.rootID)
	writer := fixture.writer.WithGroupingConfirmation(app.app.library.StartCurationConfirmation)
	scans := countRows(t, app, `SELECT count(*) FROM scan_runs`)
	result := submitStewardGrouping(t, writer, stewardGroupingCaller(*admin), input)
	waitStewardGroupingScan(t, app, scans)
	split := stewardGroupingTracks(t, app)
	requireStewardGroupingLayout(t, before, split, input, original)

	var files []curation.LocalGroupFile
	for _, track := range evidence.Releases[0].Tracks {
		position := track.Track
		if track.Path == stewardThird {
			position = 3
		}
		files = append(files, curation.LocalGroupFile{FileID: track.FileID, LocalPosition: curation.LocalPosition{Disc: 1, Track: position}})
	}
	grouping := &curation.LocalGroup{Albums: []struct {
		Files []curation.LocalGroupFile `json:"files"`
	}{{Files: files}}}
	request.tool(t, "submit_local_cleanup", curation.LocalInput{ItemID: item.ID, Decision: "apply", Grouping: grouping})
	schedulerWait(t, "后台本地辅助拒绝过期归组", func() bool {
		return countRows(t, app, `SELECT count(*) FROM steward_tool_calls c
			JOIN steward_messages m ON m.id=c.message_id JOIN steward_conversations s ON s.id=m.conversation_id
			WHERE s.kind='curation' AND c.tool_name='submit_local_cleanup' AND c.error_code='item_stale'`) == 1
	})
	projectAssist(t, app, original, split[stewardThird].Release)
	b3RequireCount(t, app, 0, `SELECT count(*) FROM curation_pending_items WHERE kind='local_cleanup' AND status<>'resolved'
		AND (evidence->'release_ids') ?| ARRAY[$1,$2]::text[]`, original, split[stewardThird].Release)
	runScan(t, app, admin)
	if after := stewardGroupingTracks(t, app); !maps.Equal(after, split) {
		t.Errorf("脚本给出不同归组后，实际 %v，期望 %v", after, split)
	}
	b3RequireCount(t, app, 1, `SELECT count(*) FROM local_model_groupings WHERE operation_id=$1`, result.OperationID)
	b3RequireCount(t, app, 1, `SELECT count(*) FROM operations WHERE operation_type='local_model_grouping'`)
}

func testStewardGroupingTool(t *testing.T) {
	app, admin := assistScanFixture(t, stewardMixedSample())
	root := programString(t, app, `SELECT id FROM library_roots`)
	input := stewardMixedGrouping(root)
	before := stewardGroupingTracks(t, app)
	original := before[stewardFirst].Release
	writer := curation.NewWriter(app.database, app.dataDirectory, nil)
	registry := tool.NewRegistry()
	requireCurationOK(t, registry.Register(curationtools.RegroupLocalFiles(writer)))
	member := app.createUser(t, "user")
	caller := stewardGroupingCaller(member)
	caller.User.Role = "user"
	raw := c9JSON(input)
	_, err := registry.PreviewCall(t.Context(), caller, stewardRegroupTool, raw)
	c9Code(t, err, "permission_denied")
	_, err = registry.Invoke(t.Context(), caller, stewardRegroupTool, raw)
	c9Code(t, err, "permission_denied")
	for _, def := range curationtools.StewardTools(writer, nil) {
		if def.Name == stewardRegroupTool {
			t.Fatal("后台整理工具清单包含管家归组工具")
		}
	}

	scans := countRows(t, app, `SELECT count(*) FROM scan_runs`)
	server, _ := c9Model(t, []map[string]any{c9ModelCall(0, stewardRegroupTool, input)}, func(body map[string]any) {
		tools, _ := body["tools"].([]any)
		if !slices.ContainsFunc(tools, func(v any) bool {
			entry, _ := v.(map[string]any)
			function, _ := entry["function"].(map[string]any)
			return function["name"] == stewardRegroupTool
		}) {
			t.Error("管理员聊天的模型清单缺少管家归组工具")
		}
	})
	resp := app.request(t, http.MethodPut, stewardBase+"/settings", map[string]string{
		"endpoint": server.URL, "model": "regroup-script", "protocol": "openai",
	}, &admin, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("配置脚本模型 HTTP %d %s", resp.Code, resp.Body)
	}
	resp = app.request(t, http.MethodPost, stewardBase+"/conversations", map[string]string{"mode": "assistant"}, &admin, nil)
	if resp.Code != http.StatusCreated {
		t.Fatalf("创建聊天 HTTP %d %s", resp.Code, resp.Body)
	}
	conversation := decodeJSONMap(t, resp)["id"].(string)
	path := stewardBase + "/conversations/" + conversation
	resp = app.request(t, http.MethodPost, path+"/messages", map[string]string{"text": "把混放目录分成两张合成专辑"}, &admin, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("聊天归组预览 HTTP %d %s", resp.Code, resp.Body)
	}
	detail, err := runtime.NewStore(app.database).GetConversation(t.Context(), admin.id, conversation)
	requireCurationOK(t, err)
	if len(detail.Plans) != 1 || len(detail.Plans[0].Items) != 1 {
		t.Fatalf("聊天未创建一条归组预览，响应 %s", resp.Body)
	}
	plan := detail.Plans[0]
	item := plan.Items[0]
	if item.Tool != stewardRegroupTool || !item.Revertible || len(item.Changes) != 1 || item.Changes[0].Field != "grouping" {
		t.Fatalf("聊天计划的归组预览 %+v", item)
	}
	requireExternalJSON(t, c9JSON(item.Changes[0].Before), []curation.GroupingRelease{{ID: original, Title: "Synthetic Collection"}})
	requireExternalJSON(t, c9JSON(item.Changes[0].After), input.Albums)
	b3RequireCount(t, app, 0, `SELECT count(*) FROM operations WHERE operation_type='local_model_grouping'`)
	b3RequireCount(t, app, 0, `SELECT count(*) FROM local_model_groupings`)
	b3RequireCount(t, app, scans, `SELECT count(*) FROM scan_runs`)
	b3RequireCount(t, app, 1, `SELECT count(*) FROM curation_stable_releases WHERE id=$1`, original)
	if after := stewardGroupingTracks(t, app); !maps.Equal(after, before) {
		t.Errorf("预览后的曲目为 %v，期望 %v", after, before)
	}

	b3RequireCount(t, app, 1, `SELECT count(*) FROM curation_settings WHERE NOT enabled`)
	resp = app.request(t, http.MethodPost, path+"/plans/"+plan.ID+"/decision", map[string]any{
		"digest": plan.Digest, "accepted_item_ids": []string{item.ID},
	}, &admin, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("关闭自动整理时，确认聊天归组 HTTP %d %s", resp.Code, resp.Body)
	}
	waitStewardGroupingScan(t, app, scans)
	requireStewardGroupingLayout(t, before, stewardGroupingTracks(t, app), input, original)
	b3RequireCount(t, app, 1, `SELECT count(*) FROM operations WHERE operation_type='local_model_grouping'
		AND actor_id=$1 AND origin->>'source'='chat' AND origin->>'conversation_id'=$2 AND origin->>'confirmed_by'=($1::uuid)::text`, admin.id, conversation)
	b3RequireCount(t, app, 1, `SELECT count(*) FROM curation_settings WHERE NOT enabled`)
}
