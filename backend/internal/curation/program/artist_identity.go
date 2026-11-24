package program

import (
	"context"

	"github.com/qlqq/roomusic/backend/internal/curation"
)

type ArtistIdentityPlan struct {
	Changes []MatchChange
	preview curation.ArtistIdentityPreview
}

func (m *ModelMatcher) PlanArtistIdentity(ctx context.Context, item curation.PendingItem, source, externalID string) (ArtistIdentityPlan, error) {
	p, err := m.writer.PreviewArtistIdentity(ctx, m.sources, item, source, externalID)
	if err != nil {
		return ArtistIdentityPlan{}, err
	}
	return ArtistIdentityPlan{Changes: []MatchChange{{Field: "artist_link", After: p.Entry}}, preview: p}, nil
}

func (m *ModelMatcher) ApplyArtistIdentity(ctx context.Context, plan ArtistIdentityPlan, opt ApplyOptions) (curation.ArtistLinkApplied, error) {
	return m.writer.ApplyArtistIdentity(ctx, plan.preview, curation.ArtistLinkRequest{
		Source: opt.Source, Key: opt.Key, BatchID: opt.Batch, ConfirmedBy: opt.ConfirmedBy, ConversationID: opt.ConversationID,
	}, opt.Finish)
}
