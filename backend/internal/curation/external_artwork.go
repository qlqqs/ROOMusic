package curation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type ArtworkRequest struct {
	ReleaseID               string
	Data                    []byte
	Source                  Source
	ExternalID              string
	BatchID, Key, RequestID string
}

type ExternalApplied struct {
	OperationID string `json:"operation_id"`
	Skipped     string `json:"skipped,omitempty"`
}

var externalProviderPattern = regexp.MustCompile(`^([a-z0-9._-]{1,64}|plugin:[a-z0-9-]{3,40})$`)

func (w *Writer) externalSpec(ctx context.Context, kind, targetField, targetID, hash string, source Source, externalID, batchID, key, requestID string) (operations.Spec, error) {
	if !validExternalID(externalID) {
		return operations.Spec{}, operations.Invalid("invalid_input", "外部编号无效")
	}
	fingerprint := operations.Fingerprint(map[string]any{"type": kind, targetField: targetID, "content_hash": hash, "provider": source.Provider, "external_id": externalID})
	return w.sourceSpec(ctx, kind, targetID, fingerprint, source, batchID, key, requestID, "", "")
}

func validExternalID(id string) bool {
	return len(id) > 0 && len(id) <= 128 && utf8.ValidString(id) && !strings.ContainsRune(id, 0)
}

func (w *Writer) sourceSpec(ctx context.Context, kind, targetID, fingerprint string, source Source, batchID, key, requestID, confirmedBy, conversation string) (operations.Spec, error) {
	key, ok := operations.NormalizeKey(key)
	if !ok || !uuid.Valid(targetID) || (batchID != "" && !uuid.Valid(batchID)) {
		return operations.Spec{}, operations.Invalid("invalid_input", "目标、批次或幂等键无效")
	}
	if (source.Kind != "source" && source.Kind != "model") || !externalProviderPattern.MatchString(source.Provider) || len(source.Basis) > 500 || !utf8.ValidString(source.Basis) || strings.ContainsRune(source.Basis, 0) {
		return operations.Spec{}, operations.Invalid("invalid_input", "来源或依据无效")
	}
	actor, err := w.writerActor(ctx, batchID, confirmedBy, conversation)
	if err != nil {
		return operations.Spec{}, err
	}
	return operations.Spec{Type: kind, ActorID: actor, Key: key, BatchID: batchID, RequestID: requestID,
		Origin:      &operations.Origin{ConversationID: conversation, Source: "curation", ProviderKind: source.Kind, Provider: source.Provider, Basis: source.Basis},
		Fingerprint: fingerprint}, nil
}

func checkExternalWriter(ctx context.Context, tx *sql.Tx, spec operations.Spec) error {
	actor, err := activeEnabler(ctx, tx)
	if err != nil {
		return err
	}
	if actor != spec.ActorID {
		return operations.Conflict("curation_disabled", "自动整理开启者已变更，请重试")
	}
	if spec.BatchID != "" {
		return requireOpenBatch(ctx, tx, spec.BatchID)
	}
	return nil
}

func (a *ExternalApplied) skip(reason string) (operations.Outcome, error) {
	a.Skipped = reason
	return operations.Outcome{}, operations.Conflict("nothing_to_apply", "没有需要补充的内容")
}

func externalOutcome(ctx context.Context, release *governance.LockedAutoRelease, before, after any) (operations.Outcome, error) {
	expected, result, err := release.Bump(ctx)
	if err != nil {
		return operations.Outcome{}, err
	}
	return operations.Outcome{TargetType: operations.TargetRelease, TargetID: release.ID(), ExpectedRevision: expected, ResultRevision: result, Before: before, After: after}, nil
}

