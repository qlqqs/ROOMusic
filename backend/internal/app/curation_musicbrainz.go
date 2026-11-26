package app

import (
	"context"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
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

// MusicBrainz 的关联实体读取至多包含 25 个发行；满页不能证明某一版唯一。
func (c curationMusicBrainz) ReleaseGroupEditions(ctx context.Context, id string) ([]string, bool, error) {
	group, err := c.Lookup(ctx, "release-group", id, []string{"releases"})
	if err != nil {
		return nil, false, err
	}
	if group.ID != id || len(group.Releases) == 0 {
		return nil, false, sources.ErrUnavailable
	}
	ids := make([]string, 0, len(group.Releases))
	for _, release := range group.Releases {
		if !uuid.Valid(release.ID) {
			return nil, false, sources.ErrUnavailable
		}
		ids = append(ids, release.ID)
	}
	return ids, len(ids) < 25, nil
}
