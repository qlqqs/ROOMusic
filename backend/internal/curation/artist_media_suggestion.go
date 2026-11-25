package curation

import (
	"context"
	"database/sql"
	"errors"

	"github.com/qlqq/roomusic/backend/internal/catalog/artwork"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

type ConfirmedArtistMedia struct {
	ItemID, ImageURL, Text, Lang string
	Image                        []byte
}

// 已采纳的图片与简介分别使用现有的人工层和操作类型；最后一条操作与建议结论一起提交。
func (w *Writer) AcceptArtistMedia(ctx context.Context, e ModelException, media ConfirmedArtistMedia, actor, key, requestID string) error {
	item, err := w.PendingItem(ctx, e.PendingItemID)
	if err != nil {
		return err
	}
	if !SuggestionRecorded(item) || item.ID != media.ItemID || item.Kind != "artist_identity" {
		return operations.Conflict("exception_stale", "艺人建议已变更")
	}
	var stored artwork.Stored
	if media.ImageURL != "" {
		stored, err = w.artworks.Describe(media.Image)
		if err != nil || min(stored.Width, stored.Height) < 200 || stored.MIMEType == "image/gif" {
			return operations.Invalid("invalid_input", "艺人图片格式或尺寸无效")
		}
	}
	var ids []string
	apply := func(k manualMediaKind, value manualMedia, opKey string, last bool) error {
		spec := operations.Spec{ActorID: actor, Key: opKey, RequestID: requestID, Type: k.Type(),
			Fingerprint: operations.Fingerprint([]string{e.ID, item.ID, k.field}), Origin: &operations.Origin{Source: "curation", ReviewDecision: "human", ReviewID: e.Review.ID}}
		if e.ConversationID != nil {
			spec.Origin.ConversationID = *e.ConversationID
		}
		var wrote bool
		result, err := operations.Run(ctx, w.db, spec, func(ctx context.Context, tx *sql.Tx, id string) (operations.Outcome, error) {
			var out operations.Outcome
			canonical, _, err := lockEffectiveArtist(ctx, tx, item.ArtistID)
			if err != nil {
				return out, err
			}
			if canonical != item.ArtistID {
				return out, operations.Conflict("exception_stale", "艺人已合并")
			}
			if err = checkWrite(ctx, tx, spec, actor, canonical, 0, 0); err != nil {
				return out, err
			}
			if err = CheckMatchItem(ctx, tx, item, "", true); err != nil {
				return out, err
			}
			current, err := k.displayed(ctx, tx, canonical)
			if err != nil {
				return out, err
			}
			if current.operationID != "" {
				return out, operations.Conflict("exception_stale", "艺人资料的缺口已变更")
			}
			if k.field == "image" {
				wrote, err = w.artworks.WriteIfChanged(ctx, tx, media.Image, stored)
				if err != nil {
					return out, err
				}
			}
			out, err = k.change(ctx, tx, id, canonical, canonical, &value, "manual")
			if last && err == nil {
				err = FinishException(ctx, tx, e.ID, actor, "accepted", append(ids, id))
			}
			return out, err
		})
		if err != nil {
			if wrote {
				w.discardArtistImage(ctx, stored.Key, item.ArtistID)
			}
			return err
		}
		ids = append(ids, result.OperationID)
		return nil
	}
	if media.ImageURL != "" {
		err = apply(manualImageKind(w), manualMedia{Resource: stored.Key, MIME: stored.MIMEType, Width: stored.Width, Height: stored.Height}, key, media.Text == "")
		if err != nil {
			return err
		}
	}
	if media.Text != "" {
		if len(ids) > 0 {
			key = DerivedKey(key, "bio")
		}
		err = apply(manualBioKind(w), manualMedia{Text: media.Text, Lang: media.Lang}, key, true)
		// 图片已提交后，简介可能在两步之间被填写；此时仅采纳实际写入的图片。
		var failure *operations.Failure
		if len(ids) > 0 && errors.As(err, &failure) && failure.Code == "exception_stale" {
			tx, beginErr := w.db.BeginTx(ctx, nil)
			if beginErr != nil {
				return beginErr
			}
			defer tx.Rollback()
			if finishErr := FinishException(ctx, tx, e.ID, actor, "accepted", ids); finishErr != nil {
				return finishErr
			}
			return tx.Commit()
		}
	}
	return err
}

// SuppressWebMedia 沿用撤销网页资料时的来源与值标记。
func SuppressWebMedia(ctx context.Context, tx *sql.Tx, artistID, subject, externalID, actor string) error {
	if subject == "artist_image" {
		externalID = sources.ImageURLID(externalID)
	}
	identity := externalIdentity{"web_search", externalID}
	return insertSuppression(ctx, tx, "artist", artistID, subject, identity.identityHash(), identity, "", actor)
}
