package main

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strconv"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/musicbrainz"
)

type edition struct {
	id      string
	lengths []int64
}

type pairCounts struct {
	EqualCount int `json:"same_track_count_pairs"`
	Comparable int `json:"comparable_pairs"`
	Current    int `json:"pass_current"`
	Four       int `json:"pass_four_seconds"`
	Two        int `json:"pass_two_seconds"`
}

type toleranceRow struct {
	Group    string `json:"release_group,omitempty"`
	Title    string `json:"title,omitempty"`
	Category string `json:"category,omitempty"`
	Editions int    `json:"editions"`
	pairCounts
	Ratios map[string]*float64 `json:"pass_fraction"`
}

func (row *toleranceRow) fractions() {
	row.Ratios = make(map[string]*float64, 3)
	for name, n := range map[string]int{"current": row.Current, "four_seconds": row.Four, "two_seconds": row.Two} {
		if row.Comparable == 0 {
			row.Ratios[name] = nil
			continue
		}
		value := float64(n) / float64(row.Comparable)
		row.Ratios[name] = &value
	}
}

func measureTolerance(ctx context.Context, corpus []corpusEntry, groupIDs []string) error {
	var groups []toleranceRow
	if len(groupIDs) == 0 {
		for _, c := range corpus {
			if c.Tolerance {
				groups = append(groups, toleranceRow{Group: c.Group, Title: c.Title, Category: c.Category})
			}
		}
	} else {
		seen := make(map[string]bool)
		for _, id := range groupIDs {
			if !uuid.Valid(id) || seen[id] {
				return fmt.Errorf("发行组编号无效或重复：%s", id)
			}
			seen[id] = true
			groups = append(groups, toleranceRow{Group: id})
		}
	}

	client := musicbrainz.New(musicbrainz.Options{})
	// 发行组浏览使用与正式客户端相同的请求处理和同一个限速器。
	requests := sources.NewRequester(musicbrainz.Declaration(), sources.Options{Limiter: client.Limiter()})
	total := toleranceRow{}
	for i := range groups {
		row := &groups[i]
		editions, err := browseEditions(ctx, requests, client.BaseURL(), row.Group)
		if err != nil {
			return fmt.Errorf("发行组 %s：%w", row.Group, err)
		}
		row.Editions = len(editions)
		for i, a := range editions {
			for _, b := range editions[i+1:] {
				row.compare(a.lengths, b.lengths)
			}
		}
		row.fractions()
		total.Editions += row.Editions
		total.EqualCount += row.EqualCount
		total.Comparable += row.Comparable
		total.Current += row.Current
		total.Four += row.Four
		total.Two += row.Two
	}
	total.fractions()
	return writeResult(map[string]any{"groups": groups, "total": total,
		"method": "同组同曲目数，每对按发行编号字典序以第一版模拟本地、时长四舍五入至秒。至少一半曲目双方时长已知才能计入分母。全部可比曲目通过才算通过。"})
}

func browseEditions(ctx context.Context, requests *sources.Requester, base, group string) ([]edition, error) {
	var result []edition
	seen := make(map[string]bool)
	for offset := 0; ; {
		params := url.Values{"release-group": {group}, "inc": {"recordings"}, "fmt": {"json"}, "limit": {"100"}, "offset": {strconv.Itoa(offset)}}
		var page struct {
			Count    *int                 `json:"release-count"`
			Offset   *int                 `json:"release-offset"`
			Releases []musicbrainz.Entity `json:"releases"`
		}
		err := requests.GetJSON(ctx, base+"/release?"+params.Encode(), func(body []byte) (bool, bool) {
			valid := json.Unmarshal(body, &page) == nil && page.Count != nil && page.Offset != nil && *page.Offset == offset && page.Releases != nil
			return len(page.Releases) == 0, valid
		})
		if err != nil {
			return nil, fmt.Errorf("第 %d 项起的浏览请求：%w", offset, err)
		}
		for _, release := range page.Releases {
			if !uuid.Valid(release.ID) || seen[release.ID] {
				return nil, fmt.Errorf("第 %d 项起的发行编号缺失或重复：%q", offset, release.ID)
			}
			seen[release.ID] = true
			e := edition{id: release.ID}
			for _, m := range release.Media {
				if len(m.Tracks) != m.TrackCount {
					return nil, fmt.Errorf("发行 %s 第 %d 碟曲目不全", release.ID, m.Position)
				}
				for _, t := range m.Tracks {
					var ms int64
					if t.Length != nil {
						ms = *t.Length
					}
					e.lengths = append(e.lengths, ms)
				}
			}
			result = append(result, e)
		}
		// MusicBrainz 也按整页曲目数截断，返回不足一百项时仍可能有下一页。
		offset += len(page.Releases)
		if offset >= *page.Count {
			break
		}
		if len(page.Releases) == 0 {
			return nil, fmt.Errorf("第 %d 项起返回空页，但还有 %d 个发行", offset, *page.Count-offset)
		}
	}
	slices.SortFunc(result, func(a, b edition) int { return cmp.Compare(a.id, b.id) })
	return result, nil
}

func (c *pairCounts) compare(local, remote []int64) {
	if len(local) != len(remote) {
		return
	}
	c.EqualCount++
	var known int
	current, four, two := true, true, true
	for i, ms := range remote {
		if local[i] <= 0 || ms <= 0 {
			continue
		}
		known++
		// 正式整理的本地证据是整数秒，远端保留毫秒。
		delta := ((local[i]+500)/1000)*1000 - ms
		if delta < 0 {
			delta = -delta
		}
		current = current && delta <= max(int64(3000), ms/100)
		four = four && delta <= 4000
		two = two && delta <= 2000
	}
	if known == 0 || known*2 < len(local) {
		return
	}
	c.Comparable++
	if current {
		c.Current++
	}
	if four {
		c.Four++
	}
	if two {
		c.Two++
	}
}
