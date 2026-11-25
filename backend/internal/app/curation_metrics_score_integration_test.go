package app

import (
	"cmp"
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"testing"
)

type metricScore struct {
	metricDetail
	Entered, OpenPending, OpenExceptions, WrongLinks, WrongMerges, WrongFields int
}

type metricDetail struct {
	ID       string         `json:"id"`
	Shape    string         `json:"shape"`
	Complete bool           `json:"complete"`
	Match    metricMatch    `json:"match"`
	Requests map[string]int `json:"requests"`
	Reasons  []metricIssue  `json:"reasons"`
}

type metricMatch struct {
	Status         string   `json:"status"`
	Method         string   `json:"method"`
	Level          string   `json:"level,omitempty"`
	Rule           string   `json:"rule,omitempty"`
	CandidateCount int      `json:"candidate_count,omitempty"`
	Conflicts      []string `json:"conflicts,omitempty"`
	PendingStatus  string   `json:"pending_status,omitempty"`
	Resolution     string   `json:"resolution,omitempty"`
}

// Target、Peer 使用样本名或曲目与艺人名；CountedFor 标明共享错误归入哪份样本的汇总。
type metricIssue struct {
	Kind       string          `json:"kind"`
	Target     string          `json:"target"`
	Peer       string          `json:"peer,omitempty"`
	Field      string          `json:"field,omitempty"`
	Source     string          `json:"source,omitempty"`
	Expected   string          `json:"expected"`
	Actual     string          `json:"actual"`
	Before     json.RawMessage `json:"before,omitempty"`
	After      json.RawMessage `json:"after,omitempty"`
	CountedFor string          `json:"counted_for,omitempty"`
}

func (s *metricScore) issue(issue metricIssue) {
	s.Complete = false
	s.Reasons = append(s.Reasons, issue)
}

func metricReadRows(t *testing.T, app *integrationTestApplication, q string, read func(*sql.Rows)) {
	t.Helper()
	rows, err := app.database.QueryContext(t.Context(), q)
	requireCurationOK(t, err)
	defer rows.Close()
	for rows.Next() {
		read(rows)
	}
	requireCurationOK(t, rows.Err())
}

type metricObject struct{ kind, id string }

