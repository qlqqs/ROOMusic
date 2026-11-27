package app

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
)

type fixedRow struct {
	ID            string              `json:"id"`
	Mode          string              `json:"mode"`
	Expected      string              `json:"expected"`
	Actual        string              `json:"actual"`
	MatchStatus   []string            `json:"program_status,omitempty"`
	Links         map[string][]string `json:"external_ids,omitempty"`
	Writes        map[string]string   `json:"writes,omitempty"`
	LocalExisting map[string]string   `json:"local_existing,omitempty"`
	Issues        []string            `json:"issues,omitempty"`
	Wrong         []string            `json:"wrong,omitempty"`
	ScanDiff      []string            `json:"scan_diff,omitempty"`
	Missing       []string            `json:"missing,omitempty"`
	Requests      map[string]int      `json:"requests_by_endpoint"`
	ElapsedMS     int64               `json:"elapsed_ms"`
	Correct       bool                `json:"correct"`
}

func newFixedRow(s fixedSample, mode string) fixedRow {
	expected := s.Program
	if mode == "script" {
		expected = s.Script
	}
	return fixedRow{ID: s.ID, Mode: mode, Expected: expected, Links: map[string][]string{}, Writes: map[string]string{}, LocalExisting: map[string]string{}}
}
func (r fixedRow) requestCount() int {
	sum := 0
	for _, n := range r.Requests {
		sum += n
	}
	return sum
}