type artworkValue struct {
	externalIdentity
	Key    string `json:"storage_key"`
	Hash   string `json:"content_hash"`
	MIME   string `json:"mime_type"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

func (w *Writer) ApplyArtwork(ctx context.Context, request ArtworkRequest) (ExternalApplied, error) {
	stored, err := w.artworks.Describe(request.Data)
	if err != nil {
		return ExternalApplied{}, operations.Invalid("invalid_input", "封面图片无效")
	}
	id := strings.ToLower(request.ReleaseID)
	spec, err := w.externalSpec(ctx, operations.TypeReleaseExternalArtwork, "release_id", id, stored.Hash, request.Source, request.ExternalID, request.BatchID, request.Key, request.RequestID)
	if err != nil {
		return ExternalApplied{}, err
	}
	identity := externalIdentity{request.Source.Provider, request.ExternalID}
	marker := externalSuppression{"release", id, "artwork", stored.Hash, identity}
	var applied ExternalApplied
	wrote := false
	result, err := operations.Run(ctx, w.db, spec, func(ctx context.Context, tx *sql.Tx, operationID string) (operations.Outcome, error) {
		release, err := governance.LockAutoRelease(ctx, tx, id)
		if err != nil {
			return operations.Outcome{}, err
		}
		if err := checkExternalWriter(ctx, tx, spec); err != nil {
			return operations.Outcome{}, err
		}
		var local bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM release_artworks WHERE release_id=$1::uuid)`, id).Scan(&local); err != nil {
			return operations.Outcome{}, err
		}
		if local {
			return applied.skip("local_present")
		}
		var oldKey, oldHash, oldSource, oldExternalID, oldOwner string
		var prior any
		err = tx.QueryRowContext(ctx, `SELECT storage_key,content_hash,source,external_id,operation_id::text FROM release_external_artworks WHERE release_id=$1::uuid`, id).
			Scan(&oldKey, &oldHash, &oldSource, &oldExternalID, &oldOwner)
		switch {
		case err == nil:
			if w.artworks.Exists(oldKey) {
				return applied.skip("occupied")
			}
			prior = map[string]string{"storage_key": oldKey, "content_hash": oldHash, "source": oldSource, "external_id": oldExternalID, "operation_id": oldOwner}
		case !errors.Is(err, sql.ErrNoRows):
			return operations.Outcome{}, err
		}
		suppressed, err := marker.exists(ctx, tx)
		if err != nil {
			return operations.Outcome{}, err
		}
		if suppressed {
			return applied.skip("suppressed")
		}
		wrote, err = w.artworks.WriteIfChanged(ctx, tx, request.Data, stored)
		if err != nil {
			return operations.Outcome{}, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO release_external_artworks(release_id,storage_key,content_hash,mime_type,width,height,source,external_id,operation_id)
   VALUES($1::uuid,$2,$3,$4,$5,$6,$7,$8,$9::uuid)
   ON CONFLICT(release_id) DO UPDATE SET storage_key=EXCLUDED.storage_key,content_hash=EXCLUDED.content_hash,
   mime_type=EXCLUDED.mime_type,width=EXCLUDED.width,height=EXCLUDED.height,source=EXCLUDED.source,
   external_id=EXCLUDED.external_id,operation_id=EXCLUDED.operation_id,created_at=now()`, id, stored.Key, stored.Hash, stored.MIMEType, stored.Width, stored.Height, identity.Source, identity.ExternalID, operationID)
		if err != nil {
			return operations.Outcome{}, err
		}
		next := artworkValue{identity, stored.Key, stored.Hash, stored.MIMEType, stored.Width, stored.Height}
		out, err := externalOutcome(ctx, release, map[string]any{"artwork": prior}, map[string]any{"artwork": next})
		out.Response = ExternalApplied{OperationID: operationID}
		return out, err
	})
	if err != nil {
		if wrote {
			w.discardArtwork(ctx, stored.Key, id)
		}
		return applied, err
	}
	return ExternalApplied{OperationID: result.OperationID}, nil
}

func (w *Writer) discardArtwork(ctx context.Context, key, releaseID string) {
	ctx = context.WithoutCancel(ctx)
	if err := w.artworks.Discard(ctx, w.db, key); err != nil {
		w.logger.WarnContext(ctx, "未能清理自动整理的封面文件", "event", "curation.artwork.discard_failed", "module", "curation", "release_id", releaseID)
	}
}

// 两种补充操作都按外部行的写入者判断归属，撤销状态记录实际删除的内容。
type externalKind struct {
	writer                *Writer
	kind, table, idColumn string
	decode                func(operations.Record, json.RawMessage) (externalChange, error)
}

type externalChange struct {
	marker                     externalSuppression
	value, changes, emptyState any
	preview                    map[string]string
	storageKey                 string
}

func ExternalArtworkOperations(w *Writer) operations.Kind {
	return externalKind{w, operations.TypeReleaseExternalArtwork, "release_external_artworks", "release_id", decodeArtworkChange}
}

func (k externalKind) Type() string { return k.kind }

func decodeArtworkChange(record operations.Record, raw json.RawMessage) (externalChange, error) {
	var state struct{ Artwork artworkValue }
	if err := json.Unmarshal(raw, &state); err != nil {
		return externalChange{}, err
	}
	a := state.Artwork
	if a.Key == "" || a.Hash == "" || a.Source == "" || a.ExternalID == "" || a.MIME == "" || a.Width <= 0 || a.Height <= 0 {
		return externalChange{}, errors.New("封面操作记录缺少图片信息")
	}
	return externalChange{
		marker: externalSuppression{"release", record.TargetID, "artwork", a.Hash, a.externalIdentity},
		value:  map[string]any{"source": a.Source, "external_id": a.ExternalID, "mime_type": a.MIME, "width": a.Width, "height": a.Height},
		changes: map[string]any{"kind": operations.TypeReleaseExternalArtwork, "source": a.Source, "external_id": a.ExternalID,
			"artwork": map[string]any{"resource_id": a.Key, "mime": a.MIME, "width": a.Width, "height": a.Height}},
		emptyState: map[string]any{"artwork": nil}, preview: map[string]string{"source": a.Source, "external_id": a.ExternalID}, storageKey: a.Key}, nil
}

func (k externalKind) Revert(ctx context.Context, tx *sql.Tx, operationID string, original operations.Record) (operations.Outcome, error) {
	release, err := governance.LockAutoReleaseForRevert(ctx, tx, original.TargetID)
	if err != nil {
		var failure *operations.Failure
		if errors.As(err, &failure) && failure.Class == operations.ClassNotFound {
			return operations.Outcome{}, operations.Conflict("revert_conflict", "补充内容所属的发行已移除")
		}
		return operations.Outcome{}, err
	}
	change, err := k.decode(original, original.After)
	if err != nil {
		return operations.Outcome{}, err
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM "+k.table+" WHERE "+k.idColumn+"=$1::uuid AND operation_id=$2::uuid", change.marker.targetID, original.ID)
	if err != nil {
		return operations.Outcome{}, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return operations.Outcome{}, err
	}
	if n == 0 {
		return operations.Outcome{}, operations.Conflict("revert_conflict", "补充内容已移除或已被其他操作替换")
	}
	if err := change.marker.insert(ctx, tx, change.value, operationID); err != nil {
		return operations.Outcome{}, err
	}
	out, err := externalOutcome(ctx, release, original.After, change.emptyState)
	if err != nil {
		return operations.Outcome{}, err
	}
	out.ResponseRevision = release.EditRevision()
	if change.storageKey != "" {
		out.AfterCommit = func() { k.writer.discardArtwork(ctx, change.storageKey, original.TargetID) }
	}
	return out, nil
}

func (k externalKind) Describe(ctx context.Context, q operations.Queryer, records []operations.Record) (map[string]operations.Description, error) {
	releaseIDs := make([]string, 0, len(records))
	for _, record := range records {
		releaseIDs = append(releaseIDs, record.TargetID)
	}
	infos, err := governance.LoadOperationReleases(ctx, q, releaseIDs)
	if err != nil {
		return nil, err
	}
	descriptions := make(map[string]operations.Description, len(records))
	owners := make(map[string]string, len(records))
	targets := make([]string, 0, len(records))
	for _, record := range records {
		var d operations.Description
		if info, ok := infos[record.TargetID]; ok {
			d.Release = &info
		}
		raw := record.After
		if record.Type == operations.TypeRevert {
			raw = record.Before
		}
		if change, err := k.decode(record, raw); err == nil {
			d.Changes = change.changes
			if record.Type != operations.TypeRevert && record.RevertedByID == "" {
				targets = append(targets, change.marker.targetID)
				owners[record.ID] = change.marker.targetID
			}
		}
		descriptions[record.ID] = d
	}
	if len(targets) == 0 {
		return descriptions, nil
	}
	rows, err := q.QueryContext(ctx, "SELECT "+k.idColumn+"::text,operation_id::text FROM "+k.table+" WHERE "+k.idColumn+"=ANY($1::uuid[])", targets)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var targetID, owner string
		if err := rows.Scan(&targetID, &owner); err != nil {
			return nil, err
		}
		if owners[owner] == targetID {
			d := descriptions[owner]
			d.Revertible = true
			descriptions[owner] = d
		}
	}
	return descriptions, rows.Err()
}

func (k externalKind) PreviewRevert(ctx context.Context, q operations.Queryer, record operations.Record) ([]operations.PreviewChange, error) {
	change, err := k.decode(record, record.After)
	if err != nil {
		return nil, err
	}
	return []operations.PreviewChange{{Field: change.marker.subject, Before: change.preview, After: nil}}, nil
}
