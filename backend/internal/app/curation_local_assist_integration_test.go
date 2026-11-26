package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/curation/model"
	"github.com/qlqq/roomusic/backend/internal/identity"
	operationtools "github.com/qlqq/roomusic/backend/internal/operations/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/platform/config"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/coverartarchive"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
	"github.com/qlqq/roomusic/backend/internal/upstreammock"
)

type assistEvidence struct {
	Releases []struct {
		ID        string          `json:"release_id"`
		Reasons   []string        `json:"reasons"`
		Fields    []string        `json:"fields"`
		Credits   []assistCredit  `json:"credits"`
		Conflicts json.RawMessage `json:"conflicts"`
		Tracks    []assistTrack   `json:"tracks"`
	}
}

type assistCredit struct {
	Target    string `json:"target_id"`
	Role      string `json:"role"`
	Ref       string `json:"source_ref"`
	Raw       string `json:"raw"`
	Requested bool   `json:"requested"`
}

type assistTrack struct {
	catalog.LocalFile
	ID     string `json:"track_id"`
	FileID string `json:"file_id"`
	Path   string `json:"path"`
	Track  int    `json:"track"`
	Disc   int    `json:"disc"`
}

func loadAssistEvidence(t *testing.T, raw []byte) assistEvidence {
	t.Helper()
	var result assistEvidence
	requireCurationOK(t, json.Unmarshal(raw, &result))
	return result
}

func projectAssist(t *testing.T, app *integrationTestApplication, releases ...string) {
	t.Helper()
	tx, err := app.database.BeginTx(t.Context(), nil)
	requireCurationOK(t, err)
	defer tx.Rollback()
	requireCurationOK(t, curation.ProjectLocalItems(t.Context(), tx, releases, nil))
	requireCurationOK(t, tx.Commit())
}

func assistItem(t *testing.T, app *integrationTestApplication, release string) curation.PendingItem {
	t.Helper()
	id := programString(t, app, `SELECT id FROM curation_pending_items
		WHERE kind='local_cleanup' AND evidence->'release_ids' ? $1 AND status<>'resolved'`, release)
	item, err := curation.NewWriter(app.database, app.dataDirectory, nil).PendingItem(t.Context(), id)
	requireCurationOK(t, err)
	return item
}

func assistClaim(t *testing.T, app *integrationTestApplication, admin *integrationTestUser, item curation.PendingItem) (*curation.Writer, context.Context, string) {
	t.Helper()
	writer := curation.NewWriter(app.database, app.dataDirectory, nil).WithArtists(governance.NewArtistGovernance(artistResolutionAdapter{}))
	conversation, err := runtime.NewStore(app.database).CreateCurationConversation(t.Context(), admin.id, "本地辅助合成会话", "assist-test")
	requireCurationOK(t, err)
	claims, err := writer.ClaimPreparedKind(t.Context(), conversation.ID, time.Now(), "local", []curation.PendingItem{item})
	requireCurationOK(t, err)
	if len(claims) != 1 || claims[0].ID != item.ID {
		t.Fatalf("领取 %s，实际 %+v", item.ID, claims)
	}
	batch, err := writer.OpenBatch(t.Context(), "model")
	requireCurationOK(t, err)
	ctx := curation.WithModelRun(t.Context(), &curation.ModelRun{
		ConversationID: conversation.ID, Model: "assist-test", ActorID: admin.id, PendingItemID: item.ID, TaskKind: "local",
		ClaimDigests: map[string]string{item.ID: claims[0].ModelClaimDigest()},
		Batch:        func(context.Context) (string, error) { return batch.ID, nil },
	})
	return writer, ctx, batch.ID
}

func assistReleaseAt(t *testing.T, app *integrationTestApplication, path string) string {
	t.Helper()
	return programString(t, app, `SELECT m.release_id FROM tracks t JOIN media m ON m.id=t.medium_id WHERE t.relative_path=$1 AND t.source_status='present'`, path)
}

func assistSnapshot(t *testing.T, app *integrationTestApplication) string {
	t.Helper()
	return programString(t, app, `SELECT jsonb_agg(jsonb_build_array(t.relative_path,r.id,r.title,r.artist,r.album_artist,
		m.position,t.title,t.artist,t.disc_number,t.position) ORDER BY t.relative_path)::text
		FROM tracks t JOIN media m ON m.id=t.medium_id JOIN releases r ON r.id=m.release_id WHERE t.source_status='present'`)
}

func assistScanFixture(t *testing.T, samples ...metricSample) (*integrationTestApplication, integrationTestUser) {
	t.Helper()
	app := newIntegrationTestApplication(t)
	admin := app.createUser(t, "admin")
	b3Root(t, app)
	for _, sample := range samples {
		writeMetricSample(t, app.allowedRoot, sample)
	}
	scanAndConfirmCuration(t, app, &admin)
	return app, admin
}

func assistAlbum(id, dir, album, artist string, tracks ...metricLocalTrack) metricSample {
	return metricSample{ID: id, Local: metricLocal{Directory: dir, Format: "flac", Album: album, Artist: artist, Tracks: tracks}}
}

func assistSong(file, title, position string) metricLocalTrack {
	tags := map[string]string{}
	if position != "" {
		tags["TRACKNUMBER"] = position
	}
	return metricLocalTrack{File: file, Title: title, Seconds: 100, Tags: tags}
}

