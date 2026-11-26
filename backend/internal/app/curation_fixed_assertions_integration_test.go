package app

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPostgreSQLCurationFixedSelectedReport(t *testing.T) {
	if os.Getenv(postgrestest.Environment) == "" {
		t.Skipf("%s is not configured", postgrestest.Environment)
	}
	const modelEndpoint = "openai/chat/v1/chat/completions"
	const lookup = "musicbrainz/release/lookup"
	if os.Getenv("ROOMUSIC_CURATION_METRICS_SAMPLES") == "" {
		t.Setenv("ROOMUSIC_CURATION_METRICS_SAMPLES", "fingerprint-only,one-char,unlisted-01,id-conflict")
	}
	samples := fixedSamples(t, os.Getenv("ROOMUSIC_CURATION_METRICS_SAMPLES"))
	out := filepath.Join(t.TempDir(), "report.json")
	t.Setenv("ROOMUSIC_CURATION_METRICS_SUITE", "fixed")
	t.Setenv("ROOMUSIC_CURATION_METRICS_OUT", out)
	TestPostgreSQLCurationMetrics(t)
	data, err := os.ReadFile(out)
	requireCurationOK(t, err)
	var report fixedReport
	requireCurationOK(t, json.Unmarshal(data, &report))
	if len(report.Rows) != 2*len(samples) {
		t.Fatalf("两个模式报告 %d 行，期望 %d", len(report.Rows), 2*len(samples))
	}
	for _, sample := range samples {
		for _, mode := range []string{"program", "script"} {
			t.Run("report-"+mode+"/"+sample.ID, func(t *testing.T) {
				i := slices.IndexFunc(report.Rows, func(row fixedRow) bool { return row.ID == sample.ID && row.Mode == mode })
				if i < 0 {
					t.Fatal("缺少样本行")
				}
				row := report.Rows[i]
				if mode == "program" {
					for endpoint := range row.Requests {
						if strings.HasPrefix(endpoint, "openai/") || strings.HasPrefix(endpoint, "anthropic/") {
							t.Errorf("程序轮发了模型请求：%v", row.Requests)
						}
					}
				} else {
					if row.Requests[modelEndpoint] == 0 {
						t.Errorf("脚本轮没有模型调用：%v", row.Requests)
					}
					if row.Requests["openai/chat/v1/models"] != 0 || row.Requests["openai/v1/models"] != 0 {
						t.Errorf("配置探测记入业务请求：%v", row.Requests)
					}
				}
				switch sample.ID {
				case "fingerprint-only":
					if !row.Correct || row.Requests["acoustid/v2/lookup"] != len(sample.Local.Tracks) || row.Requests[lookup] != 1 ||
						!slices.Contains(row.MatchStatus, "matched/acoustid") {
						t.Errorf("每轮应独立查指纹及读取发行：%+v", row)
					}
					for track, want := range sample.Expect.Recordings {
						file := sample.Local.Tracks[track].File
						if got := row.Links[file+"/track"]; !slices.Equal(got, []string{want}) {
							t.Errorf("%s 声纹对应 %v，期望 %s", file, got, want)
						}
					}
				case "one-char":
					if mode == "script" {
						if !row.Correct || row.Requests["musicbrainz/release/search"] == 0 ||
							row.Requests[lookup] == 0 || !slices.Contains(row.MatchStatus, "matched/model") ||
							!slices.Equal(row.Links["专辑/release"], []string{sample.Expect.Release}) ||
							row.Writes["专辑/title"] != sample.External.Releases[0].Title {
							t.Errorf("正确名字应触发查询、匹配预期版本并纠正标题：%+v", row)
						}
					} else if row.Actual != "unlisted" || !slices.Equal(row.MatchStatus, []string{"unmatched/search"}) || len(row.Links)+len(row.Writes) != 0 {
						t.Errorf("程序应保留本地错字样本：%+v", row)
					}
				case "unlisted-01", "id-conflict":
					if row.Actual != row.Expected || len(row.Links)+len(row.Writes) != 0 ||
						report.Totals[mode].CorrectNegative[row.Expected] == 0 {
						t.Errorf("无收录或编号冲突应原样保留：%+v", row)
					}
				}
			})
		}
	}
}

