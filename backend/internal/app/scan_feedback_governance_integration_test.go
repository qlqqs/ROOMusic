package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/library"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func startScanFeedback(t *testing.T, app *integrationTestApplication, admin *integrationTestUser) string {
	t.Helper()
	response := app.request(t, http.MethodPost, "/api/v1/scans", map[string]any{}, admin, nil)
	if response.Code != 202 {
		t.Fatalf("start scan: HTTP %d: %s", response.Code, response.Body.String())
	}
	return decodeJSONMap(t, response)["id"].(string)
}

func removeScanFeedbackAlbum(t *testing.T, env *claimRescanEnvironment, album string) {
	t.Helper()
	if err := os.RemoveAll(filepath.Join(env.library, album)); err != nil {
		t.Fatal(err)
	}
}

// 屏障在真实 SQL 的行触发器中阻塞，不向生产代码加入 hook，也不靠延时碰运气。
// advisory 键包含当前 schema，所以同一个隔离库中的其他测试不会触碰本屏障。
type scanFeedbackGate struct {
	conn     *sql.Conn
	app      *integrationTestApplication
	pid      int
	released bool
}

func newScanFeedbackGate(t *testing.T, app *integrationTestApplication, table, event, condition string) *scanFeedbackGate {
	t.Helper()
	conn, err := app.database.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	gate := &scanFeedbackGate{conn: conn, app: app}
	t.Cleanup(func() { gate.release(t); _ = conn.Close() })
	if err = conn.QueryRowContext(context.Background(), `SELECT pg_backend_pid()`).Scan(&gate.pid); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.ExecContext(context.Background(), `SELECT pg_advisory_lock(87431,hashtext(current_schema()))`); err != nil {
		t.Fatal(err)
	}
	mustExec(t, app, `CREATE FUNCTION scan_feedback_gate() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(87431,hashtext(current_schema())); RETURN NEW; END $$`)
	mustExec(t, app, fmt.Sprintf(`CREATE TRIGGER scan_feedback_gate AFTER %s ON %s FOR EACH ROW WHEN (%s) EXECUTE FUNCTION scan_feedback_gate()`, event, table, condition))
	return gate
}