func TestPostgreSQLLocalAssistSevenReasons(t *testing.T) {
	fixed := fixedSamples(t, "filename-only,legacy-many,legacy-one,amp-comma,mixed-a,mixed-b,mixed-contiguous,named-bonus,unknown-disc-a,unknown-disc-b,missing-position,embedded-artist")
	var samples []metricSample
	for _, sample := range fixed {
		samples = append(samples, sample.metricSample)
	}
	// 根下相邻目录首曲均从 1 开始。非根父目录下另放同艺人的两张独立专辑，由脚本明确保留。
	samples = append(samples,
		assistAlbum("top-a", "Top Album A", "Top Album A", "Root Duo", assistSong("01.flac", "First", "1")),
		assistAlbum("top-b", "Top Album B", "Top Album B", "Root Duo", assistSong("01.flac", "Second", "1")),
		assistAlbum("peer-a", "Independent/One", "Dawn Album", "Harbor Band", assistSong("01.flac", "Morning", "1")),
		assistAlbum("peer-b", "Independent/Two", "Dusk Album", "Harbor Band", assistSong("01.flac", "Evening", "1")),
		assistAlbum("ordinary", "Ordinary", "Single Disc", "Solo Artist", assistSong("01.flac", "Song", "1")),
		assistAlbum("known-cd1", "Known/CD1", "Known Discs", "Known Quartet", assistSong("01.flac", "Song One", "1")),
		assistAlbum("known-cd2", "Known/CD2", "Known Discs", "Known Quartet", assistSong("01.flac", "Song Two", "1")),
	)
	multi := assistAlbum("no-disc", "Multi Without Disc", "Multi Without Disc", "Multi Ensemble", assistSong("01.flac", "Song One", "1"))
	multi.Local.Tags = map[string]string{"DISCTOTAL": "2"}
	samples = append(samples, multi)
	catalogDir := writeMetricCatalog(t, samples)
	for _, withModel := range []bool{false, true} {
		name := "未配置模型"
		if withModel {
			name = "脚本模型"
		}
		t.Run(name, func(t *testing.T) {
			mock, err := upstreammock.New(upstreammock.Options{DataDir: catalogDir, ScriptFile: filepath.Join(fixedData, "fixed-script.json")})
			requireCurationOK(t, err)
			server := httptest.NewServer(mock)
			t.Cleanup(server.Close)
			clock := newProgramClock()
			opts := clock.options()
			opts.FullPassInterval = time.Minute
			fast := func() *sources.Limiter { return sources.NewLimiter(0, nil, nil) }
			app := newConfiguredIntegrationTestApplication(t, slog.New(slog.NewTextHandler(io.Discard, nil)), config.Server{UpstreamMockURL: server.URL},
				WithCurationProgram(true, opts), WithCurationModel(true, model.Options{Now: opts.Now, PollInterval: 10 * time.Millisecond, BatchSize: 1}),
				WithCurationSources(sources.Options{Limiter: fast()}, coverartarchive.Options{Limiter: fast(), ManifestLimiter: fast()}, sources.Options{Limiter: fast()}))
			clock.wait(t)
			admin := app.createUser(t, "admin")
			fixedConfigure(t, app, &admin, server.URL, withModel, false)
			fixedSource(t, app, &admin, "web_search", false)
			for _, sample := range samples {
				writeMetricSample(t, app.allowedRoot, sample)
			}
			scanAndConfirmCuration(t, app, &admin)
			before := assistSnapshot(t, app)
			var releases []string
			metricReadRows(t, app, `SELECT id FROM curation_stable_releases`, func(rows *sql.Rows) {
				var id string
				requireCurationOK(t, rows.Scan(&id))
				releases = append(releases, id)
			})
			projectAssist(t, app, releases...)
			reasons := map[string]bool{}
			wantReasons := []string{"filename_only", "legacy_encoding", "artist_split", "mixed_directory",
				"unknown_disc_directory", "missing_position", "missing_disc", "compound_title"}
			metricReadRows(t, app, `SELECT evidence FROM curation_pending_items WHERE kind='local_cleanup' AND status='waiting_model'`, func(rows *sql.Rows) {
				var raw []byte
				requireCurationOK(t, rows.Scan(&raw))
				for _, entry := range loadAssistEvidence(t, raw).Releases {
					for _, reason := range entry.Reasons {
						reasons[reason] = true
					}
				}
			})
			for _, reason := range wantReasons {
				if !reasons[reason] {
					t.Errorf("缺少入口 %s，实际 %v", reason, reasons)
				}
			}
			for _, sample := range samples {
				if !slices.Contains([]string{"top-a", "top-b", "ordinary", "known-cd1", "known-cd2", "legacy-one", "mixed-contiguous"}, sample.ID) {
					continue
				}
				var part fixedPartition
				part = fixedScanPartition(t, app, fixedSample{metricSample: sample})
				for _, id := range part.ids {
					b3RequireCount(t, app, 0, `SELECT count(*) FROM curation_pending_items WHERE kind='local_cleanup' AND evidence->'release_ids' ? $1`, id)
				}
			}
			b3Enable(t, app, admin)
			clock.advance(t, time.Minute)
			if withModel {
				schedulerWait(t, "有脚本回答的本地辅助目录结案", func() bool {
					return countRows(t, app, `SELECT count(*) FROM curation_pending_items WHERE kind='local_cleanup' AND status<>'resolved'`) == 0
				})
				for _, reason := range wantReasons {
					n := countRows(t, app, `SELECT count(*) FROM curation_pending_items p
						WHERE p.kind='local_cleanup' AND p.conversation_id IS NOT NULL AND p.resolution IN ('local_cleanup','undetermined')
						AND EXISTS(SELECT FROM jsonb_array_elements(p.evidence->'releases') r WHERE r->'reasons' ? $1)`, reason)
					if n == 0 {
						t.Errorf("入口 %s 缺少脚本完成的答复", reason)
					}
				}
				b3RequireCount(t, app, 1, `SELECT count(DISTINCT p.id) FROM curation_pending_items p WHERE p.kind='local_cleanup'
					AND p.resolution='undetermined' AND p.evidence->'directories' ?& ARRAY['Independent/One','Independent/Two']`)
				runScan(t, app, &admin)
				if assistReleaseAt(t, app, "Independent/One/01.flac") == assistReleaseAt(t, app, "Independent/Two/01.flac") {
					t.Error("同一艺人的两张独立专辑被合并")
				}
			} else {
				if got := assistSnapshot(t, app); got != before {
					t.Errorf("缺配模型改变了扫描字段、成员或位次：%s -> %s", before, got)
				}
				b3RequireCount(t, app, 0, `SELECT count(*) FROM steward_conversations WHERE kind='curation'`)
				b3RequireCount(t, app, 0, `SELECT count(*) FROM operations WHERE origin->>'mode'='local_cleanup'`)
				if len(metricCounters(t, mock)) == 0 {
					t.Error("无模型时程序没有执行合法的来源请求")
				}
			}
		})
	}
}

