package artwork_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/artwork"
	"github.com/qlqq/roomusic/backend/internal/catalog/ingest"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPostgreSQLScanFeedbackArtworkChanges(t *testing.T) {
	ctx := context.Background()
	db, err := postgres.Open(ctx, postgrestest.NewSchema(t, "artwork_feedback_"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	root, group, release, medium := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	exec(`INSERT INTO library_roots(id,path) VALUES($1,'/synthetic')`, root)
	exec(`INSERT INTO release_groups(id) VALUES($1)`, group)
	exec(`INSERT INTO releases(id,group_id,title,artist) VALUES($1,$2,'Album','Artist')`, release, group)
	exec(`INSERT INTO media(id,release_id,position) VALUES($1,$2,1)`, medium, release)
	exec(`INSERT INTO tracks(id,medium_id,position,title,artist,source_root_id,relative_path,observed_at) VALUES($1,$2,1,'Track','Artist',$3,'a.flac',NOW())`, uuid.New(), medium, root)
	store := artwork.NewStore(t.TempDir())
	data := []byte{'G', 'I', 'F', '8', '9', 'a', 1, 0, 1, 0}
	newScan := func() (string, *ingest.ScanFeedback) {
		id := uuid.New()
		exec(`INSERT INTO scan_runs(id,status,started_at) VALUES($1,'running',NOW())`, id)
		return id, ingest.NewScanFeedback(id)
	}
	summarize := func(id string) ingest.ScanChangeSummary {
		t.Helper()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		summary, err := ingest.SummarizeScanChanges(ctx, tx, id)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		return summary
	}
	for _, step := range []struct {
		name   string
		remove bool
		want   int64
	}{{"add", false, 1}, {"same", false, 0}, {"remove", true, 1}, {"already absent", true, 0}} {
		t.Run(step.name, func(t *testing.T) {
			id, f := newScan()
			var err error
			if step.remove {
				err = store.RemoveWithFeedback(ctx, db, release, f)
			} else {
				err = store.SaveWithFeedback(ctx, db, release, data, "image/gif", "embedded", f)
			}
			if err != nil {
				t.Fatal(err)
			}
			if f.Broken() {
				t.Fatal(f.Err())
			}
			got := summarize(id)
			if got.Albums.Updated != step.want || got.Tracks != (ingest.ChangeCounts{}) {
				t.Fatalf("summary: %+v", got)
			}
		})
	}
	t.Run("record failure does not rollback artwork", func(t *testing.T) {
		exec(`CREATE FUNCTION reject_artwork_feedback() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'feedback fault'; END $$`)
		exec(`CREATE TRIGGER reject_artwork_feedback BEFORE INSERT ON scan_catalog_changes FOR EACH ROW EXECUTE FUNCTION reject_artwork_feedback()`)
		_, f := newScan()
		if err := store.SaveWithFeedback(ctx, db, release, data, "image/gif", "embedded", f); err != nil {
			t.Fatal(err)
		}
		if !f.Broken() {
			t.Fatal("feedback did not break")
		}
		var resourceID string
		if err := db.QueryRow(`SELECT storage_key FROM release_artworks WHERE release_id=$1`, release).Scan(&resourceID); err != nil {
			t.Fatal(err)
		}
		resource, err := store.Lookup(ctx, db, resourceID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.Read(resource); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("core failure cleans unreferenced file", func(t *testing.T) {
		_, f := newScan()
		different := append(append([]byte(nil), data...), byte(99))
		if err := store.SaveWithFeedback(ctx, db, uuid.New(), different, "image/gif", "embedded", f); err == nil {
			t.Fatal("missing release accepted")
		}
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM release_artworks`).Scan(&count); err != nil && err != sql.ErrNoRows {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("associations=%d", count)
		}
	})
}
