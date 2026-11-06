package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func isSystemAPISample(name string) bool {
	return strings.HasPrefix(name, "system/") || strings.HasPrefix(name, "errors/backup-")
}
func TestPostgreSQLSystemAPIResponseSamples(t *testing.T) {
	dir := t.TempDir()
	app, _, runner := newSystemApplication(t, dir)
	admin := app.createUser(t, "admin")
	samples := newAPISampleRecorder(t, app)
	samples.scope = "system"
	record := func(name, method, path string, status int) {
		t.Helper()
		samples.record(name, app.request(t, method, path, nil, &admin, map[string]string{"X-Request-ID": sampleRequestID}), status)
	}
	record("system/version-dev", "GET", "/api/v1/system/version", 200)
	record("system/backups-empty", "GET", "/api/v1/system/backups", 200)
	files := []struct {
		name, at string
		size     int
	}{
		{"roomusic-20261106-030012.dump", "2026-11-05T19:00:12Z", 1572864},
		{"roomusic-20261105-221530-pre-restore.dump", "2026-11-05T14:15:30Z", 1561210},
		{"roomusic-20261105-090501-pre-migration-17.dump", "2026-11-05T01:05:01Z", 1498112},
		{"roomusic-20261104-153000-manual.dump", "2026-11-04T07:30:00Z", 1480004},
	}
	for _, file := range files {
		at, err := time.Parse(time.RFC3339, file.at)
		if err != nil {
			t.Fatal(err)
		}
		systemWriteFile(t, dir, file.name, make([]byte, file.size), at)
	}
	// 状态由第二个Service读取磁盘，验证失败记录的持久化合同。
	state := []byte(`{"version":1,"last_failure":{"at":"2026-11-06T11:00:00Z","stage":"pg_dump","source":"scheduled"}}`)
	if err := os.WriteFile(filepath.Join(dir, ".roomusic-backup-state.json"), state, 0600); err != nil {
		t.Fatal(err)
	}
	withFailure, _, _ := newSystemApplication(t, dir)
	failureAdmin := withFailure.createUser(t, "admin")
	samples.record("system/backups", withFailure.request(t, "GET", "/api/v1/system/backups", nil, &failureAdmin, map[string]string{"X-Request-ID": sampleRequestID}), 200)
	missing, _, _ := newSystemApplication(t, filepath.Join(t.TempDir(), "missing"))
	missingAdmin := missing.createUser(t, "admin")
	samples.record("system/backups-directory-missing", missing.request(t, "GET", "/api/v1/system/backups", nil, &missingAdmin, map[string]string{"X-Request-ID": sampleRequestID}), 200)
	record("errors/backup-not-found", "GET", "/api/v1/system/backups/roomusic-x.dump", 404)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	holder, err := app.database.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := holder.ExecContext(ctx, `SELECT pg_advisory_lock($1::bigint)`, systemBackupLock); err != nil {
		holder.Close()
		t.Fatal(err)
	}
	record("errors/backup-busy", "POST", "/api/v1/system/backups", 409)
	if _, err := holder.ExecContext(ctx, `SELECT pg_advisory_unlock($1::bigint)`, systemBackupLock); err != nil {
		holder.Close()
		t.Fatal(err)
	}
	holder.Close()
	record("system/backup-started", "POST", "/api/v1/system/backups", 202)
	select {
	case <-runner.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	record("system/backups-running", "GET", "/api/v1/system/backups", 200)
	record("system/backup-already-running", "POST", "/api/v1/system/backups", 200)
	samples.finish()
}