func TestPostgreSQLLocalAssistGroupingRevert(t *testing.T) {
	for _, scenario := range []string{"未消费按条撤销", "消费后按条撤销", "消费后按批次撤销", "消费后按来源撤销", "人工并组后扫描", "人工移出后扫描", "成员文件变化"} {
		t.Run(scenario, func(t *testing.T) {
			a := assistAlbum("first", "Box/First", "Box First", "Two Voices", assistSong("take.flac", "Solo", "1"))
			b := assistAlbum("second", "Box/Second", "Box Second", "Two Voices", assistSong("take.flac", "Duet", "1"))
			app, admin := assistScanFixture(t, a, b)
			const left, right = "Box/First/take.flac", "Box/Second/take.flac"
			l, r := assistReleaseAt(t, app, left), assistReleaseAt(t, app, right)
			if l == r {
				t.Fatal("普通兄弟目录在脚本归组前应独立")
			}
			if scenario == "人工移出后扫描" {
				mustExec(t, app, `UPDATE releases SET group_id=(SELECT group_id FROM releases WHERE id=$2) WHERE id=$1`, l, r)
			}
			before := assistSnapshot(t, app)
			projectAssist(t, app, l)
			item := assistItem(t, app, l)
			e := loadAssistEvidence(t, item.Evidence)
			if len(e.Releases) != 2 {
				t.Fatalf("应给出两个发行，实际 %+v", e.Releases)
			}
			b3Enable(t, app, admin)
			writer, ctx, batch := assistClaim(t, app, &admin, item)
			grouping := &curation.LocalGroup{}
			var files []curation.LocalGroupFile
			for _, release := range e.Releases {
				for _, track := range release.Tracks {
					disc := 1
					if track.Path == right {
						disc = 2
					}
					files = append(files, curation.LocalGroupFile{FileID: track.FileID, LocalPosition: curation.LocalPosition{Disc: disc, Track: 1}})
				}
			}
			grouping.Albums = append(grouping.Albums, struct {
				Files []curation.LocalGroupFile `json:"files"`
			}{Files: files})
			requireCurationOK(t, writer.SubmitLocal(ctx, curation.LocalInput{ItemID: item.ID, Decision: "apply", Grouping: grouping}))
			b3RequireCount(t, app, 1, `SELECT count(*) FROM local_model_groupings WHERE jsonb_array_length(assignments)=2`)
			if got := assistSnapshot(t, app); got != before {
				t.Fatalf("提交时已改变成员：%s -> %s", before, got)
			}
			operation := programString(t, app, `SELECT operation_id FROM local_model_groupings`)
			registry := tool.NewRegistry()
			for _, def := range operationtools.RevertTools(app.database, curation.LocalGroupingOperations{}) {
				requireCurationOK(t, registry.Register(def))
			}
			caller := tool.Caller{User: identity.User{ID: admin.id, Role: "admin"}, Source: tool.SourceChat,
				ConversationID: uuid.New(), SharedExecution: true, Mode: "operator"}
			preview, err := registry.PreviewCall(t.Context(), caller, "revert_operation", modelToolInput(t, map[string]any{"operation_id": operation}))
			requireCurationOK(t, err)
			if len(preview.Changes) != 1 || preview.Changes[0].Field != "grouping" || preview.Changes[0].Before == nil {
				t.Fatalf("撤销预览应给出原有文件位次，实际 %+v", preview)
			}
			if scenario == "人工并组后扫描" || scenario == "人工移出后扫描" {
				rev := countRows(t, app, `SELECT edit_revision FROM releases WHERE id=$1`, l)
				body := map[string]any{"expected_revision": rev, "action": "detach"}
				if scenario == "人工并组后扫描" {
					body["action"] = "join"
					body["group_id"] = programString(t, app, `SELECT group_id FROM releases WHERE id=$1`, r)
				}
				resp := app.request(t, http.MethodPost, "/api/v1/releases/"+l+"/group", body,
					&admin, map[string]string{"Idempotency-Key": uuid.New()})
				if resp.Code != http.StatusOK {
					t.Fatalf("人工归组 HTTP %d %s", resp.Code, resp.Body)
				}
				runScan(t, app, &admin)
				if assistReleaseAt(t, app, left) == assistReleaseAt(t, app, right) {
					t.Error("模型计划覆盖了之后的手动归组")
				}
				return
			}
			if scenario == "成员文件变化" {
				writeSyntheticTrack(t, filepath.Join(app.allowedRoot, right), 91, "ALBUM=Box Second", "ARTIST=Two Voices", "TITLE=New Take", "TRACKNUMBER=1")
				runScan(t, app, &admin)
				if assistReleaseAt(t, app, left) == assistReleaseAt(t, app, right) {
					t.Error("源文件改变后仍执行旧归组")
				}
				return
			}
			if scenario != "未消费按条撤销" {
				runScan(t, app, &admin)
				if assistReleaseAt(t, app, left) != assistReleaseAt(t, app, right) {
					t.Errorf("无文件变化的增量扫描未消费模型计划，实际 %s", assistSnapshot(t, app))
				}
				if got := programString(t, app, `SELECT jsonb_agg(jsonb_build_array(m.position,t.position) ORDER BY t.relative_path)::text
					FROM tracks t JOIN media m ON m.id=t.medium_id WHERE t.source_status='present'`); got != "[[1, 1], [2, 1]]" {
					t.Errorf("曲位应为首碟首曲和第二碟首曲，实际 %s", got)
				}
			}
			var scope curation.Scope
			switch scenario {
			case "消费后按批次撤销":
				scope = curation.Scope{Kind: "batch", ID: batch}
			case "消费后按来源撤销":
				scope = curation.Scope{Kind: "provider", Provider: "assist-test"}
			}
			if scope.Kind == "" {
				resp := app.request(t, http.MethodPost, revertPath(operation), nil, &admin, map[string]string{"Idempotency-Key": uuid.New()})
				if resp.Code != http.StatusOK {
					t.Fatalf("按条撤销 HTTP %d %s", resp.Code, resp.Body)
				}
			} else {
				reverter := curation.NewReverter(app.database, curation.LocalGroupingOperations{})
				got, err := reverter.RevertScope(t.Context(), admin.id, scope, "", uuid.New(), "local-test")
				requireCurationOK(t, err)
				if got.Reverted != 1 || len(got.Skipped) != 0 {
					t.Fatalf("按范围撤销应撤一条，实际 %+v", got)
				}
			}
			b3RequireCount(t, app, 0, `SELECT count(*) FROM local_model_groupings`)
			runScan(t, app, &admin)
			if assistReleaseAt(t, app, left) == assistReleaseAt(t, app, right) {
				t.Error("撤销后的实际扫描仍按模型归组")
			}
		})
	}
}

