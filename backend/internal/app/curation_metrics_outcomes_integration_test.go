package app

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/curation"
)

type metricSuggestion struct {
	ID        string          `json:"id"`
	Target    string          `json:"target_id"`
	Tool      string          `json:"tool"`
	Field     string          `json:"field"`
	Value     json.RawMessage `json:"value"`
	CanAccept bool            `json:"can_accept"`
}

func metricSuggestionDetails(t *testing.T, item curation.ModelException, owner string) []metricSuggestion {
	t.Helper()
	row := metricSuggestion{ID: item.ID, Target: owner, Tool: item.Tool, CanAccept: item.CanAccept}
	if item.Track != nil {
		row.Target = item.Track.ID
	}
	var preview struct {
		Changes []struct {
			Field string
			After json.RawMessage
		}
	}
	// 无字段变更的观察也占一条，字段为空，值为 null。
	if len(item.Preview) == 0 || item.Preview[0] != '{' {
		return []metricSuggestion{row}
	}
	requireCurationOK(t, json.Unmarshal(item.Preview, &preview))
	if len(preview.Changes) == 0 {
		return []metricSuggestion{row}
	}
	var details []metricSuggestion
	for _, change := range preview.Changes {
		row.Field, row.Value = change.Field, change.After
		details = append(details, row)
	}
	return details
}

func metricSuggestions(t *testing.T, app *integrationTestApplication) map[string][]metricSuggestion {
	t.Helper()
	found := map[string][]metricSuggestion{}
	writer := curation.NewWriter(app.database, app.dataDirectory, nil)
	for page := 1; ; page++ {
		items, total, err := writer.ListExceptions(t.Context(), "open", page, 100)
		requireCurationOK(t, err)
		for _, item := range items {
			var owner string
			if item.Release != nil {
				owner = item.Release.ID
			} else if item.Artist != nil {
				owner = item.Artist.ID
			}
			found[owner] = append(found[owner], metricSuggestionDetails(t, item, owner)...)
		}
		if int64(page*100) >= total {
			return found
		}
	}
}

type suggestionAnswers map[string]map[string][]string

func (row metricSuggestion) answer() string {
	var value string
	if len(row.Value) > 0 && row.Value[0] == '"' && json.Unmarshal(row.Value, &value) == nil {
		return value
	}
	if row.Tool == "resolve_artist_identity" && row.Field == "artist_link" {
		var entry curation.ArtistLinkPreview
		if json.Unmarshal(row.Value, &entry) == nil {
			return entry.Source + "/" + entry.ExternalID
		}
	}
	return string(row.Value)
}

func metricSuggestionIssues(sample string, releases []string, w metricExpect, rows []metricSuggestion, answers suggestionAnswers) []metricIssue {
	found := map[string][]string{}
	var issues []metricIssue
	for _, row := range rows {
		value := row.answer()
		if slices.Contains(releases, row.Target) {
			found[row.Field] = append(found[row.Field], value)
		}
		if w.SuggestionCanAccept != nil && row.CanAccept != *w.SuggestionCanAccept {
			issues = append(issues, metricIssue{Kind: "suggestion_can_accept", Target: sample, Field: row.Field, Expected: fmt.Sprint(*w.SuggestionCanAccept), Actual: fmt.Sprint(row.CanAccept)})
			continue
		}
		allowed := answers[row.Target][row.Field]
		if w.allowsMatch() && row.CanAccept && (value == "" || !slices.Contains(allowed, value)) {
			issues = append(issues, metricIssue{Kind: "suggestion_value", Target: sample, Field: row.Field, Expected: strings.Join(allowed, " 或 "), Actual: value})
		}
	}
	for _, field := range slices.Sorted(maps.Keys(w.Suggestions)) {
		want := w.Suggestions[field]
		if !slices.Contains(found[field], want) {
			issues = append(issues, metricIssue{Kind: "missing_suggestion", Target: sample, Field: field, Expected: want, Actual: strings.Join(found[field], "、")})
		}
	}
	return issues
}

