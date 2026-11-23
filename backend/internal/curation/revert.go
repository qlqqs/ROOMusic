package curation

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type Scope struct {
	Kind     string `json:"kind"`
	ID       string `json:"id,omitempty"`
	Provider string `json:"provider,omitempty"`
}

type SkippedRevert struct {
	OperationID string `json:"operation_id"`
	Code        string `json:"code"`
}
type RevertResult struct {
	Reverted   int             `json:"reverted"`
	Skipped    []SkippedRevert `json:"skipped"`
	NextCursor *string         `json:"next_cursor"`
}

type Reverter struct {
	db    *sql.DB
	kinds map[string]operations.Kind
}

func NewReverter(db *sql.DB, kinds ...operations.Kind) *Reverter {
	r := &Reverter{db: db, kinds: make(map[string]operations.Kind, len(kinds))}
	for _, kind := range kinds {
		r.kinds[kind.Type()] = kind
	}
	return r
}

func (r *Reverter) RevertScope(ctx context.Context, actorID string, scope Scope, cursor, key, requestID string) (RevertResult, error) {
	key, ok := operations.NormalizeKey(key)
	if !ok {
		return RevertResult{}, operations.Invalid("invalid_input", "请求需要 Idempotency-Key（UUID）")
	}
	switch {
	case scope.Kind == "batch" && uuid.Valid(scope.ID) && scope.Provider == "":
	case scope.Kind == "provider" && scope.ID == "" && providerPattern.MatchString(scope.Provider):
	default:
		return RevertResult{}, operations.Invalid("invalid_input", "撤销范围无效")
	}
	var before time.Time
	var beforeID string
	if cursor != "" {
		stamp, id, ok := strings.Cut(cursor, "/")
		parsed, err := time.Parse(time.RFC3339Nano, stamp)
		if !ok || !uuid.Valid(id) || err != nil {
			return RevertResult{}, operations.Invalid("invalid_input", "续页游标无效")
		}
		before, beforeID = parsed, id
	}
	const limit = 100
	items, err := operations.CurationRevertCandidates(ctx, r.db, scope.ID, scope.Provider, before, beforeID, limit+1)
	if err != nil {
		return RevertResult{}, err
	}
	result := RevertResult{Skipped: []SkippedRevert{}}
	if len(items) > limit {
		items = items[:limit]
		last := items[limit-1]
		next := last.CreatedAt.UTC().Format(time.RFC3339Nano) + "/" + last.ID
		result.NextCursor = &next
	}
	for _, op := range items {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		kind, ok := r.kinds[op.Type]
		if !ok {
			result.Skipped = append(result.Skipped, SkippedRevert{op.ID, "not_revertible"})
			continue
		}
		spec := operations.Spec{
			Type: operations.TypeRevert, ActorID: actorID, Key: DerivedKey(key, op.ID), RequestID: requestID,
			Fingerprint: operations.Fingerprint(map[string]any{"type": operations.TypeRevert, "operation_id": op.ID}),
		}
		run, err := operations.Run(ctx, r.db, spec, operations.RevertExecutor(kind, op.ID))
		if err != nil {
			_, code, _ := operations.Problem(err)
			result.Skipped = append(result.Skipped, SkippedRevert{op.ID, code})
			continue
		}
		if !run.Replayed {
			result.Reverted++
		}
	}
	return result, nil
}

// DerivedKey 以请求键为命名空间，按步骤名称派生 UUIDv5。
func DerivedKey(key, name string) string {
	namespace, _ := hex.DecodeString(strings.ReplaceAll(key, "-", ""))
	digest := sha1.Sum(append(namespace, name...))
	digest[6] = digest[6]&0x0f | 0x50
	digest[8] = digest[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", digest[:4], digest[4:6], digest[6:8], digest[8:10], digest[10:16])
}
