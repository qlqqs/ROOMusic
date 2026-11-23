package stewardtools

import (
	"context"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
)

func pageBounds(p input, length int) (int, int) {
	start := min((p.Page-1)*p.PageSize, length)
	return start, min(start+p.PageSize, length)
}

func albumReader(store *catalog.Store) func(context.Context, input) (any, error) {
	return func(ctx context.Context, p input) (any, error) {
		result, err := store.LoadReleaseGroup(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		total := len(result.Releases)
		start, end := pageBounds(p, total)
		result.Releases = result.Releases[start:end]
		return struct {
			catalog.ReleaseGroupDetail
			Pagination        map[string]any `json:"pagination"`
			ReleasesTruncated bool           `json:"releases_truncated"`
		}{result, pagination(p, int64(total)), result.ReleaseCount > int64(total)}, nil
	}
}

func releaseReader(store *catalog.Store) func(context.Context, input) (any, error) {
	return func(ctx context.Context, p input) (any, error) {
		// 与 HTTP 详情按相同顺序读取，先确认可见性，再读取版本的附属资料。
		summary, err := store.LoadReleaseSummary(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		header, err := store.LoadReleaseHeader(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		media, err := store.LoadReleaseMedia(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		credits, err := store.LoadReleaseCredits(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		evidence, err := store.LoadReleaseEvidenceSummary(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		media, total := pageMedia(media, p)
		return struct {
			catalog.ReleaseDetail
			Pagination map[string]any `json:"pagination"`
		}{catalog.ReleaseDetail{
			ReleaseSummary: summary, CandidateKind: header.CandidateKind, Genres: header.Genres,
			CatalogNumber: header.CatalogNumber, Edition: header.Edition, Label: header.Label, Barcode: header.Barcode,
			Media: media, Credits: credits, Evidence: evidence, Revision: header.Revision, CorrectedFields: header.CorrectedFields,
		}, pagination(p, int64(total))}, nil
	}
}

func pageMedia(media []catalog.ReleaseMedium, p input) ([]catalog.ReleaseMedium, int) {
	total := 0
	for _, medium := range media {
		total += len(medium.Tracks)
	}
	start, end := pageBounds(p, total)
	result := make([]catalog.ReleaseMedium, 0, len(media))
	offset := 0
	for _, medium := range media {
		next := offset + len(medium.Tracks)
		if start < next && end > offset {
			medium.Tracks = medium.Tracks[max(0, start-offset):min(len(medium.Tracks), end-offset)]
			result = append(result, medium)
		}
		offset = next
	}
	return result, total
}

func trackReader(store *catalog.Store, issues *governance.Store) func(context.Context, input) (any, error) {
	return func(ctx context.Context, p input) (any, error) {
		summaries, err := store.LoadTrackSummaries(ctx, []string{p.ID})
		if err != nil {
			return nil, err
		}
		summary, exists := summaries[p.ID]
		if !exists {
			return nil, catalog.ErrTrackNotFound
		}
		var detail *catalog.ReleaseTrack
		var revision int64
		if summary.Present {
			state, err := issues.LoadTrackCorrectionState(ctx, p.ID)
			if err != nil {
				return nil, err
			}
			revision = state.Revision
			media, err := store.LoadReleaseMedia(ctx, summary.Release.ID)
			if err != nil {
				return nil, err
			}
			for _, medium := range media {
				for _, track := range medium.Tracks {
					if track.ID == p.ID {
						detail = &track
					}
				}
			}
		}
		// ReleaseTrack 的 Source 已由读取层投影为文件名；两种 DTO 都没有歌词字段。
		return struct {
			catalog.TrackSummary
			Revision int64                 `json:"revision,omitempty"`
			Detail   *catalog.ReleaseTrack `json:"detail"`
		}{summary, revision, detail}, nil
	}
}

func evidenceReader(store *catalog.Store) func(context.Context, input) (any, error) {
	return func(ctx context.Context, p input) (any, error) {
		exists, err := store.ReleaseExists(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, catalog.ErrReleaseNotFound
		}
		// 读取层只提供整理字段，来源路径已经过 SafeRelativeSourceRef；不读取标签原始 JSON 或歌词。
		return store.LoadReleaseEvidence(ctx, p.ID)
	}
}
