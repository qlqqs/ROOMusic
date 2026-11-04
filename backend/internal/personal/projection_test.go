package personal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
)

type projectionCatalog struct {
	Catalog
	facts      func(context.Context, []string) (map[string]catalog.TrackFact, error)
	summaries  func(context.Context, []string) (map[string]catalog.TrackSummary, error)
	groups     func(context.Context, []string) (map[string]catalog.PersonalReleaseGroupSummary, error)
	statistics func(context.Context, []string) (catalog.TrackCollectionStats, error)
}

func (c projectionCatalog) LoadTrackFacts(ctx context.Context, ids []string) (map[string]catalog.TrackFact, error) {
	if c.facts == nil {
		return c.Catalog.LoadTrackFacts(ctx, ids)
	}
	return c.facts(ctx, ids)
}
func (c projectionCatalog) LoadTrackCollectionStats(ctx context.Context, ids []string) (catalog.TrackCollectionStats, error) {
	if c.statistics == nil {
		return c.Catalog.LoadTrackCollectionStats(ctx, ids)
	}
	return c.statistics(ctx, ids)
}
func (c projectionCatalog) LoadTrackSummaries(ctx context.Context, ids []string) (map[string]catalog.TrackSummary, error) {
	if c.summaries == nil {
		return c.Catalog.LoadTrackSummaries(ctx, ids)
	}
	return c.summaries(ctx, ids)
}
func (c projectionCatalog) LoadReleaseGroupSummaries(ctx context.Context, ids []string) (map[string]catalog.PersonalReleaseGroupSummary, error) {
	if c.groups == nil {
		return c.Catalog.LoadReleaseGroupSummaries(ctx, ids)
	}
	return c.groups(ctx, ids)
}

func TestPersonalAlbumProjection(t *testing.T) {
	representative := &catalog.ReleaseSummary{Title: "现在标题", Artist: "现在艺人"}
	for _, tc := range []struct {
		name                           string
		group                          catalog.PersonalReleaseGroupSummary
		savedTitle, savedArtist, title string
		available                      bool
	}{
		{"当前代表", catalog.PersonalReleaseGroupSummary{GroupID: "group", Available: true, ReleaseCount: 2, Representative: representative}, "旧标题", "旧艺人", "现在标题", true},
		{"保存回退", catalog.PersonalReleaseGroupSummary{GroupID: "group"}, "旧标题", "旧艺人", "旧标题", false},
		{"空组占位", catalog.PersonalReleaseGroupSummary{GroupID: "group"}, "", "", "暂时不可用的专辑", false},
		{"隐藏代表不泄露", catalog.PersonalReleaseGroupSummary{GroupID: "group", ReleaseCount: 1, Representative: representative}, "安全标题", "安全艺人", "安全标题", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := personalAlbum(tc.group, tc.savedTitle, tc.savedArtist)
			if err != nil || got.Title != tc.title || got.Available != tc.available {
				t.Fatalf("album: %+v %v", got, err)
			}
			if !got.Available && (got.Representative != nil || got.ReleaseCount != 0 || got.Artist != tc.savedArtist) {
				t.Fatalf("unavailable leaked projection: %+v", got)
			}
		})
	}
	if _, err := personalAlbum(catalog.PersonalReleaseGroupSummary{Available: true}, "", ""); err == nil {
		t.Fatal("accepted unavailable representative")
	}
}

func TestPersonalPlaylistStatistics(t *testing.T) {
	summary := PlaylistSummary{CoverGroups: []CoverGroup{}}
	values := []float64{12, 0, -1, math.NaN(), math.Inf(1), 3}
	for i, value := range values {
		fact := catalog.TrackFact{GroupID: fmt.Sprint(i % 5), Present: i%2 == 0, DurationSeconds: &value}
		if err := accumulatePlaylist(&summary, fact); err != nil {
			t.Fatal(err)
		}
	}
	if err := accumulatePlaylist(&summary, catalog.TrackFact{GroupID: "later", Present: true}); err != nil {
		t.Fatal(err)
	}
	if summary.DurationSeconds != 15 || summary.UnknownDurationCount != 5 || summary.MissingCount != 3 || len(summary.CoverGroups) != 4 {
		t.Fatalf("statistics: %+v", summary)
	}
	for i, group := range summary.CoverGroups {
		if group.GroupID != fmt.Sprint(i) || group.Artwork != nil {
			t.Fatalf("cover order: %+v", summary.CoverGroups)
		}
	}
	overflow := math.MaxFloat64
	summary.DurationSeconds = overflow
	if err := accumulatePlaylist(&summary, catalog.TrackFact{DurationSeconds: &overflow}); err == nil {
		t.Fatal("accepted duration overflow")
	}
}