func (r *fixedRow) score(t *testing.T, app *integrationTestApplication, s fixedSample, part fixedPartition, queries []metricSourceQuery) {
	links := metricStoredLinks(t, app)
	fields := metricStoredFields(t, app)
	evidence, err := catalog.LoadCurationEvidence(t.Context(), app.database, part.ids)
	requireCurationOK(t, err)
	want := s.Expect
	sawMB := false
	for k := range r.Requests {
		if strings.HasPrefix(k, "musicbrainz/") || strings.HasPrefix(k, "acoustid/") {
			sawMB = true
		}
	}
	found := map[string]bool{}
	if len(part.ids) != 1 {
		r.ScanDiff = append(r.ScanDiff, fmt.Sprintf("预期一张，实得 %d 张", len(part.ids)))
	}
	// 每个外部标识和实际写入都带上稳定的本地成员名；同张出现新的错值仍是新增错认。
	checkLinks := func(target, kind, id, expected string, required bool) {
		if r.Expected != "release" && r.Expected != "release_group" {
			expected = ""
		}
		rows := links[metricLinkSubject{kind, id}]
		for _, v := range rows {
			key := target + "/" + kind
			r.Links[key] = append(r.Links[key], v.value)
			if expected == "" || v.value != expected || v.sourceID != "musicbrainz" {
				r.Wrong = append(r.Wrong, fmt.Sprintf("%s 实际 %s，预期 %q", key, v.value, expected))
			}
		}
		if required && expected != "" && len(rows) == 0 {
			r.Missing = append(r.Missing, target+"/"+kind)
		}
	}
	checkFields := func(target string, subject metricObject, required, optional map[string]string) {
		actual := fields[subject.id]
		for k, v := range actual {
			r.Writes[target+"/"+k] = v.value
			if _, needed := required[k]; !needed && optional[k] != v.value && v.value != "" {
				r.Wrong = append(r.Wrong, fmt.Sprintf("%s/%s 错写 %q", target, k, v.value))
			}
		}
		if len(required) == 0 {
			return
		}
		effective := metricEffectiveFields(t, app, subject, slices.Sorted(maps.Keys(required)))
		for k, expected := range required {
			key := target + "/" + k
			value := effective[k]
			switch {
			case !value.Valid:
				r.Missing = append(r.Missing, key)
			case value.String != expected:
				r.Wrong = append(r.Wrong, fmt.Sprintf("%s 错写 %q，预期 %q", key, value.String, expected))
			default:
				if _, written := actual[k]; !written {
					r.LocalExisting[key] = value.String
				}
			}
		}
	}
	for _, id := range part.ids {
		e := evidence[id]
		status := b3WorkState(t, app, "release", id)
		var m struct {
			Conflicts    []string
			Method, Rule string
		}
		if len(status.Result) > 0 {
			requireCurationOK(t, json.Unmarshal(status.Result, &m))
		}
		r.MatchStatus = append(r.MatchStatus, strings.Join(append([]string{status.Status, m.Method}, m.Conflicts...), "/"))
		actual := ""
		switch {
		case len(links[metricLinkSubject{"release", id}]) > 0:
			actual = "release"
		case len(links[metricLinkSubject{"release_group_match", id}]) > 0:
			actual = "release_group"
		case status.Status == "try_later" || status.Status == "error" || status.Status == "pending":
			actual = status.Status
		case len(m.Conflicts) == 1 && m.Conflicts[0] == "too_many":
			actual = "候选过多"
		case len(m.Conflicts) > 0 && !slices.Contains(m.Conflicts, "insufficient_evidence"):
			actual = "conflict"
		case status.Status == "unmatched" || sawMB:
			actual = "unlisted"
		default:
			actual = "skip"
		}
		found[actual] = true
		foreignMember := slices.ContainsFunc(e.Tracks, func(tr catalog.CurationTrack) bool { return !slices.Contains(part.tracks, tr.ID) })
		if foreignMember {
			r.Wrong = append(r.Wrong, "扫描错并 "+strings.Join(part.members[id], "、"))
		}
		release := actual == "release"
		group := actual == "release_group"
		checkLinks("专辑", "release", id, want.Release, release)
		checkLinks("专辑", "release_group_match", id, want.GroupMatch, group)
		checkLinks("专辑", "release_group", e.GroupID, want.Group, release)
		required := map[string]string{}
		optional := map[string]string{}
		maps.Copy(optional, s.OptionalFields)
		if release || group {
			maps.Copy(required, want.Fields)
		}
		if r.Mode == "script" {
			maps.Copy(optional, s.OptionalScriptFields)
		}
		checkFields("专辑", metricObject{"release", id}, required, optional)
		if s.ID == "named-bonus" {
			titles := programString(t, app, `SELECT coalesce(string_agg(title, ' / ' ORDER BY position),'') FROM media WHERE release_id=$1`, id)
			if !strings.Contains(titles, "初声") || !strings.Contains(titles, "夜声") {
				r.ScanDiff = append(r.ScanDiff, "碟标题："+titles)
			}
		}
		if len(part.ids) == 1 {
			entry := metricEntry{sample: s.metricSample, release: id, tracks: part.tracks, queries: queries}
			for _, issue := range metricFixtureIssues(t, app, entry) {
				r.ScanDiff = append(r.ScanDiff, fmt.Sprintf("%s 期望 %s，实际 %s", issue.Field, issue.Expected, issue.Actual))
			}
		}
		for i, track := range part.tracks {
			if track == "" {
				r.ScanDiff = append(r.ScanDiff, "未扫到 "+s.Local.Tracks[i].File)
				continue
			}
			var tr *catalog.CurationTrack
			for n := range e.Tracks {
				if e.Tracks[n].ID == track {
					tr = &e.Tracks[n]
					break
				}
			}
			if tr == nil {
				continue
			}
			file := s.Local.Tracks[i].File
			if file == "" {
				file = fmt.Sprintf("CUE/%02d", i+1)
			}
			expected := ""
			if i < len(want.Recordings) {
				expected = want.Recordings[i]
			}
			checkLinks(file, "track", track, expected, release)
			tf := map[string]string{}
			if release && i < len(want.TrackFields) {
				maps.Copy(tf, want.TrackFields[i])
			}
			// 名字差异附带项独立记账，正确发行的完成不等待此项。
			extra := map[string]string{}
			if r.Mode == "script" && i < len(s.ScriptTrackFields) {
				maps.Copy(extra, s.ScriptTrackFields[i])
				for k, v := range extra {
					if fields[track][k].value != v {
						r.Issues = append(r.Issues, file+" 名字核对待写 "+v)
					}
				}
			}
			checkFields(file, metricObject{"track", track}, tf, extra)
			if i < len(s.ExpectedTitles) && tr.Title != s.ExpectedTitles[i] {
				r.ScanDiff = append(r.ScanDiff, file+" 标题 "+tr.Title)
			}
			// 已知偏差，暂不处理：缺曲号时 PositionInferred 也为真（curation_evidence.go），
			// 产品按合同不写曲号（1/0）仍会记一条差异，所以本样本不计入正确负例。
			if s.ID == "missing-position" && (tr.Disc != 1 || i == 3 && tr.PositionInferred) {
				r.ScanDiff = append(r.ScanDiff, fmt.Sprintf("%s 碟/轨 %d/%d，推断=%t", file, tr.Disc, tr.Position, tr.PositionInferred))
			}
		}
		if s.ID == "field-tie" && e.Year != 0 {
			r.ScanDiff = append(r.ScanDiff, fmt.Sprintf("打平年份被选为 %d", e.Year))
		}
		credits, err := catalog.LoadCurationArtistCredits(t.Context(), app.database, id)
		requireCurationOK(t, err)
		byName := map[string]string{}
		for _, list := range append([][]catalog.CurationArtistCredit{credits.ReleaseArtists, credits.AlbumArtists}, slices.Collect(maps.Values(credits.Tracks))...) {
			for _, c := range list {
				byName[c.Name] = c.ArtistID
			}
		}
		for name, aid := range byName {
			checkLinks(name, "artist", aid, want.Artists[name], false)
		}
		if s.ID == "amp-comma" && len(byName) != 1 {
			r.ScanDiff = append(r.ScanDiff, fmt.Sprintf("署名 %v", slices.Sorted(maps.Keys(byName))))
		}
		if s.NoDetail {
			if n := r.Requests["musicbrainz/release/lookup"]; n > 0 {
				r.Issues = append(r.Issues, fmt.Sprintf("读版详情 %d 次", n))
			}
			if countRows(t, app, `SELECT count(*) FROM curation_pending_items WHERE target_id=$1 AND kind='release_match' AND conversation_id IS NOT NULL`, id) > 0 {
				r.Issues = append(r.Issues, "进入模型识别")
			}
		}
	}
	r.Actual = strings.Join(slices.Sorted(maps.Keys(found)), "+")
	if r.Actual == "" {
		r.Actual = "未扫描"
	}
	for _, items := range []*[]string{&r.Missing, &r.Wrong, &r.Issues, &r.ScanDiff, &r.MatchStatus} {
		slices.Sort(*items)
		*items = slices.Compact(*items)
	}
	r.Correct = (r.Actual == "release" || r.Actual == "release_group") && r.Actual == r.Expected && len(r.Missing) == 0 && len(r.Wrong) == 0 && len(r.ScanDiff) == 0
}