func TestPostgreSQLLocalAssistCreditParts(t *testing.T) {
	const raw = "林舟  & 周雁"
	s := assistAlbum("credits", "Credits", "Letter", raw, assistSong("01.flac", "Letter One", "1"))
	s.Local.Tags = map[string]string{"ALBUMARTIST": raw}
	app, admin := assistScanFixture(t, s)
	release := assistReleaseAt(t, app, "Credits/01.flac")
	var before catalog.ReleaseDetail
	getJSON(t, app, &admin, "/api/v1/releases/"+release, &before)
	if before.AlbumArtist == nil {
		t.Fatal("扫描应提供专辑显示署名")
	}
	projectAssist(t, app, release)
	item := assistItem(t, app, release)
	e := loadAssistEvidence(t, item.Evidence)
	b3Enable(t, app, admin)
	writer, ctx, _ := assistClaim(t, app, &admin, item)
	var splits []curation.LocalSplit
	for _, credit := range e.Releases[0].Credits {
		if credit.Raw != raw {
			t.Fatalf("原署名字符有变化，实际 %q", credit.Raw)
		}
		splits = append(splits, curation.LocalSplit{
			Target: credit.Target, Role: credit.Role, Ref: credit.Ref, Parts: []string{"林舟", "周雁"}, Joins: []string{"  & "},
		})
	}
	if len(splits) != 2 {
		t.Fatalf("应给发行和曲目各一个可拆原署名，实际 %+v", splits)
	}
	requireCurationOK(t, writer.SubmitLocal(ctx, curation.LocalInput{ItemID: item.ID, Decision: "apply", Splits: splits}))
	b3RequireCount(t, app, 2, `SELECT count(*) FROM local_credit_parts WHERE raw=$1 AND parts='["林舟","周雁"]' AND joins='["  & "]'`, raw)
	check := func() {
		t.Helper()
		var detail catalog.ReleaseDetail
		getJSON(t, app, &admin, "/api/v1/releases/"+release, &detail)
		if detail.AlbumArtist == nil || *detail.AlbumArtist != *before.AlbumArtist {
			t.Errorf("显示署名应逐字保留扫描值 %q，实际 %+v", *before.AlbumArtist, detail.ReleaseSummary)
		}
		album, err := catalog.EffectiveAutoArtistCredits(t.Context(), app.database, release, "")
		requireCurationOK(t, err)
		track, err := catalog.EffectiveAutoArtistCredits(t.Context(), app.database, release, e.Releases[0].Tracks[0].ID)
		requireCurationOK(t, err)
		names := func(rows []catalog.CurationArtistCredit) []string {
			var out []string
			for _, row := range rows {
				out = append(out, row.Name)
			}
			return out
		}
		for role, got := range map[string][]string{"发行": names(album), "曲目": names(track)} {
			if !slices.Equal(got, []string{"林舟", "周雁"}) {
				t.Errorf("%s 的有效艺人应按分段原序，实际 %v", role, got)
			}
		}
	}
	check()
	runScan(t, app, &admin)
	check()
	b3RequireCount(t, app, 0, `SELECT count(*) FROM external_links WHERE object_type='artist'`)
	for _, query := range []string{
		`SELECT operation_id FROM release_auto_values WHERE release_id=$1 AND field='album_artist'`,
		`SELECT v.operation_id FROM track_auto_values v JOIN tracks t ON t.id=v.track_id JOIN media m ON m.id=t.medium_id WHERE m.release_id=$1 AND v.field='artist'`,
	} {
		id := programString(t, app, query, release)
		resp := app.request(t, http.MethodPost, revertPath(id), nil, &admin, map[string]string{"Idempotency-Key": uuid.New()})
		if resp.Code != http.StatusOK {
			t.Fatalf("撤销拆署名 HTTP %d %s", resp.Code, resp.Body)
		}
	}
	projectAssist(t, app, release)
	runScan(t, app, &admin)
	b3RequireCount(t, app, 0, `SELECT count(*) FROM curation_pending_items WHERE kind='local_cleanup' AND status<>'resolved'`)
	got, err := catalog.EffectiveAutoArtistCredits(t.Context(), app.database, release, "")
	requireCurationOK(t, err)
	if len(got) != 0 {
		t.Errorf("撤销并重投影后恢复了已撤的自动署名：%+v", got)
	}
}