func TestPersonalQueueSelectionBoundaries(t *testing.T) {
	for _, count := range []int{0, 1, 1000, 1001, 10000, 10001, 12005} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			var factCalls, summaryCalls int
			source := projectionCatalog{
				facts: func(_ context.Context, ids []string) (map[string]catalog.TrackFact, error) {
					factCalls++
					if len(ids) > 10000 {
						t.Fatal("unbounded facts")
					}
					result := map[string]catalog.TrackFact{}
					for _, id := range ids {
						result[id] = catalog.TrackFact{TrackID: id, Present: true}
					}
					return result, nil
				},
				summaries: func(_ context.Context, ids []string) (map[string]catalog.TrackSummary, error) {
					summaryCalls++
					if len(ids) > 1000 {
						t.Fatal("unbounded summaries")
					}
					result := map[string]catalog.TrackSummary{}
					for _, id := range ids {
						result[id] = catalog.TrackSummary{TrackID: id, Present: true}
					}
					return result, nil
				},
			}
			selection := queueSelection{}
			for start := 0; start < count; start += 10000 {
				ids := make([]string, min(10000, count-start))
				for i := range ids {
					ids[i] = fmt.Sprint(start + i)
				}
				if err := selection.add(context.Background(), source, ids); err != nil {
					t.Fatal(err)
				}
			}
			queue, err := selection.project(context.Background(), source)
			if err != nil || queue.Total != int64(count) || queue.Items == nil || queue.ReturnedCount != int64(min(count, 10000)) || queue.OmittedCount != int64(max(count-10000, 0)) || queue.MissingCount != 0 || queue.Truncated != (count > 10000) {
				t.Fatalf("queue: %+v %v", queue, err)
			}
			if factCalls != (count+9999)/10000 || summaryCalls != (min(count, 10000)+999)/1000 {
				t.Fatalf("calls facts=%d summaries=%d", factCalls, summaryCalls)
			}
			for i, item := range queue.Items {
				if item.TrackID != fmt.Sprint(i) {
					t.Fatalf("order %d: %s", i, item.TrackID)
				}
			}
		})
	}
}

func TestPersonalProjectionFailsClosed(t *testing.T) {
	ctx := context.Background()
	fault := errors.New("projection failure")
	for _, tc := range []struct {
		name   string
		source projectionCatalog
	}{
		{"missing fact", projectionCatalog{facts: func(context.Context, []string) (map[string]catalog.TrackFact, error) {
			return map[string]catalog.TrackFact{}, nil
		}}},
		{"fact failure", projectionCatalog{facts: func(context.Context, []string) (map[string]catalog.TrackFact, error) { return nil, fault }}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selection := queueSelection{}
			if err := selection.add(ctx, tc.source, []string{"id"}); err == nil {
				t.Fatal("silently lost referenced track")
			}
		})
	}
	for _, tc := range []struct {
		name string
		item catalog.TrackSummary
		err  error
	}{
		{"missing", catalog.TrackSummary{}, nil}, {"mismatch", catalog.TrackSummary{TrackID: "another", Present: true}, nil},
		{"changed snapshot", catalog.TrackSummary{TrackID: "id", Present: false}, nil}, {"read failure", catalog.TrackSummary{}, fault},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := projectionCatalog{summaries: func(context.Context, []string) (map[string]catalog.TrackSummary, error) {
				return map[string]catalog.TrackSummary{"id": tc.item}, tc.err
			}}
			if _, err := orderedTrackSummaries(ctx, source, []string{"id"}, true); err == nil {
				t.Fatal("accepted broken summary")
			}
		})
	}
}

func TestPersonalReadPageValidationAndWire(t *testing.T) {
	for _, page := range []Page{{Number: 0, Size: 1}, {Number: 1, Size: 101}, {Number: math.MaxInt, Size: 100}} {
		if _, err := readPageOffset(page, 100, 0); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("accepted %+v: %v", page, err)
		}
	}
	if offset, err := readPageOffset(Page{Number: 3, Size: 50}, 100, 0); err != nil || offset != 100 {
		t.Fatalf("offset: %d %v", offset, err)
	}
	for _, value := range []int64{-1, MaxRevision + 1} {
		if _, err := readPageOffset(Page{Number: 1, Size: 1, Revision: &value}, 100, 0); err == nil {
			t.Fatal("accepted invalid revision")
		}
	}
	queue := FavoriteQueue{QueueTracks: QueueTracks{Items: []catalog.TrackSummary{}, Limit: 10000}}
	encoded, err := json.Marshal(queue)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	keys := []string{"items", "total", "revision", "missing_count", "eligible_count", "returned_count", "omitted_count", "limit", "truncated"}
	if len(fields) != len(keys) {
		t.Fatalf("wire keys: %s", encoded)
	}
	for _, key := range keys {
		if _, ok := fields[key]; !ok {
			t.Fatalf("missing wire key %s", key)
		}
	}
	if !reflect.DeepEqual(fields["items"], []any{}) {
		t.Fatalf("null array: %s", encoded)
	}
}
