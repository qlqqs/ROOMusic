package app

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/curation"
)

// 从与 API 相同的读取入口取得 can_accept，保留不可采纳的观察和没有字段变更的建议。
func metricSuggestions(t *testing.T, app *integrationTestApplication) map[string][]curation.ModelException {
	t.Helper()
	found := map[string][]curation.ModelException{}
	writer := curation.NewWriter(app.database, app.dataDirectory, nil)
	for page := 1; ; page++ {
		items, total, err := writer.ListExceptions(t.Context(), "open", page, 100)
		requireCurationOK(t, err)
		for _, item := range items {
			if item.Release != nil {
				found[item.Release.ID] = append(found[item.Release.ID], item)
			}
		}
		if int64(page*100) >= total {
			return found
		}
	}
}

func metricSuggestionIssues(t *testing.T, sample string, w metricExpect, items []curation.ModelException) []metricIssue {
	t.Helper()
	found := map[string][]string{}
	var issues []metricIssue
	for _, item := range items {
		if w.SuggestionCanAccept != nil && item.CanAccept != *w.SuggestionCanAccept {
			issues = append(issues, metricIssue{Kind: "suggestion_can_accept", Target: sample, Field: item.Tool, Expected: fmt.Sprint(*w.SuggestionCanAccept), Actual: fmt.Sprint(item.CanAccept)})
		}
		var preview struct {
			Changes []struct {
				Field string
				After any
			}
		}
		// 只有带字段变更的预览能佐证所标注的建议值。
		if len(w.Suggestions) > 0 && item.Track == nil && json.Unmarshal(item.Preview, &preview) == nil {
			for _, ch := range preview.Changes {
				found[ch.Field] = append(found[ch.Field], fmt.Sprint(ch.After))
			}
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
		for _, issue := range metricSuggestionIssues(t, s.ID, want, suggestions[entry.release]) {
			s.issue(issue)
		}
		if !slices.Contains(s.Accept, s.Outcome) {
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
			case "wrong_merge", "wrong_link", "wrong_field", "open_pending", "unexpected_outcome", "missing_suggestion", "suggestion_can_accept", "source_closed", "stalled", "scan_field", "scan_structure", "route_missing":
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

	accept := false
	w := metricExpect{Accept: allowed, SuggestionCanAccept: &accept}
	observation := curation.ModelException{Tool: "submit_verification", CanAccept: false}
	actionable := curation.ModelException{Tool: "resolve_release_match", CanAccept: true}
	if issues := metricSuggestionIssues(t, "合成现场版", w, []curation.ModelException{observation}); len(issues) != 0 {
		t.Fatal(issues)
	}
	if issues := metricSuggestionIssues(t, "合成现场版", w, []curation.ModelException{observation, actionable}); len(issues) != 1 || issues[0].Kind != "suggestion_can_accept" {
		t.Fatalf("可采纳的对应建议应单独列错：%+v", issues)
	}
}