func TestPostgreSQLLocalAssistBeforeMatch(t *testing.T) {
	t.Run("目录补齐后才查指定版", func(t *testing.T) {
		const title, artist = "Lantern Letters", "Harbor Choir"
		const dir = "06 - Harbor Choir - Lantern Letters"
		const file = "01 - Quiet Tide.flac"
		album := assistAlbum("folder", dir, "", "", assistSong(file, "", ""))
		artistID, externalID, groupID := uuid.New(), uuid.New(), uuid.New()
		album.External.Artists = []metricArtist{{ID: artistID, Name: artist}}
		album.External.Releases = []metricRelease{{ID: externalID, Group: groupID, GroupTitle: title,
			Title: title, Artist: artistID, Date: "2020-01-01",
			Tracks: []metricRecording{{ID: uuid.New(), TrackID: uuid.New(), Title: "Quiet Tide", Artist: artistID, Length: 100000}}}}
		other := assistAlbum("other", "Other", "Other Finished Album", "Other Quartet", assistSong("01.flac", "Opening", "1"))
		clock, endpoint := newProgramClock(), newMetricsModel(t)
		app, mock, upstream := newCurationMockApplication(t, writeMetricCatalog(t, []metricSample{album}), clock, patrolModelOptions(clock))
		admin := app.createUser(t, "admin")
		fixedConfigure(t, app, &admin, upstream, false, false)
		writeMetricSample(t, app.allowedRoot, album)
		writeMetricSample(t, app.allowedRoot, other)
		scanAndConfirmCuration(t, app, &admin)
		release := assistReleaseAt(t, app, dir+"/"+file)
		otherRelease := assistReleaseAt(t, app, "Other/01.flac")
		projectAssist(t, app, release, otherRelease)
		ready, answer := make(chan struct{}), make(chan struct{})
		defer closeUnlessClosed(answer)
		endpoint.localReply = func(item metricsPending) curation.LocalInput {
			select {
			case <-ready:
			default:
				close(ready)
			}
			select {
			case <-answer:
			case <-t.Context().Done():
			}
			return metricLocalAnswer(item, title, artist)
		}
		endpoint.configure(t, app, &admin, map[string]metricsModelTarget{
			release:      {SampleID: album.ID, ReleaseID: externalID, GroupID: groupID, SearchTitle: title},
			otherRelease: {SampleID: other.ID},
		}, upstream)
		b3Enable(t, app, admin)
		clock.advance(t, time.Minute)
		select {
		case <-ready:
		case <-time.After(5 * time.Second):
			t.Fatal("本地模型没有收到无标签目录")
		}
		b3RequireCount(t, app, 0, `SELECT count(*) FROM curation_pending_items WHERE target_id=$1 AND kind IN ('release_match','recording_match')`, release)
		queries := metricQueriesSince(t, mock, time.Time{})
		if slices.ContainsFunc(queries, func(q metricSourceQuery) bool { return strings.Contains(q.value, title) }) {
			t.Errorf("模型尚未提交，本发行已查询 MusicBrainz：%v", queries)
		}
		if !slices.ContainsFunc(queries, func(q metricSourceQuery) bool { return strings.Contains(q.value, "Other Finished Album") }) {
			t.Errorf("等待本地模型时其他发行也被挡住：%v", queries)
		}
		close(answer)
		schedulerWait(t, "本地项目提交并结算", func() bool {
			return countRows(t, app, `SELECT count(*) FROM curation_pending_items WHERE kind='local_cleanup' AND target_id=$1 AND resolution='local_cleanup'`, release) == 1
		})
		var settled time.Time
		requireCurationOK(t, app.database.QueryRow(`SELECT resolved_at FROM curation_pending_items WHERE target_id=$1 AND kind='local_cleanup' AND resolution='local_cleanup'`, release).Scan(&settled))
		localOwner := programString(t, app, `SELECT operation_id FROM release_auto_values
			WHERE release_id=$1 AND field='title' AND writer_level=1 AND text_value=$2`, release, title)
		clock.advance(t, time.Minute)
		b3RequireCount(t, app, 1, `SELECT count(*) FROM external_links WHERE source='musicbrainz' AND object_type='release' AND object_id=$1 AND external_id=$2`, release, externalID)
		queries = metricQueriesSince(t, mock, settled)
		if !slices.ContainsFunc(queries, func(q metricSourceQuery) bool {
			return strings.Contains(q.value, title) && strings.Contains(q.value, artist)
		}) {
			t.Errorf("结算后应按两个补全的名字查询，实际 %v", queries)
		}
		externalOwner := programString(t, app, `SELECT operation_id FROM release_auto_values
			WHERE release_id=$1 AND field='title' AND writer_level=4 AND text_value=$2`, release, title)
		if externalOwner == localOwner {
			t.Fatalf("同值的具体版识别必须接管标题，实际仍由 %s 持有", localOwner)
		}
		resp := app.request(t, http.MethodPost, revertPath(externalOwner), nil, &admin, map[string]string{"Idempotency-Key": uuid.New()})
		if resp.Code != http.StatusOK {
			t.Fatalf("撤销识别字段 HTTP %d %s", resp.Code, resp.Body)
		}
		b3RequireCount(t, app, 1, `SELECT count(*) FROM release_auto_values
			WHERE release_id=$1 AND field='title' AND writer_level=1 AND operation_id=$2 AND text_value=$3`, release, localOwner, title)
	})
	t.Run("A2.5 已匹配发行只补本地署名", func(t *testing.T) {
		const raw = "林舟 + 周雁"
		a, b, external, group := uuid.New(), uuid.New(), uuid.New(), uuid.New()
		credits := []metricCredit{{Artist: a, JoinPhrase: " & "}, {Artist: b}}
		s := assistAlbum("unmatched-credit", "Unmatched Credit", "合成署名发行", raw, assistSong("01.flac", "合成曲", "1"))
		s.Local.Tags = map[string]string{"ALBUMARTIST": raw, "MUSICBRAINZ_ALBUMID": external}
		s.External.Artists = []metricArtist{{ID: a, Name: "Lin Zhou"}, {ID: b, Name: "Zhou Yan"}}
		s.External.Releases = []metricRelease{{ID: external, Group: group, GroupTitle: s.Local.Album,
			Title: s.Local.Album, Artist: a, Credits: credits,
			Tracks: []metricRecording{{ID: uuid.New(), TrackID: uuid.New(), Title: "合成曲", Artist: a, Credits: credits, Length: 100000}}}}
		clock := newProgramClock()
		app, mock, upstream := newCurationMockApplication(t, writeMetricCatalog(t, []metricSample{s}), clock)
		admin := app.createUser(t, "admin")
		fixedConfigure(t, app, &admin, upstream, false, false)
		writeMetricSample(t, app.allowedRoot, s)
		scanAndConfirmCuration(t, app, &admin)
		release := assistReleaseAt(t, app, "Unmatched Credit/01.flac")
		b3Enable(t, app, admin)
		clock.advance(t, time.Minute)
		b3RequireCount(t, app, 1, `SELECT count(*) FROM external_links WHERE source='musicbrainz' AND object_type='release' AND object_id=$1 AND external_id=$2`, release, external)
		item := assistItem(t, app, release)
		e := loadAssistEvidence(t, item.Evidence)
		if len(e.Releases) != 1 || len(e.Releases[0].Fields) != 0 ||
			!slices.Equal(e.Releases[0].Reasons, []string{"artist_split"}) || len(e.Releases[0].Credits) == 0 {
			t.Fatalf("A2.5 应只投影本地拆署名，实际 %s", item.Evidence)
		}
		for _, c := range e.Releases[0].Credits {
			if !c.Requested || c.Raw != raw || c.Ref == "" {
				t.Errorf("应引用逐字本地署名，实际 %+v", c)
			}
		}
		before := metricCounters(t, mock)
		clock.advance(t, time.Minute)
		if !maps.Equal(before, metricCounters(t, mock)) {
			t.Errorf("拆分待办影响了已结算的外部匹配，调用由 %v 变为 %v", before, metricCounters(t, mock))
		}
	})
	for _, scenario := range []string{"等待中移除模型", "搁置后放行"} {
		t.Run(scenario, func(t *testing.T) {
			s := assistAlbum("waiting", "Waiting", "Holding Quartet - Night Letter", "Holding Quartet", assistSong("01.flac", "Nocturne", "1"))
			clock, endpoint := newProgramClock(), newMetricsModel(t)
			app, mock, upstream := newCurationMockApplication(t, writeMetricCatalog(t, nil), clock, patrolModelOptions(clock))
			admin := app.createUser(t, "admin")
			fixedConfigure(t, app, &admin, upstream, false, false)
			writeMetricSample(t, app.allowedRoot, s)
			scanAndConfirmCuration(t, app, &admin)
			release := assistReleaseAt(t, app, "Waiting/01.flac")
			projectAssist(t, app, release)
			item := assistItem(t, app, release)
			endpoint.configure(t, app, &admin, map[string]metricsModelTarget{release: {SampleID: s.ID}}, upstream)
			mustExec(t, app, `INSERT INTO curation_model_usage(day,input_tokens,output_tokens) VALUES($1,1,0)`, clock.options().Now().Format(time.DateOnly))
			resp := app.request(t, http.MethodPut, curationBase+"/settings",
				map[string]any{"enabled": true, "daily_token_limit": 1}, &admin, nil)
			if resp.Code != http.StatusOK {
				t.Fatalf("设置日额度 HTTP %d %s", resp.Code, resp.Body)
			}
			clock.advance(t, time.Minute)
			if len(metricQueriesSince(t, mock, time.Time{})) != 0 {
				t.Fatal("额度等待中开始了发行识别")
			}
			b3RequireCount(t, app, 1, `SELECT count(*) FROM curation_pending_items WHERE id=$1 AND status='waiting_model' AND conversation_id IS NULL`, item.ID)
			if scenario == "等待中移除模型" {
				resp := app.request(t, http.MethodPut, stewardBase+"/settings", map[string]string{"endpoint": "", "model": ""}, &admin, nil)
				if resp.Code != http.StatusOK {
					t.Fatalf("移除模型 HTTP %d %s", resp.Code, resp.Body)
				}
			} else {
				// 终止后的排队语义独立于触发终止的条件。
				mustExec(t, app, `UPDATE curation_pending_items SET status='shelved',model_stop_code='no_result',shelved_reason='模型没给出结果' WHERE id=$1`, item.ID)
			}
			clock.advance(t, time.Minute)
			if !slices.ContainsFunc(metricQueriesSince(t, mock, time.Time{}), func(q metricSourceQuery) bool {
				return q.service == "musicbrainz" && strings.Contains(q.value, "Night Letter")
			}) {
				t.Error("结束模型等待后未放行已有本地名字的合法查询")
			}
		})
	}

}

