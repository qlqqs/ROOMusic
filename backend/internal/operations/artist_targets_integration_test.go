package operations

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPostgreSQLArtistTargetsAtomicAndDeduplicated(t *testing.T) {
	f := newRunFixture(t)
	artist, other := uuid.New(), uuid.New()
	f.exec(`INSERT INTO artists(id,initial_name) VALUES($1::uuid,'甲'),($2::uuid,'乙')`, artist, other)
	execute := func(extra string) Executor {
		return func(ctx context.Context, tx *sql.Tx, operationID string) (Outcome, error) {
			if _, err := tx.ExecContext(ctx, `UPDATE artists SET edit_revision=edit_revision+1 WHERE id=$1::uuid`, artist); err != nil {
				return Outcome{}, err
			}
			return Outcome{TargetType: TargetArtist, TargetID: artist, Targets: []Target{{TargetArtist, strings.ToUpper(artist)}, {TargetArtist, other}, {TargetArtist, other}, {TargetArtist, extra}}, ExpectedRevision: 1, ResultRevision: 2, Before: map[string]any{}, After: map[string]any{}, Response: map[string]string{"operation_id": operationID}}, nil
		}
	}
	spec := f.spec(f.actorID, uuid.New(), TypeArtistMerge, map[string]any{"artist": artist})
	result, err := Run(context.Background(), f.database, spec, execute(other))
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := f.database.QueryRow(`SELECT count(*) FROM operations_artist_targets WHERE operation_id=$1::uuid`, result.OperationID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("targets=%d %v", count, err)
	}
	for _, id := range []string{artist, other} {
		page, err := List(context.Background(), f.database, ListParams{Targets: []Target{{TargetArtist, id}}, Page: 1, PageSize: 50})
		if err != nil || len(page.Items) != 1 || page.Items[0].ID != result.OperationID {
			t.Fatalf("target %s omitted: %+v %v", id, page, err)
		}
	}
	// 不存在的额外目标使 FK 失败，不能留下业务变化或操作记录。
	failedSpec := f.spec(f.actorID, uuid.New(), TypeArtistMerge, map[string]any{"artist": artist, "failed": true})
	if _, err := Run(context.Background(), f.database, failedSpec, execute(uuid.New())); err == nil {
		t.Fatal("unknown target accepted")
	}
	var revision int64
	if err := f.database.QueryRow(`SELECT edit_revision FROM artists WHERE id=$1::uuid`, artist).Scan(&revision); err != nil || revision != 2 || f.recordCount() != 1 {
		t.Fatalf("partial commit revision=%d error=%v", revision, err)
	}
}
