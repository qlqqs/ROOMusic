package program

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/platform/egress"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/musicbrainz"
)

type judgeMusicBrainz struct{ *musicbrainz.Client }

func (c judgeMusicBrainz) SearchReleasePage(ctx context.Context, q curation.ReleaseSearch) (curation.ReleasePage, error) {
	page, err := c.Client.SearchReleasePage(ctx, musicbrainz.ReleaseSearch(q))
	return curation.ReleasePage{PageInfo: curation.PageInfo(page.PageInfo), Items: page.Items}, err
}

func TestPostgreSQLJudgeHTTPBudget(t *testing.T) {
	for _, tc := range []struct {
		name, conflict          string
		initial, want, wantUsed int
		shortRetry, cached      bool
		resultOn                int
	}{
		{name: "新判断六次上限", want: 6, wantUsed: 6, conflict: "request_limit"},
		{name: "工作者重建沿用已花五次", initial: 5, want: 1, wantUsed: 6, conflict: "request_limit"},
		{name: "已用完不出站", initial: 6, wantUsed: 6, conflict: "request_limit"},
		{name: "429 的短重试计入上限", want: 6, wantUsed: 6, shortRetry: true, conflict: "request_limit"},
		{name: "已用完仍可读取缓存", initial: 6, wantUsed: 6, cached: true},
		{name: "第六次详情核实成功", want: 6, wantUsed: 6, resultOn: 5},
		{name: "第六次搜索仍缺详情", want: 6, wantUsed: 6, resultOn: 6, conflict: "request_limit"},
		{name: "不完整列表立即停止", want: 1, wantUsed: 1, conflict: "list_truncated"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			f := newPendingRefreshFixture(t)
			release, first := f.release(t, "合成短专辑", "合成艺人")
			f.exec(t, `UPDATE tracks SET scan_tags='{"comments":[{"key":"COMMENT","value":"CAT-A CAT-B CAT-C CAT-D CAT-E CAT-F CAT-G"}]}' WHERE id=$1`, first)
			f.exec(t, `UPDATE tracks SET duration_seconds=100 WHERE medium_id IN (SELECT id FROM media WHERE release_id=$1)`, release)
			byID, err := f.writer.LoadCurationEvidence(ctx, []string{release})
			requirePendingRefreshOK(t, err)
			e := byID[release]
			if !hasLocalIdentifier(e, "CAT-A") || !hasLocalIdentifier(e, "CAT-G") {
				t.Fatalf("缺少用于出站计数的原始注释：%+v", e.Tracks)
			}
			state := curation.WorkState{Subject: "release", TargetID: release, Status: "unmatched", RuleVersion: StoredRuleVersion, MatchRuleVersion: MatchRuleVersion, Generation: 1}
			releaseInputs(e).record(&state)
			requirePendingRefreshOK(t, curation.SaveWorkState(ctx, f.db, state))
			requirePendingRefreshOK(t, curation.UpsertPendingItem(ctx, f.db, curation.PendingItem{
				TargetID: release, Kind: "release_match", RuleVersion: StoredRuleVersion,
				InputDigest: state.InputDigest, IdentityDigest: state.IdentityDigest, Evidence: jsonValue(PendingEvidence(e)),
			}))
			item := f.claim(t, release, "release_match")
			answer := curation.JudgeAnswer{ItemID: item.ID, Decision: "judge"}
			for _, cat := range []string{"CAT-A", "CAT-B", "CAT-C", "CAT-D", "CAT-E", "CAT-F", "CAT-G"} {
				var group curation.JudgeGroup
				group.Identifier.Kind, group.Identifier.Value = "catalog_number", cat
				answer.Groups = append(answer.Groups, group)
			}
			requirePendingRefreshOK(t, f.writer.SubmitJudge(ctx, item, answer))
			f.exec(t, `UPDATE curation_work_states SET result=jsonb_set(jsonb_set(result,'{judge,used}',to_jsonb($2::int)),'{judge,next}',to_jsonb($2::int))
				WHERE subject='release' AND target_id=$1`, release, tc.initial)
			var raw []byte
			requirePendingRefreshOK(t, f.db.QueryRowContext(ctx, `SELECT result->'judge' FROM curation_work_states WHERE subject='release' AND target_id=$1`, release).Scan(&raw))
			var cursor curation.JudgeCursor
			requirePendingRefreshOK(t, json.Unmarshal(raw, &cursor))
			var mu sync.Mutex
			var queries []string
			remote := uuid.New()
			groupID, recA, recB := uuid.New(), uuid.New(), uuid.New()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				queries = append(queries, r.URL.Query().Get("query"))
				n := len(queries)
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/release/"+remote {
					fmt.Fprintf(w, `{"id":%q,"title":"合成短专辑","artist-credit":[{"name":"合成艺人"}],"release-group":{"id":%q},
					"label-info":[{"catalog-number":"CAT-E"}],"media":[{"position":1,"track-count":2,"tracks":[
					{"position":1,"title":"曲1","length":100000,"recording":{"id":%q}},
					{"position":2,"title":"曲2","length":100000,"recording":{"id":%q}}]}]}`, remote, groupID, recA, recB)
					return
				}
				if r.URL.Path != "/release" {
					t.Errorf("非预期 HTTP 路径 %s", r.URL.Path)
				}
				if tc.shortRetry && n == 1 {
					w.Header().Set("Retry-After", "0")
					http.Error(w, "合成短重试", http.StatusTooManyRequests)
					return
				}
				if tc.resultOn == n {
					fmt.Fprintf(w, `{"count":1,"offset":0,"releases":[{"id":%q,"title":"合成短专辑","label-info":[{"catalog-number":%q}]}]}`, remote, answer.Groups[n-1].Identifier.Value)
					return
				}
				if tc.conflict == "list_truncated" {
					_, _ = w.Write([]byte(`{"count":50,"offset":0,"releases":[]}`))
					return
				}
				_, _ = w.Write([]byte(`{"count":0,"offset":0,"releases":[]}`))
			}))
			defer server.Close()
			client := musicbrainz.New(sources.Options{BaseURL: server.URL, DataDirectory: t.TempDir(), Limiter: sources.NewLimiter(0, nil, nil),
				NewClient: func(egress.Options) (*http.Client, error) { return server.Client(), nil },
			})
			gate := curation.NewSources(f.db, curation.SourceRegistration{Declaration: musicbrainz.Declaration(), Releases: judgeMusicBrainz{client}})
			_, err = f.writer.SetSettings(ctx, f.adminID, true)
			requirePendingRefreshOK(t, err)
			_, err = gate.SetEnabled(ctx, "musicbrainz", f.adminID, true)
			requirePendingRefreshOK(t, err)
			if tc.cached {
				finder, _ := gate.ReleaseFinder("musicbrainz")
				_, err = finder.(curation.ReleasePageFinder).SearchReleasePage(ctx, curation.ReleaseSearch{CatalogNumber: "CAT-G"})
				requirePendingRefreshOK(t, err)
				mu.Lock()
				if len(queries) != 1 {
					t.Errorf("预热缓存应有一次真实查询，实际 %v", queries)
				}
				queries = nil
				mu.Unlock()
			}
			w := NewWorker(f.db, f.writer, gate, nil, nil, Options{})
			got, err := w.runJudge(ctx, e, &cursor)
			var conflicts []string
			if tc.conflict != "" {
				conflicts = []string{tc.conflict}
			}
			wantMatch := tc.resultOn == 5
			if err != nil || (got.Status == "matched") != wantMatch || !slices.Equal(got.Conflicts, conflicts) {
				t.Fatalf("线索的实际结论 %+v，期望匹配=%t、冲突=%v，错误=%v，游标=%+v", got, wantMatch, conflicts, err, cursor)
			}
			if wantMatch && (got.Release == nil || got.Release.ID != remote || len(got.Pairs) != 2) {
				t.Fatalf("第六次详情应核实指定发行和两首录音：%+v", got)
			}
			mu.Lock()
			calls := slices.Clone(queries)
			mu.Unlock()
			if len(calls) != tc.want {
				t.Fatalf("实际 HTTP %d 次，期望 %d：%v", len(calls), tc.want, calls)
			}
			for i, query := range calls {
				if wantMatch && i == 5 {
					if query != "" {
						t.Errorf("第六次应读取发行详情，实际查询 %q", query)
					}
					continue
				}
				groupIndex := tc.initial + i
				if tc.shortRetry && i > 0 {
					groupIndex--
				}
				if cat := answer.Groups[groupIndex].Identifier.Value; query != `catno:"`+strings.ReplaceAll(cat, "-", `\-`)+`"` {
					t.Errorf("第 %d 个实际查询 %q 未按组顺序使用 %s", i+1, query, cat)
				}
			}
			var saved int
			requirePendingRefreshOK(t, f.db.QueryRowContext(ctx, `SELECT (result->'judge'->>'used')::int FROM curation_work_states WHERE subject='release' AND target_id=$1`, release).Scan(&saved))
			if saved != tc.wantUsed {
				t.Fatalf("已用出站次数落库=%d，期望 %d", saved, tc.wantUsed)
			}
		})
	}
}
