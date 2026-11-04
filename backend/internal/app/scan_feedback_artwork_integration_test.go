package app

import (
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPostgreSQLScanFeedbackGovernanceArtwork(t *testing.T) {
	env := newClaimRescanEnvironment(t, "a")
	app := env.application
	a, _ := env.releaseIDs(t, "a")
	group, _ := scanFeedbackReleaseState(t, app, a)
	file, err := os.Create(filepath.Join(env.library, "a", "cover.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err = png.Encode(file, image.NewNRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	gate := newScanFeedbackGate(t, app, "release_artworks", "INSERT", fmt.Sprintf("NEW.release_id='%s'::uuid", a))
	scan := startScanFeedback(t, app, &env.admin)
	pid := waitScanFeedbackBlockedBy(t, app.database, gate.pid)
	// 候选已提交、封面另起事务；默认版本要等该事务的发行/组锁，而不是跨文件 I/O 持锁。
	if n := countRows(t, app, `SELECT COUNT(*) FROM tracks WHERE observed_at >= (SELECT started_at FROM scan_runs WHERE id=$1)`, scan); n != 2 {
		t.Fatalf("candidate not committed before artwork: %d", n)
	}
	responses := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		responses <- app.request(t, http.MethodPost, defaultPath(group), defaultBody(1, a), &env.admin, idempotencyHeader(uuid.New()))
	}()
	waitScanFeedbackBlockedBy(t, app.database, pid)
	gate.release(t)
	awaitScanFeedbackResponse(t, responses)
	assertScanFeedbackCounts(t, scanFeedbackReport(t, app, &env.admin, scan), [3]int{0, 1, 0}, [3]int{})
	if n := countRows(t, app, `SELECT COUNT(*) FROM release_artworks WHERE release_id=$1`, a); n != 1 {
		t.Fatal("artwork did not commit")
	}
	var stored string
	var revision int
	if err := app.database.QueryRow(`SELECT default_release_id::text,edit_revision FROM release_groups WHERE id=$1`, group).Scan(&stored, &revision); err != nil {
		t.Fatal(err)
	}
	if stored != a || revision != 2 {
		t.Fatalf("default overwritten: %s/%d", stored, revision)
	}
}

func TestPostgreSQLScanFeedbackGovernanceSourceVacateClaim(t *testing.T) {
	for _, moveTracks := range []bool{false, true} {
		t.Run(fmt.Sprintf("reparent_%t", moveTracks), func(t *testing.T) {
			env := newClaimRescanEnvironment(t, "a", "b")
			app := env.application
			source, _ := env.releaseIDs(t, "a")
			target, _ := env.releaseIDs(t, "b")
			sourceDir, targetDir := "a", "b"
			// 认领先锁较小 UUID 的旧来源，再等待扫描占用的较大发行，不能与扫描的组锁形成环。
			if source > target {
				source, target = target, source
				sourceDir, targetDir = targetDir, sourceDir
			}
			env.correct(t, source, "保留旧人工标题")
			removeScanFeedbackAlbum(t, env, sourceDir)
			runScan(t, app, &env.admin)
			preview := decodeJSONMap(t, app.request(t, http.MethodGet, claimPreviewPath(target, source), nil, &env.admin, nil))
			originalGroup, _ := scanFeedbackReleaseState(t, app, target)
			condition := "OLD.source_status='present' AND NEW.source_status='missing'"
			wantTracks := [3]int{2, 0, 2}
			if moveTracks {
				for n := 3; n <= 5; n++ {
					env.write(t, targetDir, n, byte(100+n), "Added")
				}
				condition = "NEW.medium_id<>OLD.medium_id"
				wantTracks = [3]int{3, 2, 0}
			} else {
				for n := 1; n <= 2; n++ {
					env.write(t, targetDir, n, byte(100+n), "Replacement")
				}
			}
			gate := newScanFeedbackGate(t, app, "tracks", "UPDATE", condition)
			scan := startScanFeedback(t, app, &env.admin)
			pid := waitScanFeedbackBlockedBy(t, app.database, gate.pid)
			responses := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				responses <- app.request(t, http.MethodPost, claimPath(target), claimBody(int64(preview["revision"].(float64)), int64(preview["source_revision"].(float64)), source, preview["token"].(string)), &env.admin, idempotencyHeader(uuid.New()))
			}()
			waitScanFeedbackBlockedBy(t, app.database, pid)
			gate.release(t)
			var response *httptest.ResponseRecorder
			select {
			case response = <-responses:
			case <-time.After(15 * time.Second):
				t.Fatal("claim did not finish")
			}
			requireAPIError(t, response, 404, "not_found")
			assertScanFeedbackCounts(t, scanFeedbackReport(t, app, &env.admin, scan), [3]int{1, 0, 1}, wantTracks)
			if group, revision := scanFeedbackReleaseState(t, app, target); group != originalGroup || revision != 1 {
				t.Fatalf("rejected claim changed target: %s/%d", group, revision)
			}
			if n := countRows(t, app, `SELECT COUNT(*) FROM release_corrections WHERE release_id=$1`, source); n != 1 {
				t.Fatal("rejected claim lost source correction")
			}
			if n := countRows(t, app, `SELECT COUNT(*) FROM operations WHERE operation_type='release_claim'`); n != 0 {
				t.Fatal("rejected claim was recorded")
			}
		})
	}
}

// 三个真实记录入口共享同一套治理请求/后态断言；每个入口只改变对应的扫描事实。
func prepareScanFeedbackMutation(t *testing.T, env *claimRescanEnvironment, album, releaseID string, fingerprint byte, entry string) (*scanFeedbackGate, [3]int) {
	t.Helper()
	switch entry {
	case "candidate":
		env.write(t, album, 1, fingerprint, "Updated")
		return newScanFeedbackGate(t, env.application, "releases", "UPDATE", fmt.Sprintf("NEW.id='%s'::uuid AND NEW.edit_revision=OLD.edit_revision", releaseID)), [3]int{0, 1, 0}
	case "artwork":
		file, err := os.Create(filepath.Join(env.library, album, "cover.png"))
		if err != nil {
			t.Fatal(err)
		}
		if err = png.Encode(file, image.NewNRGBA(image.Rect(0, 0, 4, 4))); err != nil {
			file.Close()
			t.Fatal(err)
		}
		if err = file.Close(); err != nil {
			t.Fatal(err)
		}
		return newScanFeedbackGate(t, env.application, "release_artworks", "INSERT", fmt.Sprintf("NEW.release_id='%s'::uuid", releaseID)), [3]int{}
	case "missing":
		if err := os.Remove(filepath.Join(env.library, album, "01.flac")); err != nil {
			t.Fatal(err)
		}
		return newScanFeedbackGate(t, env.application, "tracks", "UPDATE", "OLD.source_status='present' AND NEW.source_status='missing'"), [3]int{0, 0, 1}
	default:
		t.Fatalf("unknown scan entry %q", entry)
		return nil, [3]int{}
	}
}
