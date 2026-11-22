package musicbrainz

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/sources"
)

func DiscID(toc sources.DiscTOC) (string, error) {
	n := len(toc.Offsets)
	if n < 1 || n > 99 || toc.Leadout <= toc.Offsets[n-1] || int64(toc.Leadout) > math.MaxUint32 {
		return "", sources.ErrInvalidQuery
	}
	h := sha1.New()
	fmt.Fprintf(h, "%02X%02X%08X", 1, n, toc.Leadout)
	last := -1
	for _, offset := range toc.Offsets {
		if offset <= last {
			return "", sources.ErrInvalidQuery
		}
		fmt.Fprintf(h, "%08X", offset)
		last = offset
	}
	for i := n; i < 99; i++ {
		fmt.Fprint(h, "00000000")
	}
	return base64.NewEncoding("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._").WithPadding('-').EncodeToString(h.Sum(nil)), nil
}

func (c *Client) ReleasesByDisc(ctx context.Context, toc sources.DiscTOC) (sources.DiscMatch, error) {
	id, err := DiscID(toc)
	if err != nil {
		return sources.DiscMatch{}, err
	}
	parts := []string{"1", strconv.Itoa(len(toc.Offsets)), strconv.Itoa(toc.Leadout)}
	for _, offset := range toc.Offsets {
		parts = append(parts, strconv.Itoa(offset))
	}
	params := url.Values{"toc": {strings.Join(parts, " ")}, "cdstubs": {"no"}, "inc": {"artist-credits labels release-groups"}}
	var result sources.DiscMatch
	err = c.get(ctx, "discid/"+id, params, func(body []byte) (bool, bool) {
		var disc struct {
			ID       string   `json:"id"`
			Count    *int     `json:"release-count"`
			Releases []Entity `json:"releases"`
		}
		if json.Unmarshal(body, &disc) != nil || (disc.ID == "" && disc.Count == nil) || !validEntities(disc.Releases) {
			return false, false
		}
		result.Exact = disc.ID != ""
		if !result.Exact && len(disc.Releases) > 25 {
			disc.Releases = disc.Releases[:25]
		}
		result.Releases = releases(disc.Releases)
		return len(result.Releases) == 0, true
	})
	if errors.Is(err, sources.ErrNotFound) {
		return sources.DiscMatch{Releases: []sources.Release{}}, nil
	}
	return result, err
}
