package app

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestFixedSampleInventory(t *testing.T) {
	samples := map[string]fixedSample{}
	for _, s := range fixedSamples(t, "") {
		if s.ID == "" || samples[s.ID].ID != "" {
			t.Fatalf("样本 ID 缺失或重复 %q", s.ID)
		}
		samples[s.ID] = s
		if s.Local.Directory == "" || len(s.Local.Tracks) == 0 {
			t.Errorf("%s 缺少本地成员", s.ID)
		}
		if s.Program == "release" || s.Script == "release" {
			i := slices.IndexFunc(s.External.Releases, func(r metricRelease) bool { return r.ID == s.Expect.Release })
			if i < 0 {
				t.Errorf("%s 的预期版本 %s 不在模拟目录中", s.ID, s.Expect.Release)
			} else {
				release := s.External.Releases[i]
				recordings := make([]string, 0, len(release.Tracks))
				for _, track := range release.Tracks {
					recordings = append(recordings, track.ID)
				}
				if release.Group != s.Expect.Group || !slices.Equal(recordings, s.Expect.Recordings) {
					t.Errorf("%s 的预期组或逐轨录音与目录不符：%s %v", s.ID, release.Group, recordings)
				}
			}
		}
		for mode, outcome := range map[string]string{"program": s.Program, "script": s.Script} {
			switch outcome {
			case "release":
				if s.Expect.Release == "" || s.Expect.Group == "" || len(s.Expect.Recordings) != len(s.Local.Tracks) ||
					slices.Contains(s.Expect.Recordings, "") || s.Expect.Fields == nil && s.Source != "disc-folders" {
					t.Errorf("%s/%s 的版、组、逐轨录音或写入约束缺失：%+v", mode, s.ID, s.Expect)
				}
			case "release_group":
				if s.Expect.GroupMatch == "" || s.Expect.Group != s.Expect.GroupMatch || s.Expect.Release != "" ||
					len(s.Expect.Recordings) != 0 || len(s.Expect.Fields) == 0 {
					t.Errorf("%s/%s 的组级写入约束无效：%+v", mode, s.ID, s.Expect)
				}
			case "unlisted", "conflict", "skip":
				if s.Program == s.Script && (s.Expect.Release != "" || s.Expect.Group != "" || s.Expect.GroupMatch != "" ||
					len(s.Expect.Recordings)+len(s.Expect.Fields)+len(s.Expect.TrackFields) != 0) {
					t.Errorf("%s/%s 负例声明了外部对应或写入：%+v", mode, s.ID, s.Expect)
				}
			default:
				t.Errorf("%s/%s 缺少有效答案：%q", mode, s.ID, outcome)
			}
		}
	}
	for _, id := range []string{
		"editions-model-quartet", "embedded-artist", "one-char", "romanized", "legacy-one", "legacy-many",
		"sampler-five", "disc-folders", "named-bonus", "filename-only", "name-only-gap", "mixed-contiguous", "mixed-a", "mixed-b",
		"amp-comma", "picard-01", "cue-01", "identifier-01", "catalog-label", "isrc-only", "fingerprint-only",
		"id-conflict", "unlisted-01", "spoken-podcast", "unknown-disc-a", "unknown-disc-b", "missing-position", "field-tie",
	} {
		if samples[id].ID == "" {
			t.Errorf("固定清单缺少 %s", id)
		}
	}
	quartet := samples["editions-model-quartet"]
	editions := map[string]bool{}
	for _, r := range quartet.External.Releases {
		if r.ID == "" || editions[r.ID] || r.Group != quartet.Expect.GroupMatch || len(r.Tracks) != len(quartet.Local.Tracks) {
			t.Errorf("同组等曲数发行约束无效：%+v", r)
		}
		editions[r.ID] = true
	}
	if len(editions) < 4 {
		t.Errorf("同组样本只有 %d 版", len(editions))
	}
	for id, tag := range map[string]string{
		"picard-01": "MUSICBRAINZ_ALBUMID", "identifier-01": "BARCODE", "catalog-label": "CATALOGNUMBER", "id-conflict": "MUSICBRAINZ_ALBUMID",
	} {
		if samples[id].Local.Tags[tag] == "" {
			t.Errorf("%s 缺少 %s", id, tag)
		}
	}
	if !samples["cue-01"].Local.Cue || samples["fingerprint-only"].Local.Format != "fingerprint" {
		t.Error("DiscID 或声纹样本缺少相应输入")
	}
	if tracks := samples["isrc-only"].Local.Tracks; len(tracks) == 0 ||
		slices.ContainsFunc(tracks, func(tr metricLocalTrack) bool { return tr.Tags["ISRC"] == "" }) {
		t.Error("ISRC 样本缺少逐轨编号")
	}

	// A1.5 中同名目录的两组成员来自不同文件，未知碟名各有目录。
	a, b := samples["mixed-a"], samples["mixed-b"]
	for _, pair := range [][2]fixedSample{{a, b}, {samples["unknown-disc-a"], samples["unknown-disc-b"]}} {
		first, second := pair[0], pair[1]
		if first.Local.Album == "" || first.Local.Album == second.Local.Album ||
			strings.Split(first.Local.Directory, "/")[0] != strings.Split(second.Local.Directory, "/")[0] {
			t.Errorf("%s 与 %s 应共用扫描批次且有不同专辑标签", first.ID, second.ID)
		}
	}
	if a.Local.Directory != b.Local.Directory || len(a.Local.Tracks) == 0 || len(b.Local.Tracks) == 0 ||
		a.Local.Tracks[0].File == b.Local.Tracks[0].File || a.Local.Tracks[0].Tags["TRACKNUMBER"] != b.Local.Tracks[0].Tags["TRACKNUMBER"] {
		t.Error("混放样本应来自同一目录、文件不同且曲号重叠")
	}
	if samples["unknown-disc-a"].Local.Directory == samples["unknown-disc-b"].Local.Directory {
		t.Error("未知碟目录样本应来自两个不同子目录")
	}
	if s := samples["filename-only"]; s.Local.Album != "" || s.Local.Artist != "" || len(s.ExpectedTitles) != len(s.Local.Tracks) ||
		slices.ContainsFunc(s.Local.Tracks, func(tr metricLocalTrack) bool { return tr.Title != "" || tr.Artist != "" || len(tr.Tags) != 0 }) {
		t.Error("仅文件名样本应只用文件路径并逐轨声明原样标题")
	}
	if s := samples["legacy-many"]; s.Local.LegacyAlbumHex == "" || s.Expect.Scan == nil || len(s.Expect.Scan.ForbiddenQuery) == 0 {
		t.Error("多解旧编码样本缺少原始字节或禁止查询的内容")
	}
	if artist := samples["amp-comma"].Local.Artist; !strings.Contains(artist, "&") || !strings.Contains(artist, ",") {
		t.Error("署名样本缺少 & 或逗号")
	}
	if tracks := samples["missing-position"].Local.Tracks; !slices.ContainsFunc(tracks, func(tr metricLocalTrack) bool {
		return tr.Tags["TRACKNUMBER"] == "" && tr.Tags["DISCNUMBER"] == ""
	}) {
		t.Error("缺编号样本应有无碟号与曲号的音轨")
	}
	years := map[string]int{}
	for _, tr := range samples["field-tie"].Local.Tracks {
		years[tr.Tags["DATE"]]++
	}
	if len(years) != 2 || years[""] != 0 || slices.ContainsFunc(slices.Collect(maps.Values(years)), func(n int) bool { return n != 2 }) {
		t.Errorf("年份打平样本应有两对不同年份：%v", years)
	}
}

