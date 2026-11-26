package program

import (
	"context"

	"github.com/qlqq/roomusic/backend/internal/sources"
)

func (w *Worker) PreparationContext(ctx context.Context) context.Context {
	return w.policy.Context(ctx, sources.QueueModelLookup, nil)
}