func metricScoreEntries(t *testing.T, app *integrationTestApplication, entries []metricEntry) []metricScore {
	t.Helper()
	scores := make([]metricScore, len(entries))
	releaseIndex := map[string]int{}
	sampleIndex := map[string]int{}
	owners := map[string][]int{}
	own := func(id string, i int) {
		if !slices.Contains(owners[id], i) {
			owners[id] = append(owners[id], i)
		}
	}
	for i, e := range entries {
		scores[i].metricDetail = metricDetail{ID: e.sample.ID, Shape: e.sample.Shape, Complete: true, Requests: e.requests, Reasons: []metricIssue{}}
		releaseIndex[e.release] = i
		sampleIndex[e.sample.ID] = i
		own(e.release, i)
		for _, id := range e.artists {
			own(id, i)
		}
	}

	// 自动补署名后可能出现新的艺人，保留扫描时的身份，同时读取最终署名。
	artists := make([]map[string]string, len(entries))
	for i, e := range entries {
		artists[i] = maps.Clone(e.artists)
	}
	metricReadRows(t, app, `SELECT DISTINCT coalesce(m.origin_release_id,m.release_id,c.release_id),n.spelling,c.artist_id
		FROM artist_effective_credits c JOIN artist_names n ON n.id=c.name_id
		LEFT JOIN tracks t ON t.id=c.track_id LEFT JOIN media m ON m.id=t.medium_id`, func(rows *sql.Rows) {
		var release, name, id string
		requireCurationOK(t, rows.Scan(&release, &name, &id))
		if i, ok := releaseIndex[release]; ok {
			artists[i][name] = id
			own(id, i)
		}
	})

	groupIDs := map[string]string{}
	metricReadRows(t, app, `SELECT r.id,r.group_id,coalesce(w.status,'未处理'),coalesce(w.result->>'method',''),coalesce(w.result->'conflicts','[]'),
 coalesce(w.result->>'level',''),coalesce(w.result->>'rule',''),coalesce(p.candidate_count,0),
		coalesce(p.status,''),coalesce(p.resolution,p.shelved_reason,'')
		FROM releases r LEFT JOIN curation_work_states w ON w.target_id=r.id AND w.subject='release'
		LEFT JOIN LATERAL (SELECT status,resolution,shelved_reason,jsonb_array_length(candidates) AS candidate_count FROM curation_pending_items
			WHERE target_id=r.id AND kind IN ('release_match','recording_match') ORDER BY created_at DESC LIMIT 1) p ON true`, func(rows *sql.Rows) {
		var id, group string
		var m metricMatch
		var conflicts []byte
		requireCurationOK(t, rows.Scan(&id, &group, &m.Status, &m.Method, &conflicts, &m.Level, &m.Rule, &m.CandidateCount, &m.PendingStatus, &m.Resolution))
		requireCurationOK(t, json.Unmarshal(conflicts, &m.Conflicts))
		groupIDs[id] = group
		if i, ok := releaseIndex[id]; ok {
			scores[i].Match = m
		}
	})
	canonical := map[string]string{}
	artistNames := map[string]string{}
	mergeTrigger := map[string]string{}
	for _, entry := range entries {
		for _, op := range entry.merges {
			mergeTrigger[op] = entry.sample.ID
		}
	}
	mergeOps := map[string]string{}
	metricReadRows(t, app, `SELECT a.id,coalesce(o.redirect_artist_id,a.id),a.initial_name,coalesce(o.redirect_operation_id::text,'') FROM artists a LEFT JOIN artist_overrides o ON o.artist_id=a.id`, func(rows *sql.Rows) {
		var id, dest, name, op string
		requireCurationOK(t, rows.Scan(&id, &dest, &name, &op))
		artistNames[id] = name
		mergeOps[id] = op
		canonical[id] = dest
	})
	metricScoreValues(t, app, entries, scores, artists, groupIDs, canonical)
	// 错归组按无序的发行对计数，两个受影响的样本都不能算完成。
	for i, a := range entries {
		for j := i + 1; j < len(entries); j++ {
			b := entries[j]
			shared := groupIDs[a.release] == groupIDs[b.release]
			want := slices.Contains(a.sample.Expect.GroupWith, b.sample.ID) || slices.Contains(b.sample.Expect.GroupWith, a.sample.ID)
			if shared != want {
				issue := metricIssue{Kind: "missing_group", Target: a.sample.ID, Peer: b.sample.ID, Expected: "同组", Actual: "分组"}
				if shared {
					scores[j].WrongMerges++
					issue.Kind = "wrong_merge"
					issue.Expected, issue.Actual = "分组", "同组"
					issue.CountedFor = b.sample.ID
				}
				scores[i].issue(issue)
				scores[j].issue(issue)
			}
		}
	}
	// 明确标注的别名专辑按外部艺人编号配对，其余跨身份重定向均是错并。
	allowed := map[[2]string]bool{}
	for i, e := range entries {
		for _, peer := range e.sample.Expect.MergeArtistWith {
			j, ok := sampleIndex[peer]
			if !ok {
				t.Fatalf("样本 %s 指定的艺人合并对象 %s 不存在", e.sample.ID, peer)
			}
			for name, want := range e.sample.Expect.Artists {
				for peerName, peerWant := range entries[j].sample.Expect.Artists {
					if want == "" || peerWant != want {
						continue
					}
					a, b := e.artists[name], entries[j].artists[peerName]
					if a == "" || b == "" || canonical[a] != canonical[b] {
						issue := metricIssue{Kind: "missing_artist_merge", Target: name, Peer: peerName, Expected: "同一艺人", Actual: "未合并"}
						scores[i].issue(issue)
						scores[j].issue(issue)
					}
					allowed[[2]string{a, b}] = true
					allowed[[2]string{b, a}] = true
				}
			}
		}
	}
	for from, to := range canonical {
		if from == to || allowed[[2]string{from, to}] {
			continue
		}
		involved := append(slices.Clone(owners[from]), owners[to]...)
		if len(involved) == 0 {
			continue
		}
		trigger, ok := sampleIndex[mergeTrigger[mergeOps[from]]]
		if !ok {
			t.Fatalf("艺人合并缺少入库批次：%s", artistNames[from])
		}
		scores[trigger].WrongMerges++
		// 两位署名数相同时生产代码以本地 UUID 决定保留方，错并按无序的身份对记录。
		a, b := artistNames[from], artistNames[to]
		issue := metricIssue{Kind: "wrong_merge", Target: min(a, b), Peer: max(a, b), Expected: "分别保留", Actual: "艺人合并", CountedFor: entries[trigger].sample.ID}
		involved = append(involved, trigger)
		slices.Sort(involved)
		for _, i := range slices.Compact(involved) {
			scores[i].issue(issue)
		}
	}
	metricReadRows(t, app, `SELECT source_release_id,target_release_id FROM release_merges`, func(rows *sql.Rows) {
		var from, to string
		requireCurationOK(t, rows.Scan(&from, &to))
		a, okA := releaseIndex[from]
		b, okB := releaseIndex[to]
		if !okA || !okB {
			t.Fatalf("合并记录缺少样本：%s %s", from, to)
		}
		// 曲库没有标注任何一对整版发行需要并入。
		scores[a].WrongMerges++
		issue := metricIssue{Kind: "wrong_merge", Target: entries[a].sample.ID, Peer: entries[b].sample.ID, Expected: "分别保留", Actual: "发行合并", CountedFor: entries[a].sample.ID}
		scores[a].issue(issue)
		scores[b].issue(issue)
	})
	// 发行、散放单曲分别以发行与录音匹配项计入。艺人项只计待办与例外。
	entered := map[metricObject]bool{}
	metricReadRows(t, app, `SELECT coalesce(p.target_id,p.artist_id),p.kind,p.status,
		(SELECT count(*) FROM curation_model_exceptions x WHERE x.pending_item_id=p.id AND x.status IN ('open','accepting'))
		FROM curation_pending_items p`, func(rows *sql.Rows) {
		var id, kind, status string
		var exceptions int
		requireCurationOK(t, rows.Scan(&id, &kind, &status, &exceptions))
		involved := owners[id]
		if len(involved) == 0 {
			t.Fatalf("待定项 %s 的目标 %s 缺少样本", kind, id)
		}
		owner := slices.Min(involved)
		target := metricObject{"release", id}
		if kind == "recording_match" {
			target.kind = "track"
		}
		if kind != "artist_identity" && status != "waiting_route" && !entered[target] {
			scores[owner].Entered++
			entered[target] = true
		}
		open := status == "waiting_model" || status == "in_progress" || status == "waiting_human" || status == "waiting_route"
		if open {
			scores[owner].OpenPending++
		}
		scores[owner].OpenExceptions += exceptions
		if open || exceptions > 0 {
			for _, i := range involved {
				label := entries[owner].sample.ID
				if kind == "artist_identity" {
					label = artistNames[id]
				}
				if open {
					scores[i].issue(metricIssue{Kind: "open_pending", Target: label, Field: kind, Expected: "已结束", Actual: status})
				}
				if exceptions > 0 {
					scores[i].issue(metricIssue{Kind: "open_exception", Target: label, Field: kind, Expected: "0", Actual: fmt.Sprint(exceptions)})
				}
			}
		}
	})
	for i := range scores {
		issues := scores[i].Reasons
		slices.SortFunc(issues, func(a, b metricIssue) int {
			return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Target, b.Target), cmp.Compare(a.Peer, b.Peer), cmp.Compare(a.Field, b.Field), cmp.Compare(a.Source, b.Source), cmp.Compare(a.Expected, b.Expected), cmp.Compare(a.Actual, b.Actual), cmp.Compare(a.CountedFor, b.CountedFor))
		})
	}
	return scores
}