func TestFixedReportSummary(t *testing.T) {
	report := fixedReport{Rows: []fixedRow{
		{ID: "right-release", Mode: "program", Expected: "release", Actual: "release", Correct: true, Requests: map[string]int{"musicbrainz/release/lookup": 1}},
		{ID: "right-group", Mode: "program", Expected: "release_group", Actual: "release_group", Correct: true, Requests: map[string]int{"musicbrainz/release/search": 2}},
		{ID: "safe-miss", Mode: "program", Expected: "release", Actual: "unlisted", Requests: map[string]int{"musicbrainz/release/search": 1}},
		{ID: "no-query", Mode: "program", Expected: "skip", Actual: "skip"},
		{ID: "unknown", Mode: "program", Expected: "unlisted", Actual: "unlisted", Requests: map[string]int{"musicbrainz/release/search": 1}},
		{ID: "conflict", Mode: "program", Expected: "conflict", Actual: "conflict", Requests: map[string]int{"musicbrainz/release/lookup": 1}},
		{ID: "multiple-errors", Mode: "program", Expected: "release", Actual: "release", Wrong: []string{"错版", "错组", "错写厂牌", "错写厂牌"}},
		{ID: "wrong-negative", Mode: "program", Expected: "unlisted", Actual: "release", Wrong: []string{"负例错连"}},
		{ID: "missing-field", Mode: "program", Expected: "release", Actual: "release", Missing: []string{"专辑/label"}},
		{ID: "unknown", Mode: "script", Expected: "unlisted", Actual: "unlisted", Requests: map[string]int{"openai/chat/v1/chat/completions": 3}},
	}, Totals: map[string]fixedTotal{}}
	report.summarize("program", 140*time.Millisecond)
	report.summarize("script", 200*time.Millisecond)
	want := map[string]fixedTotal{
		"program": {Correct: 2, Wrong: 2, Count: 6, Mean: 6.0 / 9, ElapsedMS: 140,
			Outcomes:        map[string]int{"release": 4, "release_group": 1, "skip": 1, "unlisted": 2, "conflict": 1},
			CorrectNegative: map[string]int{"skip": 1, "unlisted": 1, "conflict": 1}},
		"script": {Count: 3, Mean: 3, ElapsedMS: 200, Outcomes: map[string]int{"unlisted": 1}, CorrectNegative: map[string]int{"unlisted": 1}},
	}
	if !reflect.DeepEqual(report.Totals, want) {
		t.Errorf("汇总 %+v，期望 %+v", report.Totals, want)
	}
}

