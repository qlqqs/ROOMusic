package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
)

type explainedRead struct {
	query string
	args  []any
}

// 捕获真实 Store 的全部 SQL（含分页 COUNT），不手抄一份近似业务查询。
type explainCapture struct {
	Queryer
	reads []explainedRead
}

func (c *explainCapture) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	c.reads = append(c.reads, explainedRead{q, args})
	return c.Queryer.QueryContext(ctx, q, args...)
}
func (c *explainCapture) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	c.reads = append(c.reads, explainedRead{q, args})
	return c.Queryer.QueryRowContext(ctx, q, args...)
}

func TestPostgreSQLRecentAdditionsExplain(t *testing.T) {
	output := os.Getenv("ROOMUSIC_CATALOG_EXPLAIN_OUTPUT")
	if output == "" {
		t.Skip("仅按需执行规模 EXPLAIN；设置 ROOMUSIC_CATALOG_EXPLAIN_OUTPUT 保存报告")
	}
	f := newPlacementFixture(t)
	auxiliary := []string{"release_corrections", "release_field_decisions", "release_grouping_evidence", "release_issue_reviews", "release_artworks"}
	for _, table := range auxiliary {
		f.exec("ALTER TABLE " + table + " SET (autovacuum_enabled=false)")
	}
	f.exec(`INSERT INTO release_groups(id) SELECT md5('explain-group-'||n)::uuid FROM generate_series(1,8000) n`)
	f.exec(`INSERT INTO releases(id,group_id,title,artist,year,created_at,source_root_id,relative_directory,candidate_kind)
 SELECT md5('explain-release-'||n)::uuid,md5('explain-group-'||(CASE WHEN n<=8000 THEN n ELSE n-8000 END))::uuid,
 '合成专辑 '||n,'合成艺人',1980+n%45,TIMESTAMPTZ '2020-01-01T00:00:00Z'+n*INTERVAL '1 hour',
 $1::uuid,'synthetic/'||n,'ordinary_directory' FROM generate_series(1,10000) n`, f.rootID)
	f.exec(`INSERT INTO media(id,release_id,position,title)
 SELECT md5('explain-medium-'||n)::uuid,md5('explain-release-'||n)::uuid,1,'合成碟' FROM generate_series(1,10000) n`)
	f.exec(`INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,source_kind,duration_seconds)
 SELECT md5('explain-track-'||n||'-'||p)::uuid,md5('explain-medium-'||n)::uuid,p,'合成曲目','合成艺人',1,
 $1::uuid,'synthetic/'||n||'/'||p||'.flac',CASE WHEN n%40=0 OR (n%4=1 AND p=1) THEN 'missing' ELSE 'present' END,NOW(),'physical',180
 FROM generate_series(1,10000) n CROSS JOIN generate_series(1,10) p`, f.rootID)
	for _, table := range []string{"release_groups", "releases", "media", "tracks"} {
		f.exec("ANALYZE " + table)
	}
	var report strings.Builder
	report.WriteString("# 最近新增与加入时间排序：规模验证\n\n## 环境与方法\n\n- 一次性 Compose 项目 roomusic-test-m5f，随机测试数据库、独立 schema；没有访问用户数据。\n- 合成 8,000 组、10,000 发行、100,000 曲目，5,000 曲目 missing（5%）；前2,000组双版本，其余单版本，其中250发行整版缺失、2,500发行各缺失1轨，共7,800个可见组。\n- 核心四表仅在测试准备时 ANALYZE。五个空元数据辅助表禁止自动统计，先验证未 ANALYZE（reltuples=-1），再显式 ANALYZE（reltuples=0）。业务请求不执行 ANALYZE、不修改任何 enable_* 或全局/会话配置。\n- 每个只读 REPEATABLE READ 事务开始后 SET LOCAL jit = off；同一物理连接事务内 SHOW jit=off，结束后与连接原默认值相同。\n- 下文捕获真实 Store 查询和参数，含分页COUNT，运行 EXPLAIN (ANALYZE, BUFFERS)。完整 HTTP 服务端 p95 见本文件的 HTTP 结果小节和 catalog-http-performance.md；SQL执行时间不能代替HTTP门槛。\n")
	var version string
	if err := f.database.QueryRow("SHOW server_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(&report, "- PostgreSQL：%s。\n", version)
	ctx := context.Background()
	conn, err := f.database.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var before string
	if err := conn.QueryRowContext(ctx, "SHOW jit").Scan(&before); err != nil {
		t.Fatal(err)
	}
	for _, analyzed := range []bool{false, true} {
		name := "辅助空表未 ANALYZE"
		want := int64(-1)
		if analyzed {
			name = "辅助空表已 ANALYZE"
			want = 0
			for _, table := range auxiliary {
				f.exec("ANALYZE " + table)
			}
		}
		fmt.Fprintf(&report, "\n## %s\n\n", name)
		for _, table := range auxiliary {
			var estimate int64
			if err := conn.QueryRowContext(ctx, `SELECT reltuples::bigint FROM pg_class WHERE oid=$1::regclass`, table).Scan(&estimate); err != nil {
				t.Fatal(err)
			}
			if estimate != want {
				t.Fatalf("辅助表 %s 统计状态=%d，预期%d", table, estimate, want)
			}
			fmt.Fprintf(&report, "- `%s`: reltuples=%d。\n", table, estimate)
		}
		for _, recent := range []bool{true, false} {
			title := "最近8项（无COUNT）"
			if !recent {
				title = "加入时间排序首页50项（含COUNT）"
			}
			fmt.Fprintf(&report, "\n### %s\n", title)
			_, err = readWithLocalJITOff(ctx, conn, func(tx *sql.Tx) (bool, error) {
				var jit string
				if err := tx.QueryRowContext(ctx, "SHOW jit").Scan(&jit); err != nil {
					return false, err
				}
				if jit != "off" {
					return false, fmt.Errorf("jit=%s", jit)
				}
				capture := &explainCapture{Queryer: tx}
				store := NewStore(capture)
				if recent {
					items, err := store.ListRecentlyAddedReleaseGroups(ctx)
					if err != nil {
						return false, err
					}
					if len(items) != 8 {
						return false, fmt.Errorf("recent length %d", len(items))
					}
				} else {
					page, err := store.ListReleaseGroups(ctx, ListParams{Sort: ReleaseGroupSortAddedAtDesc, Page: 1, PageSize: 50})
					if err != nil {
						return false, err
					}
					if len(page.Items) != 50 || page.Total != 7800 {
						return false, fmt.Errorf("page %d/%d", len(page.Items), page.Total)
					}
				}
				for _, read := range capture.reads {
					fmt.Fprintf(&report, "\n参数：`%v`；事务jit=%s。\n\n```text\n", read.args, jit)
					rows, err := tx.QueryContext(ctx, "EXPLAIN (ANALYZE, BUFFERS) "+read.query, read.args...)
					if err != nil {
						return false, err
					}
					for rows.Next() {
						var line string
						if err := rows.Scan(&line); err != nil {
							rows.Close()
							return false, err
						}
						report.WriteString(line + "\n")
					}
					err = rows.Err()
					rows.Close()
					if err != nil {
						return false, err
					}
					report.WriteString("```\n")
				}
				return true, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			var after string
			if err := conn.QueryRowContext(ctx, "SHOW jit").Scan(&after); err != nil || after != before {
				t.Fatalf("事务外jit=%s，原值%s，错误%v", after, before, err)
			}
		}
	}
	fmt.Fprintf(&report, "\n所有事务结束后同一物理连接 SHOW jit 仍为 `%s`。\n", before)
	if err := os.WriteFile(output, []byte(report.String()), 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("查询计划保存到 %s", output)
}
