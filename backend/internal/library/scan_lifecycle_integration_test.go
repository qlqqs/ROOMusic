package library

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/library/parser"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPostgreSQLSaveObservationCreatesSeparateDiscMedia(t *testing.T) {
	application := newLibraryFixture(t)
	rootID := uuid.New()
	rootPath := filepath.Join(application.allowedRoot, "multi-disc")
	if err := os.MkdirAll(rootPath, 0o755); err != nil {
		t.Fatalf("create root fixture: %v", err)
	}
	if _, err := application.database.Exec(`INSERT INTO library_roots(id,path,status,revision) VALUES($1::uuid,$2,'active',1)`, rootID, rootPath); err != nil {
		t.Fatalf("insert root fixture: %v", err)
	}
	scanID := uuid.New()
	if _, err := application.database.Exec(`INSERT INTO scan_runs(id,status,started_at) VALUES($1::uuid,'running',NOW())`, scanID); err != nil {
		t.Fatalf("insert scan fixture: %v", err)
	}
	root := registeredRoot{ID: rootID, Path: rootPath}
	roomusic := NewService(nil, application.database, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, disc := range []int{1, 2} {
		observation := parser.AudioObservation{Album: "Two Disc", Artist: "Artist", Title: fmt.Sprintf("Track %d", disc), TrackNumber: 1, DiscNumber: disc}
		if err := saveAudioObservation(context.Background(), roomusic, application, scanID, root, fmt.Sprintf("disc%d/track.flac", disc), observation); err != nil {
			t.Fatalf("save disc %d observation: %v", disc, err)
		}
	}
	var mediaCount int
	if err := application.database.QueryRow(`SELECT COUNT(*) FROM media m JOIN releases r ON r.id=m.release_id WHERE r.source_root_id=$1::uuid`, rootID).Scan(&mediaCount); err != nil {
		t.Fatalf("count media: %v", err)
	}
	if mediaCount != 2 {
		t.Fatalf("expected two media rows, got %d", mediaCount)
	}
}

func TestPostgreSQLScanRecoverySkipsLiveHolder(t *testing.T) {
	roomusic := newLibraryFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	scanID := uuid.New()
	if _, err := roomusic.database.ExecContext(ctx, `INSERT INTO scan_runs(id,status,started_at) VALUES($1::uuid,'running',NOW())`, scanID); err != nil {
		t.Fatalf("insert running scan: %v", err)
	}
	holder, acquired, err := acquireScanExecution(ctx, roomusic.database)
	cleanupScanHolder(t, holder)
	if err != nil || !acquired {
		t.Fatalf("acquire live holder: acquired=%v err=%v", acquired, err)
	}

	if err := RecoverInterruptedScans(ctx, roomusic.database); err != nil {
		t.Fatalf("recover while holder is live: %v", err)
	}
	var status string
	if err := roomusic.database.QueryRowContext(ctx, `SELECT status FROM scan_runs WHERE id=$1::uuid`, scanID).Scan(&status); err != nil {
		t.Fatalf("query scan status: %v", err)
	}
	if status != "running" {
		t.Fatalf("live scan was recovered as %q", status)
	}

	if err := holder.close(ctx); err != nil {
		t.Fatalf("release live holder: %v", err)
	}
	if err := RecoverInterruptedScans(ctx, roomusic.database); err != nil {
		t.Fatalf("recover stale scan: %v", err)
	}
	if err := roomusic.database.QueryRowContext(ctx, `SELECT status FROM scan_runs WHERE id=$1::uuid`, scanID).Scan(&status); err != nil {
		t.Fatalf("query recovered scan status: %v", err)
	}
	if status != "incomplete" {
		t.Fatalf("stale scan status = %q, want incomplete", status)
	}
	var total, recovered int
	if err := roomusic.database.QueryRowContext(ctx, `SELECT COUNT(*),COUNT(*) FILTER (WHERE id=$1::uuid AND status='incomplete' AND finished_at IS NOT NULL AND error_message='process_restarted') FROM scan_runs`, scanID).Scan(&total, &recovered); err != nil {
		t.Fatalf("query recovered scan rows: %v", err)
	}
	if total != 1 || recovered != 1 {
		t.Fatalf("recovery created or replaced a scan: total=%d original_recovered=%d", total, recovered)
	}
}

func TestPostgreSQLCanceledScanDoesNotReconcileMissingSources(t *testing.T) {
	application := newLibraryFixture(t)
	rootID := uuid.New()
	rootPath := filepath.Join(application.allowedRoot, "cancel-safe")
	if err := os.MkdirAll(rootPath, 0o755); err != nil {
		t.Fatalf("create root fixture: %v", err)
	}
	if _, err := application.database.Exec(`INSERT INTO library_roots(id,path,status,revision) VALUES($1::uuid,$2,'active',1)`, rootID, rootPath); err != nil {
		t.Fatalf("insert root fixture: %v", err)
	}
	scanID := uuid.New()
	if _, err := application.database.Exec(`INSERT INTO scan_runs(id,status,started_at,cancel_requested_at) VALUES($1::uuid,'running',NOW(),NOW())`, scanID); err != nil {
		t.Fatalf("insert canceled scan fixture: %v", err)
	}
	roomusic := NewService(nil, application.database, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	root := registeredRoot{ID: rootID, Path: rootPath}
	observation := parser.AudioObservation{Album: "Album", Artist: "Artist", Title: "Track", TrackNumber: 1, DiscNumber: 1}
	if err := saveAudioObservation(context.Background(), roomusic, application, scanID, root, "track.flac", observation); err != nil {
		t.Fatalf("save observation: %v", err)
	}
	if _, err := application.database.Exec(`UPDATE tracks SET observed_at=NOW()-INTERVAL '1 hour' WHERE source_root_id=$1::uuid`, rootID); err != nil {
		t.Fatalf("age observation: %v", err)
	}
	holder, acquired, err := acquireScanExecution(context.Background(), application.database)
	if err != nil || !acquired {
		t.Fatalf("acquire scan holder: acquired=%v err=%v", acquired, err)
	}
	defer holder.close(context.Background())
	status, err := roomusic.finalizeScan(context.Background(), holder.connection, scanID, []string{rootID}, scanOutcome{Complete: true})
	if err != nil {
		t.Fatalf("finalize canceled scan: %v", err)
	}
	if status != "canceled" {
		t.Fatalf("terminal status = %q, want canceled", status)
	}
	var sourceStatus string
	if err := application.database.QueryRow(`SELECT source_status FROM tracks WHERE source_root_id=$1::uuid`, rootID).Scan(&sourceStatus); err != nil {
		t.Fatalf("query source status: %v", err)
	}
	if sourceStatus != "present" {
		t.Fatalf("canceled scan marked source %q", sourceStatus)
	}
}