func TestFixedReportGate(t *testing.T) {
	const modeEnv = "ROOMUSIC_TEST_FIXED_GATE_CASE"
	const known = `专辑/release 实际 wrong-release，预期 "right-release"`
	const added = `专辑/release 实际 another-release，预期 "right-release"`
	if testCase := os.Getenv(modeEnv); testCase != "" {
		t.Setenv("ROOMUSIC_CURATION_METRICS_OUT", "")
		t.Setenv("ROOMUSIC_CURATION_METRICS_SAMPLES", "")
		oldRow := fixedRow{ID: "synthetic", Mode: "program", Requests: map[string]int{"musicbrainz/release/lookup": 1}, ElapsedMS: 10, Wrong: []string{known}}
		scriptRow := fixedRow{ID: "synthetic", Mode: "script"}
		fixedSave(t, filepath.Join(fixedData, "fixed-baseline.json"), fixedReport{Rows: []fixedRow{oldRow, scriptRow}})
		row := oldRow
		row.Requests = map[string]int{"musicbrainz/release/lookup": 2}
		row.ElapsedMS = 13
		switch testCase {
		case "new-value":
			row.Wrong = []string{known, added}
		case "new-mode":
			row.Mode = "script"
		}
		(&fixedReport{Rows: []fixedRow{row}}).finish(t, "fixed")
		return
	}
	// 在独立测试进程核对真实退出状态，基线写入该进程的临时目录。
	binary, err := os.Executable()
	requireCurationOK(t, err)
	for _, tc := range []struct {
		name, message string
		exitCode      int
	}{
		{"known", "基线已有错认 [🔴 program/synthetic " + known, 0},
		{"new-value", "新增错认 [🔴 program/synthetic " + added, 1},
		{"new-mode", "新增错认 [🔴 script/synthetic " + known, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), binary, "-test.run=^TestFixedReportGate$", "-test.v")
			cmd.Dir = t.TempDir()
			cmd.Env = append(os.Environ(), modeEnv+"="+tc.name)
			out, err := cmd.CombinedOutput()
			var exitErr *exec.ExitError
			if tc.exitCode == 0 && err != nil || tc.exitCode != 0 && (!errors.As(err, &exitErr) || exitErr.ExitCode() != tc.exitCode) {
				t.Fatalf("退出状态 %v，期望 %d\n%s", err, tc.exitCode, out)
			}
			if !strings.Contains(string(out), tc.message) {
				t.Errorf("缺少 %q\n%s", tc.message, out)
			}
			if tc.name == "known" && (!strings.Contains(string(out), "新增错认 0") ||
				!strings.Contains(string(out), "请求变化 +1，用时变化 +3 ms")) {
				t.Errorf("缺少基线差值或新增错认计数\n%s", out)
			}
		})
	}
}

func TestLegacyMetricFixtureCompatibility(t *testing.T) {
	samples := loadMetricSamples(t)
	i := slices.IndexFunc(samples, func(s metricSample) bool { return s.ID == "legacy-latin1" })
	if i == -1 || len(samples[i].Local.Tracks) == 0 {
		t.Fatal("旧 Latin-1 样本不存在")
	}
	sample := samples[i]
	track := sample.Local.Tracks[0]
	track.Tags = maps.Clone(track.Tags)
	delete(track.Tags, "TRACKNUMBER")
	audio := []byte{0xff, 0xfb, 0x90, 0x64}
	data := metricLegacyMP3(t, audio, sample, track)
	frame := bytes.Index(data, []byte("TRCK"))
	if frame < 10 || len(data) < frame+12 {
		t.Fatalf("旧 ID3v2.3 样本缺少 TRCK 帧：%x", data)
	}
	if size, value := binary.BigEndian.Uint32(data[frame+4:frame+8]), data[frame+10:frame+12]; size != 2 || !bytes.Equal(value, []byte{0, '1'}) {
		t.Errorf("TRCK 帧长度 %d，值 %x，期望 Latin-1 文本 1", size, value)
	}
	if !bytes.HasSuffix(data, audio) {
		t.Error("追加 ID3 标签改变了音频字节")
	}
	catalog, err := os.ReadFile(filepath.Join(writeMetricCatalog(t, []metricSample{sample}), "catalog.json"))
	requireCurationOK(t, err)
	var fields map[string]json.RawMessage
	requireCurationOK(t, json.Unmarshal(catalog, &fields))
	if _, ok := fields["fingerprints"]; ok {
		t.Errorf("旧样本无声纹映射，目录仍生成了 fingerprints：%s", catalog)
	}
}