func TestPostgreSQLLocalAssistDirectoryReleases(t *testing.T) {
	for _, scenario := range []struct {
		name, resolution string
		staggered        bool
	}{
		{name: "同时稳定后定不了", resolution: "undetermined"},
		{name: "同时稳定后采用", resolution: "local_cleanup"},
		{name: "先后稳定", resolution: "undetermined", staggered: true},
		{name: "未配置模型"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			const dir = "Shared Shelf"
			const title = "Lantern Letters"
			first := assistAlbum("shelf-a", dir, "Harbor Choir - "+title, "Harbor Choir",
				assistSong("01-a.flac", "Low Tide", "1"), assistSong("02-a.flac", "North Jetty", "2"))
			second := assistAlbum("shelf-b", dir, "Evening Signals", "Signal Quartet",
				assistSong("01-b.flac", "Foghorn", "1"), assistSong("02-b.flac", "Last Ferry", "2"))
			samples := []metricSample{first, second}
			for i := range samples {
				sample := &samples[i]
				artist := metricArtist{ID: uuid.New(), Name: sample.Local.Artist}
				remote := metricRelease{ID: uuid.New(), Group: uuid.New(), Title: sample.Local.Album, Artist: artist.ID}
				for _, track := range sample.Local.Tracks {
					remote.Tracks = append(remote.Tracks, metricRecording{
						ID: uuid.New(), TrackID: uuid.New(), Title: track.Title, Artist: artist.ID, Length: track.Seconds * 1000,
					})
				}
				if i == 0 && scenario.resolution == "local_cleanup" {
					remote.Title = title
				}
				remote.GroupTitle = remote.Title
				sample.External.Artists = []metricArtist{artist}
				sample.External.Releases = []metricRelease{remote}
				sample.Local.Tags = map[string]string{"MUSICBRAINZ_ALBUMID": remote.ID}
			}

			clock, endpoint := newProgramClock(), newMetricsModel(t)
			app, mock, upstream := newCurationMockApplication(t, writeMetricCatalog(t, samples), clock, patrolModelOptions(clock))
			admin := app.createUser(t, "admin")
			fixedConfigure(t, app, &admin, upstream, false, false)
			for _, sample := range samples {
				writeMetricSample(t, app.allowedRoot, sample)
			}
			runScan(t, app, &admin)
			releases := []string{
				assistReleaseAt(t, app, dir+"/01-a.flac"),
				assistReleaseAt(t, app, dir+"/01-b.flac"),
			}
			if releases[0] == releases[1] {
				t.Fatal("同目录两套重复曲序应扫描为两张发行")
			}
			confirm := func(ids []string) {
				t.Helper()
				mustExec(t, app, `UPDATE releases SET curation_changed_at=now()-interval '60 seconds' WHERE id=ANY($1::uuid[])`, ids)
				runScan(t, app, &admin)
				b3RequireCount(t, app, len(ids), `SELECT count(*) FROM curation_stable_releases WHERE id=ANY($1::uuid[])`, ids)
			}
			releaseQueries := func() []cMatchRequest {
				var queries []cMatchRequest
				for _, request := range cMatchRequests(t, mock) {
					if request.Service == "musicbrainz" && (request.Path == "/musicbrainz/ws/2/release" ||
						strings.HasPrefix(request.Path, "/musicbrainz/ws/2/release/")) {
						queries = append(queries, request)
					}
				}
				return queries
			}
			const localItems = `SELECT count(*) FROM curation_pending_items WHERE kind='local_cleanup'`
			const directoryItems = localItems + ` AND evidence->'release_ids' ?& $1::text[]`
			requested, answer := make(chan string, 1), make(chan struct{})
			defer closeUnlessClosed(answer)
			if scenario.resolution != "" {
				endpoint.localReply = func(item metricsPending) curation.LocalInput {
					select {
					case requested <- item.ID:
					default:
					}
					select {
					case <-answer:
					case <-t.Context().Done():
					}
					if scenario.resolution == "local_cleanup" {
						return curation.LocalInput{Decision: "apply", Texts: []curation.LocalText{
							{Target: releases[0], Field: "title", Value: title},
						}}
					}
					return curation.LocalInput{Decision: "undetermined", Reason: "本地资料不足以调整两张专辑的归组"}
				}
				endpoint.configure(t, app, &admin, map[string]metricsModelTarget{
					releases[0]: {SampleID: first.ID, ReleaseID: samples[0].External.Releases[0].ID},
					releases[1]: {SampleID: second.ID, ReleaseID: samples[1].External.Releases[0].ID},
				}, upstream)
			}
			b3Enable(t, app, admin)
			if scenario.staggered {
				confirm(releases[:1])
				b3RequireCount(t, app, 0, `SELECT count(*) FROM curation_stable_releases WHERE id=$1`, releases[1])
				clock.advance(t, time.Minute)
				b3RequireCount(t, app, 1, directoryItems+` AND status='waiting_model' AND conversation_id IS NULL AND attempts=0`, releases)
				writer := curation.NewWriter(app.database, app.dataDirectory, nil)
				ready, err := writer.PeekModelKind(t.Context(), clock.options().Now(), 1, "local", []string{})
				requireCurationOK(t, err)
				if len(ready) != 0 {
					t.Errorf("第二张稳定前可领取目录项：%+v", ready)
				}
				select {
				case id := <-requested:
					t.Errorf("第二张稳定前模型收到目录项 %s", id)
				default:
				}
				state := b3WorkState(t, app, "release", releases[0])
				if state.Status != "pending" {
					t.Errorf("第二张稳定前第一张状态为 %q，结果 %s，期望 pending", state.Status, state.Result)
				}
				if queries := releaseQueries(); len(queries) > 0 {
					t.Errorf("第二张稳定前已查询 MusicBrainz：%v", queries)
				}
				confirm(releases[1:])
			} else {
				confirm(releases)
			}
			clock.advance(t, time.Minute)
			if scenario.resolution != "" {
				var itemID string
				scansBefore := countRows(t, app, `SELECT count(*) FROM scan_runs`)
				defer func() {
					if t.Failed() {
						logAssistDirectoryState(t, app, releases, scansBefore)
					}
				}()
				select {
				case itemID = <-requested:
					item := assistItem(t, app, releases[0])
					if itemID != item.ID {
						t.Errorf("模型收到目录项 %s，期望 %s", itemID, item.ID)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("两张发行稳定后模型未收到目录项")
				}
				b3RequireCount(t, app, 1, directoryItems+` AND status='in_progress'`, releases)
				if queries := releaseQueries(); len(queries) > 0 {
					t.Errorf("目录项结案前已查询 MusicBrainz：%v", queries)
				}
				close(answer)
				schedulerWait(t, "模型交回目录项", func() bool {
					return countRows(t, app, `SELECT count(*) FROM curation_pending_items WHERE id=$1 AND status<>'in_progress'`, itemID) == 1
				})
				if countRows(t, app, directoryItems+` AND id=$2 AND status='resolved' AND resolution=$3 AND attempts=1`, releases, itemID, scenario.resolution) != 1 {
					t.Errorf("目录项 %s 应结案为 %s 且只计一次有效回答", itemID, scenario.resolution)
				}
				clock.advance(t, time.Minute)
				b3RequireCount(t, app, 1, localItems)
				calls := 0
				for _, usage := range endpoint.usage() {
					calls += usage["local_cleanup"].Calls
				}
				if calls != 1 {
					t.Errorf("本地辅助模型请求 %d 次，期望 1 次", calls)
				}
			} else {
				b3RequireCount(t, app, 0, `SELECT count(*) FROM steward_conversations WHERE kind='curation'`)
				b3RequireCount(t, app, 0, `SELECT count(*) FROM operations WHERE origin->>'mode'='local_cleanup'`)
				if usage := endpoint.usage(); len(usage) != 0 {
					t.Errorf("未配置模型时收到了模型请求：%v", usage)
				}
			}
			queries := releaseQueries()
			for i, release := range releases {
				state := b3WorkState(t, app, "release", release)
				path := "/musicbrainz/ws/2/release/" + samples[i].External.Releases[0].ID
				if state.Status == "" || state.Status == "pending" || !slices.ContainsFunc(queries, func(q cMatchRequest) bool {
					return q.Path == path
				}) {
					t.Errorf("发行 %s 状态 %q，结果 %s，MusicBrainz 发行请求 %v，期望请求 %s 并离开 pending",
						samples[i].ID, state.Status, state.Result, queries, path)
				}
			}
		})
	}
}