func TestPostgreSQLCurationFixedPartitionReport(t *testing.T) {
	if os.Getenv(postgrestest.Environment) == "" {
		t.Skipf("%s is not configured", postgrestest.Environment)
	}
	if os.Getenv("ROOMUSIC_CURATION_METRICS_SAMPLES") == "" {
		t.Setenv("ROOMUSIC_CURATION_METRICS_SAMPLES", "mixed-a,mixed-b,mixed-contiguous")
	}
	samples := map[string]fixedSample{}
	for _, s := range fixedSamples(t, os.Getenv("ROOMUSIC_CURATION_METRICS_SAMPLES")) {
		samples[s.ID] = s
	}
	for _, id := range []string{"mixed-a", "mixed-b", "mixed-contiguous"} {
		if samples[id].ID == "" {
			t.Fatalf("拆并用例需要选中 %s", id)
		}
	}
	t.Run("shared-scan", func(t *testing.T) {
		app := newIntegrationTestApplication(t)
		admin := app.createUser(t, "admin")
		b3Root(t, app)
		a, b := samples["mixed-a"], samples["mixed-b"]
		for _, sample := range []fixedSample{a, b} {
			writeMetricSample(t, app.allowedRoot, sample.metricSample)
		}
		runScan(t, app, &admin)
		left, right := fixedScanPartition(t, app, a), fixedScanPartition(t, app, b)
		if len(left.ids) != 1 || len(right.ids) != 1 || left.ids[0] == right.ids[0] ||
			slices.Contains(left.tracks, "") || slices.Contains(right.tracks, "") {
			t.Errorf("一次混放目录扫描应留下两张独立且完整的专辑：%+v %+v", left, right)
		}
		b3RequireCount(t, app, 1, `SELECT count(*) FROM scan_runs WHERE status='succeeded'`)
	})
	t.Run("split", func(t *testing.T) {
		a, b := samples["mixed-a"], samples["mixed-b"]
		corpus := prepareFixedCorpus(t, []fixedSample{a, b})
		combined := a
		combined.Local.Tracks = append(slices.Clone(a.Local.Tracks), b.Local.Tracks...)
		combined.Expect.Recordings = append(slices.Clone(a.Expect.Recordings), b.Expect.Recordings...)
		combined.Expect.Artists = maps.Clone(a.Expect.Artists)
		maps.Copy(combined.Expect.Artists, b.Expect.Artists)
		report := fixedReport{}
		fixedBatch(t, "program", []fixedSample{combined}, corpus, &report)
		if len(report.Rows) != 1 {
			t.Fatalf("一个期望样本被拆开后应仍输出一行：%+v", report.Rows)
		}
		row := report.Rows[0]
		if !slices.Contains(row.ScanDiff, "预期一张，实得 2 张") || row.Correct ||
			!slices.Equal(row.Links["专辑/release"], []string{a.Expect.Release, b.Expect.Release}) &&
				!slices.Equal(row.Links["专辑/release"], []string{b.Expect.Release, a.Expect.Release}) {
			t.Errorf("拆分报告应保留两版的外部对应及扫描差异：%+v", row)
		}
		for _, sample := range []fixedSample{a, b} {
			for i, track := range sample.Local.Tracks {
				if got := row.Links[track.File+"/track"]; !slices.Equal(got, []string{sample.Expect.Recordings[i]}) {
					t.Errorf("拆分报告漏掉 %s 的录音：%v", track.File, got)
				}
			}
		}
	})
	t.Run("merged", func(t *testing.T) {
		merged := samples["mixed-contiguous"]
		corpus := prepareFixedCorpus(t, []fixedSample{merged})
		// 文件按真实连续曲号扫描为一张。两行各持有其中一半预期成员。
		a, b := merged, merged
		a.ID, b.ID = merged.ID+"-a", merged.ID+"-b"
		a.Local.Tracks, b.Local.Tracks = merged.Local.Tracks[:2], merged.Local.Tracks[2:]
		a.Expect.Recordings, b.Expect.Recordings = merged.Expect.Recordings[:2], merged.Expect.Recordings[2:]
		report := fixedReport{}
		fixedBatch(t, "program", []fixedSample{a, b}, corpus, &report)
		if len(report.Rows) != 2 {
			t.Fatalf("两张并为一张后仍应输出两行：%+v", report.Rows)
		}
		for i, sample := range []fixedSample{a, b} {
			row := report.Rows[i]
			if row.ID != sample.ID || row.Correct || !slices.ContainsFunc(row.Wrong, func(issue string) bool {
				return strings.HasPrefix(issue, "扫描错并 ") && strings.Contains(issue, merged.Local.Tracks[0].File) &&
					strings.Contains(issue, merged.Local.Tracks[3].File)
			}) {
				t.Errorf("错并报告应列出两张的成员：%+v", row)
			}
			for j, track := range sample.Local.Tracks {
				if got := row.Links[track.File+"/track"]; !slices.Equal(got, []string{sample.Expect.Recordings[j]}) {
					t.Errorf("错并报告漏掉 %s 的录音：%v", track.File, got)
				}
			}
		}
		first, second := report.Rows[0], report.Rows[1]
		if first.Requests["musicbrainz/release/lookup"] != 1 || first.requestCount() == 0 ||
			second.requestCount() != 0 || second.ElapsedMS != 0 || !slices.Contains(second.Issues, "请求归入 "+a.ID) {
			t.Errorf("同一实际发行应只由首行承担请求与用时：%+v %+v", first, second)
		}
	})
}

