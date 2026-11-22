package operations

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// 直接对候选函数发出的 SQL 与实参执行 EXPLAIN，再返回原查询结果给它解码。
type curationPlanProbe struct {
	*sql.Tx
	plan string
}

func (p *curationPlanProbe) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	rows, err := p.Tx.QueryContext(ctx, "EXPLAIN (ANALYZE, BUFFERS, COSTS OFF, TIMING OFF) "+query, args...)
	if err != nil {
		return nil, err
	}
	var lines []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			rows.Close()
			return nil, err
		}
		lines = append(lines, s)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	p.plan = strings.Join(lines, "\n")
	return p.Tx.QueryContext(ctx, query, args...)
}

// A-V2、A-V8、A-V10：在有统计信息且可选顺序扫描的库中，按来源、批次及游标取候选。
func TestPostgreSQLCurationFilterPlans(t *testing.T) {
	for _, tc := range []struct {
		name    string
		perPage int
		tied    bool
	}{
		{"tied_timestamps", 2, true}, {"production_page", 100, false},
	} {
		t.Run(tc.name, func(t *testing.T) { checkCurationFilterPlans(t, tc.perPage, tc.tied) })
	}
}

func checkCurationFilterPlans(t *testing.T, perPage int, tied bool) {
	t.Helper()
	f := newRunFixture(t)
	batch := uuid.New()
	f.exec("INSERT INTO curation_batches(id,tier,actor_id) VALUES($1,'program',$2)", batch, f.actorID)
	// 固定并列时间与 UUID，游标在相同时间戳内用主键续做。
	f.exec(`INSERT INTO operations(id,actor_id,operation_type,target_type,target_id,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id,origin,batch_id,created_at)
 SELECT md5('curation-plan-'||n)::uuid,$1,'release_auto_correction','release',$2,gen_random_uuid(),'synthetic',1,2,'{}','{}','{}','fixture',
 jsonb_build_object('source','curation','provider',CASE WHEN n % (5000 / (2*$4::int))=0 THEN 'rare' ELSE 'common' END),
 CASE WHEN n % (5000 / (2*$4::int))=0 THEN $3::uuid END,
 timestamptz '2026-01-01T00:00:00Z'+CASE WHEN $5 THEN 0 ELSE n END * interval '1 second' FROM generate_series(1,5000) n`, f.actorID, f.releaseID, batch, perPage, tied)
	f.exec("ANALYZE operations")
	for _, tc := range []struct {
		name, index string
		params      ListParams
	}{
		{"provider", "operations_provider_idx", ListParams{Provider: "rare"}},
		{"batch", "operations_batch_idx", ListParams{BatchID: batch}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			tx, err := f.database.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			probe := &curationPlanProbe{Tx: tx}
			check := func(label, forbiddenScan string) {
				t.Helper()
				t.Logf("%s\n%s", label, probe.plan)
				if !strings.Contains(probe.plan, tc.index) {
					t.Errorf("%s 未使用 %s：\n%s", label, tc.index, probe.plan)
				}
				if strings.Contains(probe.plan, forbiddenScan) {
					t.Errorf("%s 出现 %q：\n%s", label, strings.TrimSpace(forbiddenScan), probe.plan)
				}
			}
			// List 在内部使用 listFilter 拼接实际的 WHERE、排序、分页与关联。
			var before time.Time
			var id string
			for page := 1; page <= 2; page++ {
				tc.params.Page, tc.params.PageSize = page, perPage
				list, err := List(ctx, probe, tc.params)
				if err != nil || list.Total != int64(2*perPage) || len(list.Items) != perPage {
					t.Fatalf("第 %d 页列表=%+v, %v", page, list, err)
				}
				// 连接别名的扫描方式由优化器按规模选择，列表只检查筛选别名 o。
				check(fmt.Sprintf("操作记录列表第 %d 页", page), "Seq Scan on operations o ")
				// 范围撤销多取一条，供调用方判断是否返回 next_cursor。
				limit := perPage + 1
				items, err := CurationRevertCandidates(ctx, probe, tc.params.BatchID, tc.params.Provider, before, id, limit)
				want := limit
				if page == 2 {
					want = perPage
				}
				if err != nil || len(items) != want {
					t.Fatalf("第 %d 段候选数=%d，期望 %d, %v", page, len(items), want, err)
				}
				for i, op := range list.Items {
					if items[i].ID != op.ID {
						t.Fatalf("第 %d 页第 %d 条候选=%s，期望 %s", page, i, items[i].ID, op.ID)
					}
				}
				check(fmt.Sprintf("范围撤销候选第 %d 段", page), "Seq Scan")
				last := items[perPage-1]
				before, id = last.CreatedAt, last.ID
			}
		})
	}
}