func metricCreditName(artists map[string]string, artist string, credits []metricCredit) string {
	if len(credits) == 0 {
		return artists[artist]
	}
	var b strings.Builder
	for _, credit := range credits {
		b.WriteString(artists[credit.Artist])
		b.WriteString(credit.JoinPhrase)
	}
	return b.String()
}

func metricSampleAnswers(e metricEntry) suggestionAnswers {
	w := e.sample.Expect
	fields := map[string][]string{"external_link": {w.Release}, "group_external_link": {w.Group}, "release_group_match": {w.GroupMatch, w.Group}}
	if len(w.Recordings) > 0 {
		fields["track_links"] = []string{fmt.Sprint(len(w.Recordings))}
	}
	artists := map[string]string{}
	recordings := map[string]metricRecording{}
	for _, artist := range e.sample.External.Artists {
		artists[artist.ID] = artist.Name
	}
	for _, r := range e.sample.External.Releases {
		for _, track := range r.Tracks {
			recordings[track.ID] = track
		}
		if r.ID == w.Release {
			fields["title"] = []string{r.Title}
			fields["album_artist"] = []string{metricCreditName(artists, r.Artist, r.Credits)}
			fields["label"] = []string{r.Label}
			fields["catalog_number"] = []string{r.Catalog}
			fields["barcode"] = []string{r.Barcode}
			if len(r.Date) >= 4 {
				fields["year"] = []string{r.Date[:4]}
			}
		} else if w.Release == "" && r.Group == w.GroupMatch && r.GroupTitle != "" {
			fields["title"] = []string{r.GroupTitle}
		}
	}
	for field, value := range w.Suggestions {
		fields[field] = []string{value}
	}
	for field, value := range w.Fields {
		fields[field] = []string{value}
	}
	answers := suggestionAnswers{e.release: fields}
	for i, track := range e.tracks {
		answers[track] = map[string][]string{}
		if i < len(w.Recordings) {
			if recording, ok := recordings[w.Recordings[i]]; ok {
				answers[track]["title"] = []string{recording.Title}
				answers[track]["artist"] = []string{metricCreditName(artists, recording.Artist, recording.Credits)}
			}
		}
		if i < len(w.TrackFields) {
			for field, value := range w.TrackFields[i] {
				answers[track][field] = []string{value}
			}
		}
	}
	for name, id := range e.artists {
		if mb := w.Artists[name]; mb != "" {
			answers[id] = map[string][]string{"artist_link": {"musicbrainz/" + mb}}
		}
	}
	return answers
}

// 已接受的安全终态可独立成立；遗漏可选的艺人链接不会使正确的未收录变成信息缺陷。
func metricNeedsAttribution(s metricDetail) bool {
	if s.Accepted {
		return !s.Complete && (s.Outcome == "release" || s.Outcome == "release_group")
	}
	return slices.Contains(s.Accept, "release") || slices.Contains(s.Accept, "release_group")
}

