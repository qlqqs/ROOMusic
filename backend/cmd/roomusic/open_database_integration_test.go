package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/app"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
)

func TestPostgreSQLMigrationRollbackOpenDatabaseRefusalIsNotReady(t *testing.T) {
	address := postgrestest.NewSchema(t, "migration_open_refusal_")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := postgres.Open(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	latest, err := postgres.LatestMigrationVersion()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations(version) VALUES($1)`, latest+1); err != nil {
		t.Fatal(err)
	}
	opened, err := openDatabase(ctx, address)
	if opened != nil {
		opened.Close()
		t.Fatal("降级拒绝不能返回连接")
	}
	var ahead *postgres.SchemaAheadError
	if !errors.As(err, &ahead) || ahead.Reason != "missing_rollback" {
		t.Fatalf("openDatabase 未保留拒绝分类：%v", err)
	}
	handler := app.Degraded(frontendSub(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, path := range []string{"/readyz", "/api/v1/system/version"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusServiceUnavailable {
			t.Errorf("%s 应503，得到%d", path, recorder.Code)
		}
	}
	var maximum int64
	if err := db.QueryRowContext(ctx, `SELECT max(version) FROM schema_migrations`).Scan(&maximum); err != nil || maximum != latest+1 {
		t.Fatalf("拒绝修改了历史：%d %v", maximum, err)
	}
}