func TestPostgreSQLCurationFixedScoring(t *testing.T) {
	app := newIntegrationTestApplication(t)
	admin := app.createUser(t, "admin")
	b3Root(t, app)
	publishSearch(t, app)
	b3Enable(t, app, admin)
	selection := os.Getenv("ROOMUSIC_CURATION_METRICS_SAMPLES")
	if selection == "" {
		selection = "name-only-gap,one-char"
	}
	samples := map[string]fixedSample{}
	for _, s := range fixedSamples(t, selection) {
		samples[s.ID] = s
		writeMetricSample(t, app.allowedRoot, s.metricSample)
	}
	sample, negative := samples["name-only-gap"], samples["one-char"]
	if sample.ID == "" || negative.ID == "" {
		t.Fatal("评分用例需要选中 name-only-gap 与 one-char")
	}
	runScan(t, app, &admin)
	part := fixedScanPartition(t, app, sample)
	negativePart := fixedScanPartition(t, app, negative)
	if len(part.ids) != 1 || len(negativePart.ids) != 1 || slices.Contains(part.tracks, "") {
		t.Fatalf("扫描未得到完整的评分输入：%+v %+v", part, negativePart)
	}
	putLink := func(t *testing.T, kind, id, external string) {
		t.Helper()
		mustExec(t, app, `INSERT INTO external_links(id,source,object_type,object_id,external_id,method,confidence)
		VALUES($1,'musicbrainz',$2,$3,$4,'tag_id','high')
		ON CONFLICT(source,object_type,object_id) DO UPDATE SET external_id=excluded.external_id`, uuid.New(), kind, id, external)
	}
	putReleaseLinks := func(t *testing.T, s fixedSample, p fixedPartition) string {
		t.Helper()
		release := p.ids[0]
		group := programString(t, app, `SELECT group_id::text FROM releases WHERE id=$1`, release)
		putLink(t, "release", release, s.Expect.Release)
		putLink(t, "release_group", group, s.Expect.Group)
		for i, track := range p.tracks {
			putLink(t, "track", track, s.Expect.Recordings[i])
		}
		return group
	}
	writer := curation.NewWriter(app.database, app.dataDirectory, nil)
	writeFields := func(t *testing.T, release string, values map[string]any) {
		t.Helper()
		result, err := writer.Apply(t.Context(), autoRequest(release, "musicbrainz", "", values))
		requireCurationOK(t, err)
		if len(result.Written) != len(values) {
			t.Fatalf("评分夹具未写入全部字段 %v：%+v", values, result)
		}
	}
	release := part.ids[0]
	for _, tc := range []struct {
		name string
		run  func(*testing.T)
	}{
		{"safe-miss", func(t *testing.T) {
			requireCurationOK(t, curation.SaveWorkState(t.Context(), app.database, curation.WorkState{
				Subject: "release", TargetID: release, Status: "unmatched", InputDigest: "test",
			}))
			row := newFixedRow(sample, "program")
			row.score(t, app, sample, part, nil)
			if row.Actual != "unlisted" || row.Correct || len(row.Wrong) != 0 {
				t.Errorf("安全漏认应单独报告：%+v", row)
			}
		}},
		{"release-with-name-pending", func(t *testing.T) {
			putReleaseLinks(t, sample, part)
			values := map[string]any{}
			for k, value := range sample.Expect.Fields {
				values[k] = value
			}
			writeFields(t, release, values)
			row := newFixedRow(sample, "script")
			row.score(t, app, sample, part, nil)
			file := sample.Local.Tracks[0].File
			check := file + " 名字核对待写 " + sample.ScriptTrackFields[0]["title"]
			if !row.Correct || !slices.Contains(row.Issues, check) {
				t.Errorf("名字核对待写时，专辑已经正确识别：%+v，期望附带项 %q", row, check)
			}
			if current := programString(t, app, `SELECT title FROM track_effective WHERE id=$1`, part.tracks[0]); current != sample.Local.Tracks[0].Title {
				t.Errorf("待名字核对期间应保留标题 %q，实际 %q", sample.Local.Tracks[0].Title, current)
			}
		}},
		{"required-effective-values", func(t *testing.T) {
			putReleaseLinks(t, sample, part)
			target := sample
			target.Expect.TrackFields = []map[string]string{{"title": sample.Local.Tracks[0].Title}}
			file := sample.Local.Tracks[0].File
			artist := sample.Expect.Fields["album_artist"]
			label := sample.Expect.Fields["label"]
			score := func() fixedRow {
				t.Helper()
				row := newFixedRow(target, "program")
				row.score(t, app, target, part, nil)
				return row
			}

			// 扫描已有专辑艺人与曲名，厂牌的有效值为空，记为缺写。
			row := score()
			if row.Correct || len(row.Writes)+len(row.Wrong) != 0 || !slices.Equal(row.Missing, []string{"专辑/label"}) ||
				!maps.Equal(row.LocalExisting, map[string]string{"专辑/album_artist": artist, file + "/title": sample.Local.Tracks[0].Title}) {
				t.Errorf("扫描值、必需空值或报告分栏不符：%+v", row)
			}

			writeFields(t, release, map[string]any{"label": label})
			row = score()
			if !row.Correct || !maps.Equal(row.Writes, map[string]string{"专辑/label": label}) ||
				!maps.Equal(row.LocalExisting, map[string]string{"专辑/album_artist": artist, file + "/title": sample.Local.Tracks[0].Title}) {
				t.Errorf("本地与自动层共同满足必需字段时应认对：%+v", row)
			}

			endpoint := "/api/v1/releases/" + release + "/corrections"
			var state struct{ Revision int }
			getJSON(t, app, &admin, endpoint, &state)
			resp := app.request(t, "PATCH", endpoint, correctionBody(state.Revision, map[string]any{"label": setChange("人工厂牌")}),
				&admin, idempotencyHeader(uuid.New()))
			if resp.Code != 200 {
				t.Fatalf("人工覆盖厂牌失败：%d %s", resp.Code, resp.Body)
			}
			row = score()
			if row.Correct || row.Writes["专辑/label"] != label ||
				!slices.Equal(row.Wrong, []string{`专辑/label 错写 "人工厂牌"，预期 "弦湾唱片"`}) {
				t.Errorf("人工生效值错误时应认错，并保留自动层实际写入：%+v", row)
			}
			report := fixedReport{Rows: []fixedRow{row}, Totals: map[string]fixedTotal{}}
			report.summarize("program", 0)
			if report.Totals["program"].Wrong != 1 {
				t.Errorf("有效值错误未计入认错：%+v", report.Totals)
			}
		}},
		{"group", func(t *testing.T) {
			target := sample
			target.Program = "release_group"
			target.Expect = metricExpect{GroupMatch: sample.Expect.Group, Fields: map[string]string{"year": "2011"}}
			putLink(t, "release_group_match", release, target.Expect.GroupMatch)
			writeFields(t, release, map[string]any{"year": 2011})
			row := newFixedRow(target, "program")
			row.score(t, app, target, part, nil)
			if !row.Correct || len(row.Links) != 1 || row.Writes["专辑/year"] != "2011" {
				t.Errorf("正确组级匹配应只连组并写首发年：%+v", row)
			}
			putLink(t, "release_group_match", release, "wrong-group")
			row = newFixedRow(target, "program")
			row.score(t, app, target, part, nil)
			if row.Correct || len(row.Wrong) != 1 || !strings.Contains(row.Wrong[0], "专辑/release_group_match 实际 wrong-group") {
				t.Errorf("错组应单独列出：%+v", row)
			}
		}},
		{"wrong-release-recording-and-repeated-writes", func(t *testing.T) {
			group := putReleaseLinks(t, sample, part)
			putLink(t, "release", release, "wrong-release")
			putLink(t, "release_group", group, "wrong-group")
			putLink(t, "track", part.tracks[0], "wrong-recording")
			for _, track := range part.tracks[:2] {
				result, err := writer.ApplyTrack(t.Context(), curation.TrackRequest{
					ReleaseID: release, TrackID: track, Key: uuid.New(),
					Source: curation.Source{Kind: "source", Provider: "musicbrainz", Basis: "合成错写"},
					Fields: map[string]catalog.CorrectionIntent{"title": autoIntent("重复错写")},
				})
				requireCurationOK(t, err)
				if !slices.Equal(result.Written, []string{"title"}) {
					t.Fatalf("评分夹具未写入错值：%+v", result)
				}
			}
			row := newFixedRow(sample, "program")
			row.score(t, app, sample, part, nil)
			for _, prefix := range []string{"专辑/release 实际 wrong-release", "专辑/release_group 实际 wrong-group",
				sample.Local.Tracks[0].File + "/track 实际 wrong-recording",
				sample.Local.Tracks[0].File + `/title 错写 "重复错写"`, sample.Local.Tracks[1].File + `/title 错写 "重复错写"`} {
				if !slices.ContainsFunc(row.Wrong, func(issue string) bool { return strings.HasPrefix(issue, prefix) }) {
					t.Errorf("报告缺少 %q：%v", prefix, row.Wrong)
				}
			}
			report := fixedReport{Rows: []fixedRow{row}, Totals: map[string]fixedTotal{}}
			report.summarize("program", 0)
			if row.Correct || report.Totals["program"].Wrong != 1 {
				t.Errorf("同张多错应只计一次：%+v %+v", row, report.Totals)
			}
		}},
		{"negative-link", func(t *testing.T) {
			s := negative
			s.Expect = metricExpect{}
			putLink(t, "release", negativePart.ids[0], "foreign-release")
			row := newFixedRow(s, "program")
			row.score(t, app, s, negativePart, nil)
			if row.Correct || len(row.Wrong) != 1 || !strings.Contains(row.Wrong[0], "专辑/release 实际 foreign-release") {
				t.Errorf("程序负例建立外部对应应计为错认：%+v", row)
			}
		}},
		{"negative-link-allowed-only-in-script", func(t *testing.T) {
			s := negative
			putReleaseLinks(t, s, negativePart)
			row := newFixedRow(s, "program")
			row.score(t, app, s, negativePart, nil)
			if row.Correct || !slices.ContainsFunc(row.Wrong, func(issue string) bool {
				return strings.HasPrefix(issue, "专辑/release 实际 "+s.Expect.Release)
			}) {
				t.Errorf("程序预期未匹配，建立只在脚本模式允许的外部版 %s 应计为错认：%+v", s.Expect.Release, row)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(func() {
				mustExec(t, app, `DELETE FROM external_links`)
				mustExec(t, app, `DELETE FROM release_auto_values`)
				mustExec(t, app, `DELETE FROM release_corrections`)
				mustExec(t, app, `DELETE FROM track_auto_values`)
				mustExec(t, app, `DELETE FROM curation_work_states`)
			})
			tc.run(t)
		})
	}
}