func (g *scanFeedbackGate) release(t *testing.T) {
	t.Helper()
	if g.released {
		return
	}
	g.released = true
	if _, err := g.conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(87431,hashtext(current_schema()))`); err != nil {
		t.Errorf("release gate: %v", err)
	}
}

// 只接受指定连接阻塞的请求，不把其他测试的等待误认为本场景已交错。
func waitScanFeedbackBlockedBy(t *testing.T, db *sql.DB, pid int) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiter int
		err := db.QueryRowContext(ctx, `SELECT pid FROM pg_stat_activity WHERE datname=current_database() AND $1=ANY(pg_blocking_pids(pid)) ORDER BY pid LIMIT 1`, pid).Scan(&waiter)
		if err == nil {
			return waiter
		}
		if err != sql.ErrNoRows {
			t.Fatalf("wait for pid %d: %v\n%s", pid, err, describeDatabaseSessions(db))
		}
		select {
		case <-ctx.Done():
			t.Fatalf("no waiter blocked by %d\n%s", pid, describeDatabaseSessions(db))
		case <-ticker.C:
		}
	}
}

func awaitScanFeedbackResponse(t *testing.T, responses <-chan *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	t.Helper()
	select {
	case response := <-responses:
		if response.Code != 200 {
			t.Fatalf("governance: HTTP %d: %s", response.Code, response.Body.String())
		}
		return response
	case <-time.After(15 * time.Second):
		t.Fatal("governance did not release its locks")
		return nil
	}
}

func scanFeedbackReport(t *testing.T, app *integrationTestApplication, admin *integrationTestUser, id string) map[string]any {
	t.Helper()
	if status := waitForScanToFinish(t, app, id); status != "succeeded" {
		t.Fatalf("scan status=%s", status)
	}
	response := app.request(t, http.MethodGet, "/api/v1/scans/"+id+"/report", nil, admin, nil)
	if response.Code != 200 {
		t.Fatalf("report HTTP %d: %s", response.Code, response.Body.String())
	}
	return decodeJSONMap(t, response)
}

func assertScanFeedbackCounts(t *testing.T, report map[string]any, albums, tracks [3]int) {
	t.Helper()
	for kind, want := range map[string][3]int{"albums": albums, "tracks": tracks} {
		actual := report[kind].(map[string]any)
		for i, key := range []string{"added", "updated", "missing"} {
			if actual[key] != float64(want[i]) {
				t.Fatalf("%s.%s=%v want %d; report=%v", kind, key, actual[key], want[i], report)
			}
		}
	}
}

func scanFeedbackReleaseState(t *testing.T, app *integrationTestApplication, id string) (group string, revision int) {
	t.Helper()
	if err := app.database.QueryRow(`SELECT group_id::text,edit_revision FROM releases WHERE id=$1`, id).Scan(&group, &revision); err != nil {
		t.Fatal(err)
	}
	return
}

func TestPostgreSQLScanFeedbackGovernanceRegroup(t *testing.T) {
	for _, governanceFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("governance_first_%t", governanceFirst), func(t *testing.T) {
			env := newClaimRescanEnvironment(t, "a", "b")
			app := env.application
			a, _ := env.releaseIDs(t, "a")
			b, _ := env.releaseIDs(t, "b")
			groupA, _ := scanFeedbackReleaseState(t, app, a)
			groupB, _ := scanFeedbackReleaseState(t, app, b)
			env.write(t, "a", 1, 11, "Updated")
			condition := fmt.Sprintf("NEW.id='%s'::uuid AND NEW.edit_revision=OLD.edit_revision", a)
			if governanceFirst {
				condition = fmt.Sprintf("NEW.id='%s'::uuid AND NEW.group_id<>OLD.group_id", a)
			}
			gate := newScanFeedbackGate(t, app, "releases", "UPDATE", condition)
			responses := make(chan *httptest.ResponseRecorder, 1)
			govern := func() {
				responses <- app.request(t, http.MethodPost, "/api/v1/releases/"+a+"/group", joinBody(1, groupB), &env.admin, idempotencyHeader(uuid.New()))
			}
			var scan string
			if governanceFirst {
				go govern()
				pid := waitScanFeedbackBlockedBy(t, app.database, gate.pid)
				scan = startScanFeedback(t, app, &env.admin)
				waitScanFeedbackBlockedBy(t, app.database, pid)
			} else {
				scan = startScanFeedback(t, app, &env.admin)
				pid := waitScanFeedbackBlockedBy(t, app.database, gate.pid)
				go govern()
				waitScanFeedbackBlockedBy(t, app.database, pid)
			}
			gate.release(t)
			awaitScanFeedbackResponse(t, responses)
			report := scanFeedbackReport(t, app, &env.admin, scan)
			assertScanFeedbackCounts(t, report, [3]int{0, 1, 0}, [3]int{0, 1, 0})
			if group, rev := scanFeedbackReleaseState(t, app, a); group != groupB || rev != 2 {
				t.Fatalf("group/revision=%s/%d", group, rev)
			}
			// 治理先完成时必须归入新组；扫描先完成时事件保留旧组，而不是按最后图重算。
			want := groupA
			if governanceFirst {
				want = groupB
			}
			var recorded string
			if err := app.database.QueryRow(`SELECT entity_id::text FROM scan_catalog_changes WHERE scan_run_id=$1 AND entity_kind='album' AND classification='updated'`, scan).Scan(&recorded); err != nil {
				t.Fatal(err)
			}
			if recorded != want {
				t.Fatalf("scan bucket=%s want %s (torn group snapshot)", recorded, want)
			}
			if countRows(t, app, `SELECT COUNT(*) FROM operations WHERE operation_type='release_group_change'`) != 1 {
				t.Fatal("scan wrote governance operations")
			}
		})
	}
}

// 修改、默认版本和对应撤销均在扫描已持发行/组锁时开始；还检查人工版本号与覆盖层。
func TestPostgreSQLScanFeedbackGovernanceManualState(t *testing.T) {
	for _, entry := range []string{"candidate", "artwork", "missing"} {
		for _, kind := range []string{"correction", "correction_revert", "default", "default_clear", "default_revert", "regroup", "regroup_revert", "review", "review_revert"} {
			t.Run(entry+"/"+kind, func(t *testing.T) {
				env := newClaimRescanEnvironment(t, "a", "b")
				app := env.application
				a, _ := env.releaseIDs(t, "a")
				b, _ := env.releaseIDs(t, "b")
				groupA, _ := scanFeedbackReleaseState(t, app, a)
				wantGroup := groupA
				method, path, body := http.MethodPatch, "/api/v1/releases/"+a+"/corrections", any(correctionBody(1, map[string]any{"title": setChange("人工标题")}))
				wantRevision, wantCorrections, wantReviews := 2, 1, 0
				if kind == "default" || kind == "default_clear" || kind == "default_revert" {
					method, path, body = http.MethodPost, defaultPath(groupA), defaultBody(1, a)
					wantRevision, wantCorrections = 1, 0
				}
				if kind == "default_clear" {
					prepared := app.request(t, method, path, body, &env.admin, idempotencyHeader(uuid.New()))
					if prepared.Code != 200 {
						t.Fatalf("prepare default: %s", prepared.Body.String())
					}
					body = defaultBody(2, nil)
				}
				if kind == "regroup" || kind == "regroup_revert" {
					method, path, body = http.MethodPost, "/api/v1/releases/"+a+"/group", joinBody(1, func() string { g, _ := scanFeedbackReleaseState(t, app, b); return g }())
					wantCorrections = 0
					if kind == "regroup" {
						wantGroup, _ = scanFeedbackReleaseState(t, app, b)
					}
				}
				if kind == "review" || kind == "review_revert" {
					// 可核对的 missing 依据保持不变；扫描不会把它恢复，也不将已有 missing 再计消失。
					mustExec(t, app, `INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,source_kind) SELECT $2,m.id,9,'缺失','Pianist',1,r.source_root_id,'a/09.flac','missing',NOW(),'physical' FROM media m JOIN releases r ON r.id=m.release_id WHERE r.id=$1`, a, uuid.New())
					token, err := env.store.LoadIssueBasisToken(context.Background(), a, "missing_tracks")
					if err != nil {
						t.Fatal(err)
					}
					method, path, body = http.MethodPost, issueReviewPath(a), reviewBody(1, "missing_tracks", token)
					wantCorrections, wantReviews = 0, 1
				}
				if kind == "correction_revert" || kind == "default_revert" || kind == "regroup_revert" || kind == "review_revert" {
					prepared := app.request(t, method, path, body, &env.admin, idempotencyHeader(uuid.New()))
					if prepared.Code != 200 {
						t.Fatalf("prepare: %s", prepared.Body.String())
					}
					method, path, body = http.MethodPost, revertPath(decodeJSONMap(t, prepared)["operation_id"].(string)), nil
					wantCorrections, wantReviews = 0, 0
					if kind != "default_revert" {
						wantRevision = 3
					}
				}
				bucketBefore, _ := scanFeedbackReleaseState(t, app, a)
				gate, wantTracks := prepareScanFeedbackMutation(t, env, "a", a, 11, entry)
				scan := startScanFeedback(t, app, &env.admin)
				scanPID := waitScanFeedbackBlockedBy(t, app.database, gate.pid)
				responses := make(chan *httptest.ResponseRecorder, 1)
				go func() { responses <- app.request(t, method, path, body, &env.admin, idempotencyHeader(uuid.New())) }()
				waitScanFeedbackBlockedBy(t, app.database, scanPID)
				assertScanFeedbackRevertLock(t, app, path)
				gate.release(t)
				if entry == "missing" && kind == "review" {
					select {
					case response := <-responses:
						requireAPIError(t, response, 409, "issue_changed")
					case <-time.After(15 * time.Second):
						t.Fatal("review did not finish")
					}
					wantRevision, wantReviews = 1, 0
				} else {
					awaitScanFeedbackResponse(t, responses)
				}
				assertScanFeedbackCounts(t, scanFeedbackReport(t, app, &env.admin, scan), [3]int{0, 1, 0}, wantTracks)
				assertScanFeedbackAlbumBucket(t, app, scan, bucketBefore)
				group, revision := scanFeedbackReleaseState(t, app, a)
				if group != wantGroup || revision != wantRevision {
					t.Fatalf("manual state=%s/%d want %s/%d", group, revision, wantGroup, wantRevision)
				}
				if n := countRows(t, app, `SELECT COUNT(*) FROM release_corrections`); n != wantCorrections {
					t.Fatalf("corrections=%d", n)
				}
				if n := countRows(t, app, `SELECT COUNT(*) FROM release_issue_reviews`); n != wantReviews {
					t.Fatalf("reviews=%d", n)
				}
				if kind == "default" || kind == "default_clear" || kind == "default_revert" {
					var stored string
					var revision int
					if err := app.database.QueryRow(`SELECT COALESCE(default_release_id::text,''),edit_revision FROM release_groups WHERE id=$1`, groupA).Scan(&stored, &revision); err != nil {
						t.Fatal(err)
					}
					want, wantRev := a, 2
					if kind == "default_revert" || kind == "default_clear" {
						want, wantRev = "", 3
					}
					if stored != want || revision != wantRev {
						t.Fatalf("default=%s rev=%d", stored, revision)
					}
				}
			})
		}
	}
}

func TestPostgreSQLScanFeedbackGovernanceFeedbackLockTimeout(t *testing.T) {
	env := newClaimRescanEnvironment(t, "a")
	app := env.application
	a, _ := env.releaseIDs(t, "a")
	group, _ := scanFeedbackReleaseState(t, app, a)
	holder, err := app.database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback()
	var holderPID int
	if err = holder.QueryRow(`SELECT pg_backend_pid()`).Scan(&holderPID); err != nil {
		t.Fatal(err)
	}
	if _, err = holder.Exec(`SELECT id FROM release_groups WHERE id=$1 FOR UPDATE`, group); err != nil {
		t.Fatal(err)
	}
	env.write(t, "a", 1, 11, "Updated")
	scan := startScanFeedback(t, app, &env.admin)
	waitScanFeedbackBlockedBy(t, app.database, holderPID)
	// 保持组写锁直到扫描结束：反馈必须回滚额外锁而非把 timeout 扩散到核心 SQL。
	if status := waitForScanToFinish(t, app, scan); status != "succeeded" {
		t.Fatalf("feedback competition changed scan result: %s", status)
	}
	response := app.request(t, http.MethodGet, "/api/v1/scans/"+scan+"/report", nil, &env.admin, nil)
	requireAPIError(t, response, 503, "scan_report_unavailable")
	if n := countRows(t, app, `SELECT COUNT(*) FROM tracks WHERE title LIKE 'Updated%'`); n != 1 {
		t.Fatalf("core mutation did not commit: %d", n)
	}
	if _, err = holder.Exec(`SELECT id FROM releases WHERE id=$1 FOR UPDATE NOWAIT`, a); err != nil {
		t.Fatalf("feedback release lock leaked: %v", err)
	}
	if err = holder.Rollback(); err != nil {
		t.Fatal(err)
	}
	assertScanFeedbackCounts(t, scanFeedbackReport(t, app, &env.admin, startScanFeedback(t, app, &env.admin)), [3]int{}, [3]int{})
}

func TestPostgreSQLScanFeedbackGovernanceFinalizeMissing(t *testing.T) {
	env := newClaimRescanEnvironment(t, "a", "b")
	app := env.application
	a, _ := env.releaseIDs(t, "a")
	env.correct(t, a, "人工标题")
	// 扫描后置 missing 与 succeeded 在同一事务，治理撤销只能在其释放锁后继续。
	var operation string
	if err := app.database.QueryRow(`SELECT id::text FROM operations WHERE operation_type='release_correction'`).Scan(&operation); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(env.library, "a", "01.flac")); err != nil {
		t.Fatal(err)
	}
	gate := newScanFeedbackGate(t, app, "tracks", "UPDATE", "OLD.source_status='present' AND NEW.source_status='missing'")
	scan := startScanFeedback(t, app, &env.admin)
	pid := waitScanFeedbackBlockedBy(t, app.database, gate.pid)
	var state, source string
	if err := app.database.QueryRow(`SELECT s.status,t.source_status FROM scan_runs s CROSS JOIN tracks t WHERE s.id=$1 AND t.relative_path='a/01.flac'`, scan).Scan(&state, &source); err != nil {
		t.Fatal(err)
	}
	if state != "running" || source != "present" {
		t.Fatalf("uncommitted finalization leaked: %s/%s", state, source)
	}
	responses := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		responses <- app.request(t, http.MethodPost, revertPath(operation), nil, &env.admin, idempotencyHeader(uuid.New()))
	}()
	waitScanFeedbackBlockedBy(t, app.database, pid)
	gate.release(t)
	awaitScanFeedbackResponse(t, responses)
	report := scanFeedbackReport(t, app, &env.admin, scan)
	assertScanFeedbackCounts(t, report, [3]int{0, 1, 0}, [3]int{0, 0, 1})
	if report["missing_reconciled"] != true {
		t.Fatal("missing report not reconciled")
	}
	if countRows(t, app, `SELECT COUNT(*) FROM release_corrections`) != 0 {
		t.Fatal("revert did not clear correction")
	}
}

func TestPostgreSQLScanFeedbackGovernanceClaimAndRevert(t *testing.T) {
	for _, entry := range []string{"candidate", "artwork", "missing"} {
		for _, revert := range []bool{false, true} {
			t.Run(entry+fmt.Sprintf("/revert_%t", revert), func(t *testing.T) {
				env := newClaimRescanEnvironment(t, "a", "b")
				app := env.application
				old, _ := env.releaseIDs(t, "a")
				b, _ := env.releaseIDs(t, "b")
				groupB, _ := scanFeedbackReleaseState(t, app, b)
				env.correct(t, old, "旧版本的人工标题")
				joined := app.request(t, http.MethodPost, "/api/v1/releases/"+old+"/group", joinBody(2, groupB), &env.admin, idempotencyHeader(uuid.New()))
				if joined.Code != 200 {
					t.Fatalf("join: %s", joined.Body.String())
				}
				for n := 1; n <= 2; n++ {
					env.write(t, "a", n, byte(100+n), "Replaced")
				}
				runScan(t, app, &env.admin)
				target, _ := env.releaseIDs(t, "a")
				originalGroup, _ := scanFeedbackReleaseState(t, app, target)
				preview := decodeJSONMap(t, app.request(t, http.MethodGet, claimPreviewPath(target, old), nil, &env.admin, nil))
				path := claimPath(target)
				body := any(claimBody(int64(preview["revision"].(float64)), int64(preview["source_revision"].(float64)), old, preview["token"].(string)))
				wantGroup, wantRevision, wantCorrection := groupB, 2, target
				if revert {
					claimed := app.request(t, http.MethodPost, path, body, &env.admin, idempotencyHeader(uuid.New()))
					if claimed.Code != 200 {
						t.Fatalf("claim: %s", claimed.Body.String())
					}
					path, body = revertPath(decodeJSONMap(t, claimed)["operation_id"].(string)), nil
					wantGroup, wantRevision, wantCorrection = originalGroup, 3, old
				}
				bucketBefore, _ := scanFeedbackReleaseState(t, app, target)
				gate, wantTracks := prepareScanFeedbackMutation(t, env, "a", target, 101, entry)
				scan := startScanFeedback(t, app, &env.admin)
				pid := waitScanFeedbackBlockedBy(t, app.database, gate.pid)
				responses := make(chan *httptest.ResponseRecorder, 1)
				go func() {
					responses <- app.request(t, http.MethodPost, path, body, &env.admin, idempotencyHeader(uuid.New()))
				}()
				waitScanFeedbackBlockedBy(t, app.database, pid)
				assertScanFeedbackRevertLock(t, app, path)
				gate.release(t)
				awaitScanFeedbackResponse(t, responses)
				assertScanFeedbackCounts(t, scanFeedbackReport(t, app, &env.admin, scan), [3]int{0, 1, 0}, wantTracks)
				assertScanFeedbackAlbumBucket(t, app, scan, bucketBefore)
				if group, revision := scanFeedbackReleaseState(t, app, target); group != wantGroup || revision != wantRevision {
					t.Fatalf("claim graph=%s/%d want %s/%d", group, revision, wantGroup, wantRevision)
				}
				var owner string
				if err := app.database.QueryRow(`SELECT release_id::text FROM release_corrections WHERE field='title'`).Scan(&owner); err != nil {
					t.Fatal(err)
				}
				if owner != wantCorrection {
					t.Fatalf("correction owner=%s want %s", owner, wantCorrection)
				}
				if countRows(t, app, `SELECT COUNT(*) FROM tracks WHERE source_status='missing'`) != 2+wantTracks[2] {
					t.Fatal("claim or rescan changed old missing sources")
				}
			})
		}
	}
}

func TestPostgreSQLScanFeedbackGovernanceCancelHolderLossReleasesLocks(t *testing.T) {
	for _, commitFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("prior_candidate_committed_%t", commitFirst), func(t *testing.T) {
			env := newClaimRescanEnvironment(t, "a", "b")
			app := env.application
			album, changed := "a", 0
			if commitFirst {
				album, changed = "b", 1
			}
			target, _ := env.releaseIDs(t, album)
			env.write(t, "a", 1, 11, "Updated")
			env.write(t, "b", 1, 21, "Updated")
			gate := newScanFeedbackGate(t, app, "releases", "UPDATE", fmt.Sprintf("NEW.id='%s'::uuid AND NEW.edit_revision=OLD.edit_revision", target))
			scan := startScanFeedback(t, app, &env.admin)
			holderPID := waitScanFeedbackBlockedBy(t, app.database, gate.pid)
			var progressBefore string
			if err := app.database.QueryRow(`SELECT (to_jsonb(scan_runs)-ARRAY['status','finished_at','cancel_requested_at','report_state','error_message'])::text FROM scan_runs WHERE id=$1`, scan).Scan(&progressBefore); err != nil {
				t.Fatal(err)
			}
			canceled := app.request(t, http.MethodPost, "/api/v1/scans/"+scan+"/cancel", nil, &env.admin, nil)
			if canceled.Code != 202 {
				t.Fatalf("cancel HTTP %d: %s", canceled.Code, canceled.Body.String())
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := app.app.WaitForScans(ctx); err != nil {
				t.Fatalf("canceled SQL worker did not exit: %v", err)
			}
			// SQL 正在等锁时 context 取消会使 pgx 丢弃 holder；必须证明会话和锁已经释放，不能宽松接受 running。
			ticker := time.NewTicker(5 * time.Millisecond)
			defer ticker.Stop()
			for {
				var alive bool
				if err := app.database.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1)`, holderPID).Scan(&alive); err != nil {
					t.Fatal(err)
				}
				if !alive {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatalf("canceled holder still alive: %s", describeDatabaseSessions(app.database))
				case <-ticker.C:
				}
			}
			if err := library.RecoverInterruptedScans(ctx, app.database); err != nil {
				t.Fatalf("recover after canceled holder loss: %v", err)
			}
			var status, reportState, progressAfter string
			if err := app.database.QueryRow(`SELECT status,report_state,(to_jsonb(scan_runs)-ARRAY['status','finished_at','cancel_requested_at','report_state','error_message'])::text FROM scan_runs WHERE id=$1`, scan).Scan(&status, &reportState, &progressAfter); err != nil {
				t.Fatal(err)
			}
			if status != "incomplete" || reportState != "unavailable" || progressAfter != progressBefore {
				t.Fatalf("recovery=%s/%s progress=%s want %s", status, reportState, progressAfter, progressBefore)
			}
			// 不释放屏障也必须完成退出与恢复；先前已提交的候选保留，当前候选回滚。
			if n := countRows(t, app, `SELECT COUNT(*) FROM tracks WHERE title LIKE 'Updated%'`); n != changed {
				t.Fatalf("committed changes=%d want %d", n, changed)
			}
			if n := countRows(t, app, `SELECT COUNT(*) FROM tracks WHERE source_status='missing'`); n != 0 {
				t.Fatalf("cancel reconciled %d missing tracks", n)
			}
			if n := countRows(t, app, `SELECT COUNT(*) FROM scan_observations`); n != 0 {
				t.Fatalf("cancel retained %d observations", n)
			}
			response := app.request(t, http.MethodGet, "/api/v1/scans/"+scan+"/report", nil, &env.admin, nil)
			requireAPIError(t, response, 503, "scan_report_unavailable")
			gate.release(t)
			// 下一次真实扫描重新获得 holder 和候选锁，没有执行权或事务锁泄漏。
			assertScanFeedbackCounts(t, scanFeedbackReport(t, app, &env.admin, startScanFeedback(t, app, &env.admin)), [3]int{0, 2 - changed, 0}, [3]int{0, 2 - changed, 0})
		})
	}
}