func logAssistDirectoryState(t *testing.T, app *integrationTestApplication, releases []string, scansBefore int) {
	t.Helper()
	items := programString(t, app, `SELECT COALESCE(jsonb_agg(jsonb_build_object(
		'id',id,'status',status,'resolution',resolution,'attempts',attempts,'next_attempt_at',next_attempt_at,
		'empty_count',empty_count,'conversation_id',conversation_id,'release_ids',evidence->'release_ids')
		ORDER BY created_at)::text,'[]') FROM curation_pending_items WHERE kind='local_cleanup'`)
	tools := programString(t, app, `SELECT COALESCE(jsonb_agg(jsonb_build_object(
		'conversation_id',m.conversation_id,'tool',c.tool_name,'input',c.input,'status',c.status,'code',c.error_code,'output',c.output)
		ORDER BY m.created_at,c.position)::text,'[]')
		FROM steward_tool_calls c JOIN steward_messages m ON m.id=c.message_id
		JOIN steward_conversations s ON s.id=m.conversation_id WHERE s.kind='curation'`)
	scans := programString(t, app, `SELECT COALESCE(jsonb_agg(jsonb_build_object(
		'status',status,'started_at',started_at,'finished_at',finished_at) ORDER BY started_at)::text,'[]') FROM scan_runs`)
	groups := countRows(t, app, `SELECT count(*) FROM local_model_groupings`)
	t.Logf("目录项 %s，模型工具调用 %s，提交前扫描数 %d，当前扫描 %s，模型归组记录数 %d", items, tools, scansBefore, scans, groups)
	for _, release := range releases {
		state := b3WorkState(t, app, "release", release)
		t.Logf("发行 %s 工作状态 %q，尝试 %d 次，下次运行 %v，结果 %s",
			release, state.Status, state.Attempts, state.NextAttemptAt, state.Result)
	}
}

func closeUnlessClosed(ch chan struct{}) {
	select {
	case <-ch:
	default:
		close(ch)
	}
}