func metricEvaluateOutcomes(t *testing.T, app *integrationTestApplication, entries []metricEntry, scores []metricScore) {
	t.Helper()
	suggestions := metricSuggestions(t, app)
	links := metricStoredLinks(t, app)
	local := map[string]bool{}
	metricReadRows(t, app, `SELECT release_id FROM release_auto_values WHERE writer_level=1
		UNION SELECT m.release_id FROM track_auto_values v JOIN tracks t ON t.id=v.track_id
		JOIN media m ON m.id=t.medium_id WHERE v.writer_level=1`, func(rows *sql.Rows) {
		var id string
		requireCurationOK(t, rows.Scan(&id))
		local[id] = true
	})
	history := metricHistories(t, app)
	for i, entry := range entries {
		s := &scores[i]
		want := entry.sample.Expect.withOutcomeRules()
		s.Accept = want.Accept
		if entry.drainIssue != "" {
			s.issue(metricIssue{Kind: "stalled", Target: s.ID, Expected: "整理完成", Actual: entry.drainIssue})
		}
		for _, issue := range metricFixtureIssues(t, app, entry) {
			s.issue(issue)
		}
		switch {
		case len(links[metricLinkSubject{"release", entry.release}]) > 0:
			s.Outcome = "release"
		case len(links[metricLinkSubject{"release_group_match", entry.release}]) > 0:
			s.Outcome = "release_group"
		case s.Match.Level == "recording" && len(entry.tracks) == 1 && len(links[metricLinkSubject{"track", entry.tracks[0]}]) > 0:
			// 散曲的完整识别对象是录音，沿用 accept 中的完整对应档。
			s.Outcome = "release"
		case s.Match.Level == "artist" && s.Match.Rule == "common_credit":
			s.Outcome = "no_usable_candidates"
		case slices.ContainsFunc(s.SourceRetries, metricSourceRetry.matching):
			s.Outcome = metricWaitingSource
		case len(suggestions[entry.release]) > 0:
			s.Outcome = "suggestion"
		case local[entry.release]:
			s.Outcome = "local_cleanup"
		case s.Match.Status == "no_match" && slices.Contains(s.Match.Conflicts, "insufficient_evidence") &&
			slices.ContainsFunc(s.ModelItems, func(item metricModelItem) bool {
				return item.Kind == "local_cleanup" && item.Status == "resolved" && item.Resolution == "local_cleanup"
			}):
			// 占位输入已完成本地整理但没有可靠片段可写，程序也没有外部识别依据。
			s.Outcome = "local_cleanup"
		default:
			s.Outcome = s.Match.Status
		}
		rows := slices.Clone(suggestions[entry.release])
		for _, id := range slices.Compact(slices.Sorted(maps.Values(entry.artists))) {
			rows = append(rows, suggestions[id]...)
		}
		for _, issue := range metricSuggestionIssues(s.ID, []string{entry.release}, want, rows, metricSampleAnswers(entry)) {
			s.issue(issue)
		}
		if s.Outcome != metricWaitingSource && !slices.Contains(s.Accept, s.Outcome) {
			s.Reasons = append(s.Reasons, metricIssue{Kind: "unexpected_outcome", Target: s.ID, Expected: strings.Join(s.Accept, " 或 "), Actual: s.Outcome})
		}
		for _, id := range append(slices.Collect(maps.Values(entry.artists)), entry.release) {
			for _, run := range history[id] {
				if run.closedUnavailable() {
					s.issue(metricIssue{Kind: "source_closed", Target: s.ID, Field: run.Kind, Expected: "等待来源恢复", Actual: run.Resolution})
				}
			}
		}
		for _, issue := range s.Reasons {
			switch issue.Kind {
			case "wrong_merge", "wrong_link", "wrong_field", "open_pending", "unexpected_outcome", "missing_suggestion", "suggestion_can_accept", "suggestion_value", "source_closed", "stalled", "scan_field", "scan_structure", "route_missing":
			case "missing_field":
				// 新样本中的纠错和推断是该样本显式要求的行为。
				if len(entry.sample.Expect.Accept) == 0 || s.Outcome != "release" && s.Outcome != "release_group" && s.Outcome != "local_cleanup" {
					continue
				}
			default:
				continue
			}
			s.Rejected = append(s.Rejected, fmt.Sprintf("%s %s %s：期望 %s，实际 %s", issue.Kind, issue.Target, issue.Field, issue.Expected, issue.Actual))
		}
		slices.Sort(s.Rejected)
		s.Accepted = len(s.Rejected) == 0
		if metricNeedsAttribution(s.metricDetail) {
			s.Attribution = metricAttribute(metricScoreNeeds(entry, *s, want), history, entry.release)
		}
	}
}

