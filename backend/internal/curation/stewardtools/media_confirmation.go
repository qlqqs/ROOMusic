package stewardtools

import (
	"context"
	"encoding/json"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

// MediaConfirmationPreparer 为人工采纳复用后台提案的出站与资料校验。
func MediaConfirmationPreparer(host sources.MockHost) func(context.Context, json.RawMessage) (curation.ConfirmedArtistMedia, error) {
	a := adapter{mockHost: host}
	return a.prepareMediaConfirmation
}

func (a adapter) prepareMediaConfirmation(ctx context.Context, raw json.RawMessage) (curation.ConfirmedArtistMedia, error) {
	var in mediaInput
	var media curation.ConfirmedArtistMedia
	if err := json.Unmarshal(raw, &in); err != nil {
		return media, err
	}
	if err := in.validate(); err != nil {
		return media, err
	}
	if in.Image != nil {
		if err := a.checkImagePage(ctx, in.Image.PageURL, in.Image.URL); err != nil {
			return media, err
		}
	}
	data, err := a.prepareMedia(ctx, in, in.Image != nil)
	if err != nil {
		return media, err
	}
	media.ItemID = in.ItemID
	if in.Image != nil {
		media.ImageURL, media.Image = in.Image.URL, data
	}
	if in.Bio != nil {
		media.Text, media.Lang = in.Bio.Text, in.Bio.Lang
	}
	return media, nil
}
