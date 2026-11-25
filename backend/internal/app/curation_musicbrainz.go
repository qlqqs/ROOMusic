package app

import (
	"context"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/sources/musicbrainz"
)

// 整理与管家共用已配置的客户端；这里只将搜索页投影到整理所需的证据。
type curationMusicBrainz struct{ *musicbrainz.Client }

func (c curationMusicBrainz) SearchReleasePage(ctx context.Context, q curation.ReleaseSearch) (curation.ReleasePage, error) {
	page, err := c.Client.SearchReleasePage(ctx, musicbrainz.ReleaseSearch(q))
	if err != nil {
		return curation.ReleasePage{}, err
	}
	out := curation.ReleasePage{PageInfo: curation.PageInfo(page.PageInfo), Items: page.Items, Counts: make(map[string]curation.ReleaseCounts, len(page.Counts))}
	for id, counts := range page.Counts {
		out.Counts[id] = curation.ReleaseCounts(counts)
	}
	return out, nil
}

func (c curationMusicBrainz) SearchRecordingPage(ctx context.Context, q curation.RecordingSearch) (curation.RecordingPage, error) {
	page, err := c.Client.SearchRecordingPage(ctx, musicbrainz.RecordingSearch(q))
	return curation.RecordingPage{PageInfo: curation.PageInfo(page.PageInfo), Items: page.Items, ReleaseCounts: page.ReleaseCounts}, err
}

func (c curationMusicBrainz) SearchArtistPage(ctx context.Context, name string) (curation.ArtistPage, error) {
	page, err := c.Client.SearchArtistPage(ctx, name)
	return curation.ArtistPage{PageInfo: curation.PageInfo(page.PageInfo), Items: page.Items}, err
}