func TestMetricsSafeOutcomesAndAttribution(t *testing.T) {
	allowed := []string{"unlisted", "suggestion"}
	for _, test := range []struct {
		name   string
		detail metricDetail
		want   bool
	}{
		{"正确未收录且只有可选艺人未对应", metricDetail{Accept: allowed, Outcome: "unlisted", Accepted: true}, false},
		{"正确保留建议", metricDetail{Accept: allowed, Outcome: "suggestion", Accepted: true}, false},
		{"期待放弃但程序错建组级对应", metricDetail{Accept: allowed, Outcome: "release_group"}, false},
		{"本地整理未执行", metricDetail{Accept: []string{"local_cleanup"}, Outcome: "unlisted"}, false},
		{"应找到版本却未收录", metricDetail{Accept: []string{"release"}, Outcome: "unlisted"}, true},
		{"已找到版本但缺外部录音", metricDetail{Accept: []string{"release"}, Outcome: "release", Accepted: true}, true},
		{"完整版本", metricDetail{Accept: []string{"release"}, Outcome: "release", Accepted: true, Complete: true}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := metricNeedsAttribution(test.detail); got != test.want {
				t.Errorf("参与外部信息归因=%t，预期 %t", got, test.want)
			}
		})
	}

}

func TestMetricsSuggestionValues(t *testing.T) {
	entry := metricEntry{release: "合成发行", tracks: []string{"合成曲目"}, sample: metricSample{Expect: metricExpect{Fields: map[string]string{"title": "合成专辑"}, TrackFields: []map[string]string{{"title": "合成曲名"}}}}}
	truth := metricSampleAnswers(entry)
	for _, tc := range []struct {
		name, route, target, value string
		canAccept                  bool
		issue                      string
	}{
		{"版本正确", "release", entry.release, "合成专辑", true, ""},
		{"组级正确", "release_group", entry.release, "合成专辑", true, ""},
		{"版本错值", "release", entry.release, "合成错字", true, "suggestion_value"},
		{"曲目正确", "release_group", entry.tracks[0], "合成曲名", true, ""},
		{"曲目误用专辑答案", "release_group", entry.tracks[0], "合成专辑", true, "suggestion_value"},
		{"未收录反例即使同值也不可采纳", "unlisted", entry.release, "合成专辑", true, "suggestion_can_accept"},
		{"无候选反例", "no_usable_candidates", entry.release, "合成错字", true, "suggestion_can_accept"},
		{"反例仅供观察", "no_usable_candidates", entry.release, "合成错字", false, ""},
		{"正例仅供观察", "release", entry.release, "合成错字", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value, err := json.Marshal(tc.value)
			requireCurationOK(t, err)
			rule := metricExpect{Accept: []string{tc.route, "unlisted"}}.withOutcomeRules()
			row := metricSuggestion{Target: tc.target, Field: "title", Value: value, CanAccept: tc.canAccept}
			issues := metricSuggestionIssues("合成样本", []string{entry.release}, rule, []metricSuggestion{row}, truth)
			if tc.issue == "" && len(issues) != 0 || tc.issue != "" && (len(issues) != 1 || issues[0].Kind != tc.issue) {
				t.Fatalf("期望问题 %q，实际 %+v", tc.issue, issues)
			}
		})
	}
}

func TestLiveCorpusSafeOutcomes(t *testing.T) {
	raw, err := os.ReadFile("../../cmd/roomusic-curation-bench/corpus.json")
	requireCurationOK(t, err)
	var corpus struct{ Releases []liveAlbum }
	requireCurationOK(t, json.Unmarshal(raw, &corpus))
	full := []string{"release", "release_group", "unlisted", "suggestion"}
	group := []string{"release_group", "unlisted", "suggestion"}
	want := map[string][]string{"multidisc": full, "double_lp": full, "compilation": full, "collaboration": full, "romanized": full, "subset": group, "superset": group, "gbk": {"release", "release_group", "local_cleanup", "unlisted", "suggestion"}}
	for _, album := range corpus.Releases {
		allowed, ok := want[album.Tags]
		if !ok {
			continue
		}
		rule := album.Expect.withOutcomeRules()
		if !slices.Equal(rule.Accept, allowed) || rule.SuggestionCanAccept != nil {
			t.Errorf("%s 允许结果 %v，应为 %v 且可采纳的建议按值核对", album.Title, rule.Accept, allowed)
		}
		delete(want, album.Tags)
	}
	if len(want) > 0 {
		t.Fatalf("公开清单缺少形态 %v", slices.Sorted(maps.Keys(want)))
	}
}
