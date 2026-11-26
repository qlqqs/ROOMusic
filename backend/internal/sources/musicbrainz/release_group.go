package musicbrainz

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strconv"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

// ReleaseGroupDetails 先用关联列表。达到关联版本上限或需要补曲数时，按组分页读取带介质的发行列表。
func (c *Client) ReleaseGroupDetails(ctx context.Context, id string, withMedia bool, include ...string) (Entity, error) {
	if !slices.Contains(include, "releases") {
		include = append(slices.Clone(include), "releases")
	}
	group, err := c.Lookup(ctx, "release-group", id, include)
	if err != nil {
		return Entity{}, err
	}
	if group.ID != id || group.Releases == nil {
		return Entity{}, sources.ErrUnavailable
	}
	if len(group.Releases) >= 25 || withMedia && slices.ContainsFunc(group.Releases, func(r Entity) bool { return r.TrackCount == nil && len(r.Media) == 0 }) {
		var editions []Entity
		for {
			params := url.Values{"release-group": {id}, "limit": {"100"}, "offset": {strconv.Itoa(len(editions))}, "inc": {"media"}}
			var page struct {
				Count    *int     `json:"release-count"`
				Offset   *int     `json:"release-offset"`
				Releases []Entity `json:"releases"`
			}
			err := c.get(ctx, "release", params, func(body []byte) (bool, bool) {
				if json.Unmarshal(body, &page) != nil || page.Count == nil || page.Offset == nil || *page.Offset != len(editions) || len(page.Releases) == 0 || len(page.Releases) > 100 {
					return false, false
				}
				return false, len(editions)+len(page.Releases) <= *page.Count
			})
			if err != nil {
				return Entity{}, err
			}
			editions = append(editions, page.Releases...)
			if len(editions) == *page.Count {
				break
			}
		}
		group.Releases = editions
	}
	seen := make(map[string]bool, len(group.Releases))
	for _, edition := range group.Releases {
		if !uuid.Valid(edition.ID) || seen[edition.ID] {
			return Entity{}, sources.ErrUnavailable
		}
		seen[edition.ID] = true
	}
	return group, nil
}