// 治理随后移组或撤销时，冻结的扫描统计仍归入扫描持锁读取到的组。
func assertScanFeedbackAlbumBucket(t *testing.T, app *integrationTestApplication, scanID, wantGroup string) {
	t.Helper()
	var group string
	if err := app.database.QueryRow(`SELECT entity_id::text FROM scan_catalog_changes WHERE scan_run_id=$1 AND entity_kind='album' AND classification='updated'`, scanID).Scan(&group); err != nil {
		t.Fatal(err)
	}
	if group != wantGroup {
		t.Fatalf("frozen album bucket=%s want %s", group, wantGroup)
	}
}

// 撤销等待发行锁时已经持有原操作记录锁，防止遗漏“记录→发行→组”的第一段交错。
func assertScanFeedbackRevertLock(t *testing.T, app *integrationTestApplication, path string) {
	t.Helper()
	const prefix = "/api/v1/operations/"
	if !strings.HasPrefix(path, prefix) {
		return
	}
	operation := strings.TrimSuffix(strings.TrimPrefix(path, prefix), "/revert")
	var id string
	err := app.database.QueryRow(`SELECT id::text FROM operations WHERE id=$1 FOR UPDATE NOWAIT`, operation).Scan(&id)
	var state interface{ SQLState() string }
	if !errors.As(err, &state) || state.SQLState() != "55P03" {
		t.Fatalf("revert does not hold operation row while waiting: %v", err)
	}
}
