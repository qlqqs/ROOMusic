package program_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/curation/model"
	"github.com/qlqq/roomusic/backend/internal/curation/program"
)

func TestPostgreSQLCurationShelvedReopensOnNewCandidates(t *testing.T) {
	program.RunShelvedPendingCases(t, func(ctx context.Context, db *sql.DB, writer *curation.Writer, matcher *program.ModelMatcher, exceptionID, actorID string) (curation.ModelException, error) {
		return model.NewExceptionResolver(db, writer, matcher).Decline(ctx, exceptionID, actorID)
	})
}