type fixedTotal struct {
	Correct         int            `json:"correct"`
	Wrong           int            `json:"wrong"`
	Count           int            `json:"requests"`
	Mean            float64        `json:"requests_per_sample"`
	ElapsedMS       int64          `json:"elapsed_ms"`
	Outcomes        map[string]int `json:"outcomes"`
	CorrectNegative map[string]int `json:"correct_negative"`
}
type fixedReport struct {
	Rows   []fixedRow            `json:"samples"`
	Totals map[string]fixedTotal `json:"totals"`
}

func (r *fixedReport) summarize(mode string, elapsed time.Duration) {
	sum := fixedTotal{ElapsedMS: elapsed.Milliseconds(), Outcomes: map[string]int{}, CorrectNegative: map[string]int{}}
	n := 0
	for _, s := range r.Rows {
		if s.Mode != mode {
			continue
		}
		n++
		sum.Count += s.requestCount()
		sum.Outcomes[s.Actual]++
		if s.Actual == s.Expected && (s.Actual == "skip" || s.Actual == "unlisted" || s.Actual == "conflict") && len(s.ScanDiff) == 0 && len(s.Wrong) == 0 {
			sum.CorrectNegative[s.Actual]++
		}
		if s.Correct {
			sum.Correct++
		}
		if len(s.Wrong) > 0 {
			sum.Wrong++
		}
	}
	sum.Mean = float64(sum.Count) / float64(n)
	r.Totals[mode] = sum
}
func fixedOwner(issue string) string {
	switch {
	case strings.HasPrefix(issue, "扫描错并"):
		return "子任务 3（归组）"
	case strings.Contains(issue, "错写") && strings.Contains(issue, "title"):
		return "子任务 8、9（本地辅助与名字核对）"
	default:
		return "子任务 5（程序识别与写入）"
	}
}
func (r *fixedReport) finish(t *testing.T, suite string) {
	baseline := fixedReport{}
	if suite == "fixed" {
		raw, err := os.ReadFile(path.Join(fixedData, "fixed-baseline.json"))
		requireCurationOK(t, err)
		requireCurationOK(t, json.Unmarshal(raw, &baseline))
	}
	previous := map[string]fixedRow{}
	for _, s := range baseline.Rows {
		previous[s.Mode+"/"+s.ID] = s
	}
	var added, known []string
	for _, s := range r.Rows {
		key := s.Mode + "/" + s.ID
		old, ok := previous[key]
		if suite == "fixed" && !ok {
			t.Errorf("基线缺少 %s", key)
		}
		row, err := json.Marshal(s)
		requireCurationOK(t, err)
		t.Logf("样本 %s", row)
		if ok {
			t.Logf("%s 请求变化 %+d，用时变化 %+d ms", key, s.requestCount()-old.requestCount(), s.ElapsedMS-old.ElapsedMS)
		}
		for _, issue := range s.Wrong {
			item := fmt.Sprintf("🔴 %s %s [%s]", key, issue, fixedOwner(issue))
			if suite == "fixed" && !slices.Contains(old.Wrong, issue) {
				added = append(added, item)
			} else {
				known = append(known, item)
			}
		}
	}
	for _, mode := range []string{"program", "script"} {
		v := r.Totals[mode]
		t.Logf("%s 认对 %d，认错 %d，每张请求 %.2f，用时 %d ms，结论 %v，正确负例 %v", mode, v.Correct, v.Wrong, v.Mean, v.ElapsedMS, v.Outcomes, v.CorrectNegative)
		if old, ok := baseline.Totals[mode]; ok && os.Getenv("ROOMUSIC_CURATION_METRICS_SAMPLES") == "" {
			t.Logf("%s 相对基线：认对 %+d，认错 %+d，每张请求 %+.2f，用时 %+d ms", mode, v.Correct-old.Correct, v.Wrong-old.Wrong, v.Mean-old.Mean, v.ElapsedMS-old.ElapsedMS)
		}
	}
	if file := os.Getenv("ROOMUSIC_CURATION_METRICS_OUT"); file != "" {
		fixedSave(t, file, r)
	}
	t.Logf("基线已有错认 %v", known)
	if len(added) > 0 {
		t.Errorf("新增错认 %v", added)
	} else {
		t.Log("新增错认 0")
	}
}
