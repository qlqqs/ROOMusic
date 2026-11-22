package artwork_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/artwork"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPostgreSQLArtworkDescribeWriteDiscard(t *testing.T) {
	ctx := t.Context()
	db, err := postgres.Open(ctx, postgrestest.NewSchema(t, "artwork_storage_"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	store := artwork.NewStore(dir)
	data := []byte{'G', 'I', 'F', '8', '9', 'a', 2, 0, 3, 0}
	stored, err := store.Describe(data)
	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	want := artwork.Stored{Key: hash + ".gif", Hash: hash, MIMEType: "image/gif", Width: 2, Height: 3}
	if err != nil || stored != want {
		t.Fatalf("Describe=%+v，错误=%v，期望 %+v", stored, err, want)
	}
	if files, err := os.ReadDir(dir); err != nil || len(files) != 0 {
		t.Fatalf("Describe 后目录=%v，错误=%v，期望空目录", files, err)
	}
	path := filepath.Join(dir, "artwork", stored.Key)
	read := func() {
		t.Helper()
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("文件内容=%v，错误=%v，期望 %v", got, err, data)
		}
	}
	write := func() {
		t.Helper()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if err := store.Write(ctx, tx, data, stored); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		read()
	}
	write()
	stamp := time.Unix(100000, 0)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	write()
	if stat, err := os.Stat(path); err != nil || !stat.ModTime().Equal(stamp) {
		t.Fatalf("重复 Write 重写了封面，文件信息=%v，错误=%v", stat, err)
	}
	discard := func() {
		t.Helper()
		if err := store.Discard(ctx, db, stored.Key); err != nil {
			t.Fatal(err)
		}
	}
	group, release, user := uuid.New(), uuid.New(), uuid.New()
	exec(`INSERT INTO release_groups(id) VALUES($1)`, group)
	exec(`INSERT INTO releases(id,group_id,title,artist) VALUES($1,$2,'合成专辑','合成艺人')`, release, group)
	exec(`INSERT INTO release_artworks(release_id,content_hash,mime_type,width,height,storage_key,source_type)
		VALUES($1,$2,$3,$4,$5,$6,'embedded')`, release, hash, stored.MIMEType, stored.Width, stored.Height, stored.Key)
	discard()
	read()
	exec(`DELETE FROM release_artworks WHERE release_id=$1`, release)
	exec(`INSERT INTO users(id,username,password_hash) VALUES($1,'synthetic','unused')`, user)
	_, err = operations.Run(ctx, db, operations.Spec{ActorID: user, Type: operations.TypeReleaseExternalArtwork,
		Key: uuid.New(), Fingerprint: operations.Fingerprint(map[string]string{"release": release}),
	}, func(ctx context.Context, tx *sql.Tx, id string) (operations.Outcome, error) {
		_, err := tx.ExecContext(ctx, `INSERT INTO release_external_artworks(release_id,content_hash,mime_type,width,height,storage_key,source,external_id,operation_id)
			VALUES($1,$2,$3,$4,$5,$6,'coverartarchive','42',$7)`, release, hash, stored.MIMEType, stored.Width, stored.Height, stored.Key, id)
		return operations.Outcome{TargetType: operations.TargetRelease, TargetID: release, ExpectedRevision: 1, ResultRevision: 2,
			Before: map[string]any{}, After: map[string]any{}, Response: map[string]any{}}, err
	})
	if err != nil {
		t.Fatal(err)
	}
	discard()
	read()
	exec(`DELETE FROM release_external_artworks WHERE release_id=$1`, release)
	discard()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("无引用的文件仍可见，stat 错误=%v", err)
	}
}
