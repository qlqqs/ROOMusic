package curation

import (
	"context"
	"database/sql"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

// ArtistIdentityPreview 将读到的外部条目与准备写入的有效艺人和本地版本绑定。
type ArtistIdentityPreview struct {
	Entry    ArtistLinkPreview
	artistID string
	revision int64
	item     PendingItem
}

func isArtistEntity(entry ArtistLinkPreview) bool {
	if entry.Source == "musicbrainz" {
		switch strings.ToLower(entry.artistType) {
		case "other", "character":
			return false
		default:
			return true
		}
	}
	for _, class := range entry.InstanceOf {
		switch class {
		// 人、音乐团体及常见具体类型（乐队、乐团、合唱团等）。
		case "Q5", "Q215380", "Q2088357", "Q9212979", "Q42998", "Q131186", "Q5741069", "Q18388277":
			return true
		}
	}
	return false
}

func (w *Writer) PreviewArtistIdentity(ctx context.Context, gate *Sources, item PendingItem, source, externalID string) (ArtistIdentityPreview, error) {
	plan := ArtistIdentityPreview{item: item}
	if item.Kind != "artist_identity" || item.TargetType != "artist" {
		return plan, operations.Invalid("invalid_input", "需要艺人身份待定项")
	}
	switch {
	case source == "musicbrainz" && uuid.Valid(externalID):
		externalID = strings.ToLower(externalID)
	case item.ArtistWebGeneration == nil && source == "wikidata" && sources.ValidWikidataID(externalID):
	default:
		return plan, operations.Invalid("invalid_input", "外部艺人编号无效")
	}
	id, err := effectiveArtist(ctx, w.db, item.ArtistID)
	if err != nil {
		return plan, err
	}
	plan.artistID = id
	var suppressed, occupied bool
	err = w.db.QueryRowContext(ctx, `WITH `+catalog.ArtistMediaFamilyCTE+`, family AS (
		SELECT artist_id FROM artist_media_family WHERE canonical_id=$1
	) SELECT a.edit_revision,
		EXISTS (SELECT FROM family f JOIN curation_suppressions s ON s.target_id=f.artist_id
			WHERE s.target_type='artist' AND s.subject='external_link' AND s.value_hash=$2),
		EXISTS (SELECT FROM family f JOIN external_links l ON l.object_id=f.artist_id
			WHERE l.object_type='artist' AND l.source IN ('musicbrainz','wikidata') AND ($3 OR l.source='musicbrainz'))
	FROM artists a WHERE a.id=$1`, id, (externalIdentity{source, externalID}).identityHash(), item.ArtistWebGeneration == nil).Scan(&plan.revision, &suppressed, &occupied)
	if err != nil {
		return plan, err
	}
	if suppressed {
		return plan, operations.Conflict("suppressed", "此艺人对应已标记不再提出")
	}
	if occupied {
		return plan, operations.Conflict("occupied", "艺人已有外部对应")
	}
	plan.Entry, err = w.PreviewArtistLink(ctx, gate, id, externalID)
	if err != nil {
		return plan, err
	}
	if !strings.EqualFold(plan.Entry.entryID, externalID) {
		return plan, operations.Conflict("identity_mismatch", "资料源返回的条目编号与候选不符")
	}
	if !isArtistEntity(plan.Entry) {
		return plan, operations.Invalid("not_an_artist", "条目类型不是个人或音乐团体")
	}
	if item.ArtistWebGeneration != nil {
		plan.Entry.WikidataID = nil
	}
	if item.ArtistWebGeneration == nil && !plan.Entry.NameMatch {
		return plan, operations.Invalid("identity_mismatch", "条目名字与本地艺人或别名不符")
	}
	return plan, nil
}

func (w *Writer) ApplyArtistIdentity(ctx context.Context, plan ArtistIdentityPreview, r ArtistLinkRequest, finish func(context.Context, *sql.Tx, []string) error) (ArtistLinkApplied, error) {
	r.ArtistID, r.ExpectedRevision = plan.artistID, plan.revision
	r.ExternalSource, r.ExternalID = plan.Entry.Source, plan.Entry.ExternalID
	spec, err := w.artistLinkSpec(ctx, r)
	if err != nil {
		return ArtistLinkApplied{}, err
	}
	if run, ok := ModelRunFrom(ctx); ok && r.ConfirmedBy == "" && run.ToolInputDigest != "" {
		spec.Fingerprint = run.ArtistToolFingerprint(spec.Type)
	}
	return w.applyArtistLink(ctx, r, spec, plan.Entry, &plan.item, finish)
}
