package curation

import (
	"context"
	"database/sql"
	"errors"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/operations"
)

type displayedMedia struct {
	artistID, operationID string
	manual                bool
}

func (k manualMediaKind) displayed(ctx context.Context, q operations.Queryer, canonical string) (displayedMedia, error) {
	media := catalog.ArtistImageRowsSQL
	if k.field == "bio" {
		media = catalog.ArtistBioRowsSQL
	}
	var item displayedMedia
	err := q.QueryRowContext(ctx, `WITH `+catalog.ArtistMediaFamilyCTE+`
        SELECT m.artist_id::text,m.operation_id::text,m.source='manual'
        FROM artist_media_family f JOIN (`+media+`) m ON m.artist_id=f.artist_id
        WHERE f.canonical_id=$1 ORDER BY (m.source='manual') DESC,`+catalog.ArtistMediaOrder+` LIMIT 1`, canonical).
		Scan(&item.artistID, &item.operationID, &item.manual)
	if errors.Is(err, sql.ErrNoRows) {
		return displayedMedia{}, nil
	}
	return item, err
}

func (w *Writer) RejectArtistMedia(ctx context.Context, r ArtistEditRequest, field string) (operations.Result, error) {
	manual, auto := manualImageKind(w), artistImageKind(w)
	switch field {
	case "bio":
		manual, auto = manualBioKind(w), artistBioKind(w)
	case "image":
	default:
		return operations.Result{}, operations.Invalid("invalid_input", "艺人资料字段无效")
	}
	canonical, readErr := effectiveArtist(ctx, w.db, r.ArtistID)
	var observed displayedMedia
	if readErr == nil {
		observed, readErr = manual.displayed(ctx, w.db, canonical)
	}
	opType := manual.Type()
	if !observed.manual && observed.operationID != "" {
		opType = operations.TypeRevert
	}
	// 指纹绑定客户端看到的版本与字段。重放时该资料可能已清除或露出下一层。
	spec := r.spec(opType, "artist_media_reject", field)
	return operations.Run(ctx, w.db, spec, func(ctx context.Context, tx *sql.Tx, operationID string) (operations.Outcome, error) {
		if readErr != nil {
			return operations.Outcome{}, readErr
		}
		var original operations.Record
		if opType == operations.TypeRevert {
			var err error
			original, err = operations.LoadForUpdate(ctx, tx, observed.operationID)
			if errors.Is(err, operations.ErrNotFound) || err == nil && (original.Type != auto.Type() || original.TargetID != observed.artistID || original.RevertedByID != "") {
				return operations.Outcome{}, operations.Conflict("revert_conflict", "自动资料已变化")
			}
			if err != nil {
				return operations.Outcome{}, err
			}
		}
		id, revision, err := lockEffectiveArtist(ctx, tx, r.ArtistID)
		if err != nil {
			return operations.Outcome{}, err
		}
		current, err := manual.displayed(ctx, tx, id)
		if err != nil {
			return operations.Outcome{}, err
		}
		if canonical != id || current != observed {
			return operations.Outcome{}, operations.Conflict("revert_conflict", "当前显示的艺人资料已变化")
		}
		if err := checkArtistRevision(revision, r.ExpectedRevision); err != nil {
			return operations.Outcome{}, err
		}
		if current.operationID == "" {
			return operations.Outcome{}, operations.Conflict("nothing_to_apply", "艺人没有这项资料")
		}
		if current.manual {
			return manual.change(ctx, tx, operationID, id, current.artistID, nil, "manual")
		}
		out, err := auto.Revert(ctx, tx, operationID, original)
		if err != nil {
			return operations.Outcome{}, err
		}
		edit, err := artistEditOutcome(ctx, tx, id, out.Before, out.After)
		if err != nil {
			return operations.Outcome{}, err
		}
		edit.RevertsID = original.ID
		edit.AfterCommit = out.AfterCommit
		edit.Response = artistEditResponse{operationID, id, edit.ResultRevision, "auto"}
		return edit, nil
	})
}
