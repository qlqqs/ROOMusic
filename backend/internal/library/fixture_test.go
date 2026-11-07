package library

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/testsupport"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
)

// libraryFixture 是隔离 schema 的已迁移测试库加一个临时允许根；不含 HTTP。
type libraryFixture struct {
	database    *sql.DB
	allowedRoot string
}

func newLibraryFixture(t *testing.T) *libraryFixture {
	t.Helper()
	scopedURL := postgrestest.NewSchema(t, "roomusic_test_")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	database, err := postgres.Open(ctx, scopedURL)
	if err != nil {
		t.Fatalf("open isolated library database: %v", err)
	}
	if err := testsupport.SetArtistProjectionVersionCurrent(ctx, database); err != nil {
		t.Fatalf("set current artist projection version: %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close isolated library database: %v", err)
		}
	})
	return &libraryFixture{database: database, allowedRoot: t.TempDir()}
}
