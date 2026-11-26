package app

import (
	"context"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/musicbrainz"
)

// 整理与管家共用已配置的客户端；这里投影整理所需的候选与发行组资料。
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

func (c curationMusicBrainz) GroupFirstReleaseDate(ctx context.Context, id string) (string, error) {
	group, err := c.Client.Lookup(ctx, "release-group", id, nil)
	return group.FirstReleaseDate, err
}

func (c curationMusicBrainz) ReleaseGroupDetails(ctx context.Context, id string) (curation.ReleaseGroupDetails, error) {
	group, err := c.Client.ReleaseGroupDetails(ctx, id, false)
	if err != nil {
		return curation.ReleaseGroupDetails{}, err
	}
	out := curation.ReleaseGroupDetails{ID: id, Title: group.Title, Complete: true}
	for _, edition := range group.Releases {
		out.Editions = append(out.Editions, sources.Release{ID: edition.ID, Title: edition.Title})
	}
	return out, nil
}
