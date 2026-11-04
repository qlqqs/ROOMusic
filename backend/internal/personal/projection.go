package personal

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog"
)

// QueueTracks 是收藏和歌单共用的单快照队列计数，不代表浏览器已支持音频格式。
type QueueTracks struct {
	Items         []catalog.TrackSummary `json:"items"`
	Total         int64                  `json:"total"`
	MissingCount  int64                  `json:"missing_count"`
	EligibleCount int64                  `json:"eligible_count"`
	ReturnedCount int64                  `json:"returned_count"`
	OmittedCount  int64                  `json:"omitted_count"`
	Limit         int                    `json:"limit"`
	Truncated     bool                   `json:"truncated"`
}

type CoverGroup struct {
	GroupID string                  `json:"group_id"`
	Artwork *catalog.ReleaseArtwork `json:"artwork"`
}
type PlaylistSummary struct {
	PlaylistState
	DurationSeconds      float64      `json:"duration_seconds"`
	UnknownDurationCount int64        `json:"unknown_duration_count"`
	MissingCount         int64        `json:"missing_count"`
	CoverGroups          []CoverGroup `json:"cover_groups"`
}

func readPageOffset(page Page, maxSize int, minRevision int64) (int64, error) {
	if page.Number < 1 || page.Size < 1 || page.Size > maxSize {
		return 0, ErrInvalidInput
	}
	if page.Revision != nil && (*page.Revision < minRevision || *page.Revision > MaxRevision) {
		return 0, ErrInvalidInput
	}
	if int64(page.Number-1) > math.MaxInt64/int64(page.Size) {
		return 0, ErrInvalidInput
	}
	return int64(page.Number-1) * int64(page.Size), nil
}

func personalAlbum(group catalog.PersonalReleaseGroupSummary, savedTitle, savedArtist string) (PersonalAlbum, error) {
	album := PersonalAlbum{GroupID: group.GroupID, Available: group.Available, Title: savedTitle, Artist: savedArtist}
	if !group.Available {
		if strings.TrimSpace(album.Title) == "" {
			album.Title = "暂时不可用的专辑"
		}
		return album, nil
	}
	if group.Representative == nil || group.ReleaseCount < 1 {
		return PersonalAlbum{}, fmt.Errorf("available album projection incomplete")
	}
	album.Title, album.Artist = group.Representative.Title, group.Representative.Artist
	album.ReleaseCount, album.Representative = group.ReleaseCount, group.Representative
	return album, nil
}

// orderedTrackSummaries 保持调用方顺序；只有选中的候选才加载完整摘要，每批至多一千首。
func orderedTrackSummaries(ctx context.Context, source Catalog, ids []string, presentOnly bool) ([]catalog.TrackSummary, error) {
	result := make([]catalog.TrackSummary, 0, len(ids))
	for start := 0; start < len(ids); start += 1000 {
		batch := ids[start:min(start+1000, len(ids))]
		summaries, err := source.LoadTrackSummaries(ctx, batch)
		if err != nil {
			return nil, fmt.Errorf("load personal track summaries: %w", err)
		}
		for _, id := range batch {
			item, ok := summaries[id]
			if !ok || item.TrackID != id {
				return nil, fmt.Errorf("personal track summary missing or mismatched")
			}
			if presentOnly && !item.Present {
				return nil, fmt.Errorf("personal queue snapshot inconsistent")
			}
			result = append(result, item)
		}
	}
	return result, nil
}

func personalTrackFacts(ctx context.Context, source Catalog, ids []string) (map[string]catalog.TrackFact, error) {
	unique := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	if len(unique) == 0 {
		return map[string]catalog.TrackFact{}, nil
	}
	facts, err := source.LoadTrackFacts(ctx, unique)
	if err != nil {
		return nil, fmt.Errorf("load personal track facts: %w", err)
	}
	for _, id := range unique {
		fact, ok := facts[id]
		if !ok || fact.TrackID != id {
			return nil, fmt.Errorf("personal track fact missing or mismatched")
		}
	}
	return facts, nil
}

type queueSelection struct {
	ids            []string
	total, missing int64
}

func (selection *queueSelection) add(ctx context.Context, source Catalog, ids []string) error {
	facts, err := personalTrackFacts(ctx, source, ids)
	if err != nil {
		return err
	}
	if selection.total > MaxRevision-int64(len(ids)) {
		return fmt.Errorf("personal queue count exhausted")
	}
	for _, id := range ids {
		selection.total++
		if !facts[id].Present {
			selection.missing++
			continue
		}
		if len(selection.ids) < TrackLimit {
			selection.ids = append(selection.ids, id)
		}
	}
	return nil
}

func (selection *queueSelection) project(ctx context.Context, source Catalog) (QueueTracks, error) {
	items, err := orderedTrackSummaries(ctx, source, selection.ids, true)
	if err != nil {
		return QueueTracks{}, err
	}
	eligible := selection.total - selection.missing
	omitted := eligible - int64(len(items))
	return QueueTracks{Items: items, Total: selection.total, MissingCount: selection.missing,
		EligibleCount: eligible, ReturnedCount: int64(len(items)), OmittedCount: omitted,
		Limit: TrackLimit, Truncated: omitted > 0}, nil
}

func accumulatePlaylist(summary *PlaylistSummary, fact catalog.TrackFact) error {
	if !fact.Present {
		summary.MissingCount++
	}
	duration := fact.DurationSeconds
	if duration == nil || *duration <= 0 || math.IsNaN(*duration) || math.IsInf(*duration, 0) {
		summary.UnknownDurationCount++
	} else {
		summary.DurationSeconds += *duration
		if math.IsInf(summary.DurationSeconds, 0) {
			return fmt.Errorf("playlist duration overflow")
		}
	}
	if len(summary.CoverGroups) >= 4 {
		return nil
	}
	for _, group := range summary.CoverGroups {
		if group.GroupID == fact.GroupID {
			return nil
		}
	}
	summary.CoverGroups = append(summary.CoverGroups, CoverGroup{GroupID: fact.GroupID})
	return nil
}