func TestPostgreSQLLocalAssistModelContract(t *testing.T) {
	t.Run("完整目录证据与明确无法判断", func(t *testing.T) {
		fixture := newSchedulerFixture(t)
		app, admin := fixture.application, &fixture.admin
		cue := assistAlbum("cue", "Set/Left", "Shared Set", "Local Quartet", assistSong("first", "Opening", "1"), assistSong("last", "Closing", "2"))
		cue.Local.Cue = true
		cue.Local.Tracks[0].Seconds, cue.Local.Tracks[1].Seconds = 2, 3
		right := assistAlbum("right", "Set/Right", "Shared Set", "Local Quartet", assistSong("disc-image.flac", "Right Take", "1"))
		right.Local.Tags = map[string]string{"COMMENT": "合成便笺七号"}
		writeMetricSample(t, app.allowedRoot, cue)
		writeMetricSample(t, app.allowedRoot, right)
		scanAndConfirmCuration(t, app, admin)
		release := assistReleaseAt(t, app, "Set/Right/disc-image.flac")
		mustExec(t, app, `INSERT INTO release_field_decisions(release_id,field_key,selected_source,confidence,candidates,action,rule_id,reason)
			VALUES($1,'year','tag','low','["2001","2002"]','uncertain_apply','synthetic','tied_candidates')
			ON CONFLICT(release_id,field_key) DO UPDATE SET candidates=excluded.candidates,action=excluded.action,reason=excluded.reason`, release)
		// 每一页仅提供其中一个发行时，重算出的证据和领取键必须相同。
		var ids []string
		metricReadRows(t, app, `SELECT DISTINCT m.release_id FROM media m JOIN tracks t ON t.medium_id=m.id
			WHERE t.relative_path LIKE 'Set/%' AND t.source_status='present' ORDER BY m.release_id`, func(rows *sql.Rows) {
			var id string
			requireCurationOK(t, rows.Scan(&id))
			ids = append(ids, id)
		})
		if len(ids) != 2 {
			t.Fatalf("两套同名文件应扫描成两个候选，实际 %v", ids)
		}
		projectAssist(t, app, ids[0])
		first := assistItem(t, app, ids[0])
		projectAssist(t, app, ids[1])
		if second := assistItem(t, app, ids[1]); second.ID != first.ID || second.InputDigest != first.InputDigest {
			t.Fatalf("跨页投影改变目录项 %s/%s -> %s/%s", first.ID, first.InputDigest, second.ID, second.InputDigest)
		}
		fixture.configure(t, admin)
		fixture.enable(t)
		q := fixture.next(t, false)
		if len(q.Tools) != 1 || q.Tools[0].Type != "function" || q.Tools[0].Function.Name != "submit_local_cleanup" {
			t.Fatalf("本地模型工具仅应含 submit_local_cleanup，实际 %+v", q.Tools)
		}
		var user string
		for _, message := range q.Messages {
			if message.Role == "user" {
				requireCurationOK(t, json.Unmarshal(message.Content, &user))
			}
		}
		items, found, err := metricsPendingItems(user)
		requireCurationOK(t, err)
		if !found || len(items) != 1 || items[0].ID != first.ID {
			t.Fatalf("首条消息应给当前目录项，实际 %s", user)
		}
		for _, phrase := range []string{"合成便笺七号", "Set/Left/disc-image.flac", "Set/Right/disc-image.flac", "tied_candidates"} {
			if !strings.Contains(user, phrase) && (phrase != "tied_candidates" || !strings.Contains(user, `"year":["2001","2002"]`)) {
				t.Errorf("模型消息缺少 %q，实际 %s", phrase, user)
			}
		}
		for _, forbidden := range []string{`"canonical_artist"`, `"external_id"`, `"musicbrainz_candidates"`} {
			if strings.Contains(user, forbidden) {
				t.Errorf("本地消息含外部证据键 %s", forbidden)
			}
		}
		e := loadAssistEvidence(t, items[0].RawEvidence)
		var cueTracks, physicalTracks []assistTrack
		for _, entry := range e.Releases {
			for _, track := range entry.Tracks {
				switch track.Path {
				case "Set/Left/disc-image.flac":
					cueTracks = append(cueTracks, track)
				case "Set/Right/disc-image.flac":
					physicalTracks = append(physicalTracks, track)
				}
			}
		}
		if len(cueTracks) != 2 || len(physicalTracks) != 1 ||
			cueTracks[0].FileID != cueTracks[1].FileID || cueTracks[0].SourcePath == cueTracks[1].SourcePath ||
			cueTracks[0].FileID == physicalTracks[0].FileID {
			t.Errorf("共享 CUE 文件应同文件引用、不同虚轨引用，其他目录同名文件应单独引用，实际 %+v", e.Releases)
		}
		const reason = "本地标签不能确定应否合组"
		q.tool(t, "submit_local_cleanup", curation.LocalInput{ItemID: first.ID, Decision: "undetermined", Reason: reason})
		schedulerWait(t, "带理由的明确保留结案", func() bool {
			return countRows(t, app, `SELECT count(*) FROM curation_pending_items WHERE id=$1 AND status='resolved' AND resolution='undetermined' AND evidence->>'decision_reason'=$2 AND attempts=1 AND empty_count=0`, first.ID, reason) == 1
		})
		b3RequireCount(t, app, 0, `SELECT count(*) FROM local_model_groupings`)
	})
	t.Run("过长在建会话前搁置", func(t *testing.T) {
		f := newSchedulerFixture(t)
		app := f.application
		const relative = "Long/one.flac"
		note := strings.Repeat("完整本地备注", 160)
		writeSyntheticTrack(t, filepath.Join(app.allowedRoot, relative), 82, "COMMENT="+note)
		scanAndConfirmCuration(t, app, &f.admin)
		release := assistReleaseAt(t, app, relative)
		projectAssist(t, app, release)
		item := assistItem(t, app, release)
		f.configure(t, &f.admin)
		f.update(t, http.MethodPut, curationBase+"/settings", map[string]any{"enabled": true, "max_request_tokens": 1})
		schedulerWait(t, "本地证据超限搁置", func() bool { return f.item(t, item.ID).Status == "shelved" })
		got := f.item(t, item.ID)
		if !strings.Contains(string(got.Evidence), note) || got.ShelvedReason == nil || *got.ShelvedReason != "内容太长" || got.Attempts != 0 {
			t.Fatalf("超限后应保留完整备注并记录原因，实际 %+v", got)
		}
		b3RequireCount(t, app, 0, `SELECT count(*) FROM steward_conversations WHERE kind='curation'`)
		requireCurationRuleRequestCount(t, f, 0)
	})
	t.Run("三次空答搁置", func(t *testing.T) {
		f := newSchedulerFixture(t)
		app := f.application
		const relative = "Empty/take.flac"
		writeSyntheticTrack(t, filepath.Join(app.allowedRoot, relative), 83)
		scanAndConfirmCuration(t, app, &f.admin)
		release := assistReleaseAt(t, app, relative)
		projectAssist(t, app, release)
		item := assistItem(t, app, release)
		f.configure(t, &f.admin)
		f.enable(t)
		for count := 1; count <= 3; count++ {
			q := f.next(t, false)
			q.complete(t)
			schedulerWait(t, "本地项累计空答", func() bool { return f.item(t, item.ID).EmptyCount == count })
			got := f.item(t, item.ID)
			if got.Attempts != 0 {
				t.Fatalf("空答计入有效回答：%+v", got)
			}
			if count < 3 {
				if got.NextAttemptAt == nil {
					t.Fatalf("第 %d 次空答未安排重试：%+v", count, got)
				}
				f.clock.now.Store(got.NextAttemptAt.Add(time.Second).UnixNano())
			} else if got.Status != "shelved" || got.ShelvedReason == nil || *got.ShelvedReason != "模型没给出结果" {
				t.Fatalf("第三次空答应搁置：%+v", got)
			}
		}
		requireCurationRuleRequestCount(t, f, 3)
	})

	t.Run("上游额度暂停保留本地项", func(t *testing.T) {
		f := newSchedulerFixture(t)
		app := f.application
		const relative = "Quota/take.flac"
		writeSyntheticTrack(t, filepath.Join(app.allowedRoot, relative), 84)
		scanAndConfirmCuration(t, app, &f.admin)
		release := assistReleaseAt(t, app, relative)
		projectAssist(t, app, release)
		item := assistItem(t, app, release)
		f.configure(t, &f.admin)
		f.enable(t)
		q := f.next(t, false)
		q.fail <- schedulerHTTPFailure{status: http.StatusPaymentRequired, message: "quota exhausted"}
		schedulerWait(t, "额度用完后回收领取", func() bool {
			p := f.item(t, item.ID)
			return p.Status == "waiting_model" && p.ConversationID == ""
		})
		got := f.item(t, item.ID)
		if got.Attempts != 0 || got.EmptyCount != 0 || got.InputDigest != item.InputDigest {
			t.Fatalf("额度暂停应保留原输入且不计作回答，实际 %+v", got)
		}
		next := f.clock.options().Now().Add(time.Hour)
		requireCurationRulePause(t, f, "provider_quota", &next)
		requireCurationRuleRequestCount(t, f, 1)
	})

}
