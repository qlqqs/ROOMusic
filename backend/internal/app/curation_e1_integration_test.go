package app

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/curation"
)

func TestPostgreSQLCurationE1ReleaseAutoFields(t *testing.T) {
	f := newCurationFixture(t)
	f.enable(t)
	_, revisionB := f.join(t, f.b, 1, f.groupA)
	f.apply(t, f.a, "fixture-catalog", "", map[string]any{
		"title": "自动标题 A", "album_artist": "自动专辑艺人", "year": nil,
	})
	f.correct(t, f.a, 1, map[string]any{"title": setChange("人工标题 A"), "label": setChange("人工厂牌")})

	type releaseFields struct {
		ID        string          `json:"id"`
		Auto      json.RawMessage `json:"auto_fields"`
		Corrected []string        `json:"corrected_fields"`
	}
	check := func(t *testing.T, autoB, correctedB []string) {
		t.Helper()
		var comparison struct{ Releases []releaseFields }
		path := comparisonPath(f.groupA)
		getJSON(t, f.application, &f.ordinary, path, &comparison)
		if len(comparison.Releases) != 2 {
			t.Fatalf("%s 返回 %d 个版本，期望 2 个", path, len(comparison.Releases))
		}
		compared := make(map[string]releaseFields, len(comparison.Releases))
		for _, release := range comparison.Releases {
			compared[release.ID] = release
		}
		for _, want := range []struct {
			id              string
			auto, corrected []string
		}{
			{f.a, []string{"album_artist", "year"}, []string{"title", "label"}},
			{f.b, autoB, correctedB},
		} {
			detailPath := "/api/v1/releases/" + want.id
			var detail releaseFields
			getJSON(t, f.application, &f.ordinary, detailPath, &detail)
			for _, response := range []struct {
				path string
				got  releaseFields
			}{{detailPath, detail}, {path, compared[want.id]}} {
				got := response.got
				if got.ID != want.id || got.Corrected == nil || !slices.Equal(got.Corrected, want.corrected) {
					t.Errorf("%s 的版本 %s：id=%s，corrected_fields=%v，期望 %v", response.path, want.id, got.ID, got.Corrected, want.corrected)
				}
				if want.auto == nil {
					if got.Auto != nil {
						t.Errorf("%s 的版本 %s：auto_fields 应省略，实际为 %s", response.path, want.id, got.Auto)
					}
					continue
				}
				var auto []string
				if err := json.Unmarshal(got.Auto, &auto); err != nil || !slices.Equal(auto, want.auto) {
					t.Errorf("%s 的版本 %s：auto_fields=%s，期望 %v，解析错误=%v", response.path, want.id, got.Auto, want.auto, err)
				}
			}
		}
	}
	t.Run("另一个版本没有自动值", func(t *testing.T) { check(t, nil, nil) })
	f.apply(t, f.b, "fixture-catalog", "", map[string]any{"title": "自动标题 B"})
	t.Run("另一个版本有不同的自动字段", func(t *testing.T) { check(t, []string{"title"}, nil) })
	f.correct(t, f.b, revisionB, map[string]any{"title": setChange("人工标题 B")})
	t.Run("另一个版本的自动值均被人工修正覆盖", func(t *testing.T) { check(t, nil, []string{"title"}) })
}

func e1SuppressionIDs(t *testing.T, f *curationFixture) map[string]string {
	t.Helper()
	var result struct{ Items []curation.Suppression }
	getJSON(t, f.application, &f.admin, curationBase+"/suppressions?release_id="+f.a, &result)
	ids := make(map[string]string, len(result.Items))
	for _, marker := range result.Items {
		if _, exists := ids[marker.Subject]; exists {
			t.Fatalf("同一主题存在多条标记：%+v", result.Items)
		}
		ids[marker.Subject] = marker.ID
	}
	return ids
}

func TestPostgreSQLCurationE1ClearExternalSuppressions(t *testing.T) {
	for _, test := range []struct {
		subject string
		apply   func(*testing.T, *curationFixture) string
	}{
		{"artwork", func(t *testing.T, f *curationFixture) string {
			return applyExternalArtwork(t, f.writer, externalArtworkRequest(f.a))
		}},
		{"lyrics", func(t *testing.T, f *curationFixture) string {
			track, _ := externalTrack(t, f.application, f.a)
			return applyExternalLyrics(t, f.writer, externalLyricsRequest(track))
		}},
	} {
		for _, subject := range []string{test.subject, test.subject + "_source"} {
			t.Run(subject, func(t *testing.T) {
				f := newCurationFixture(t)
				f.enable(t)
				original := test.apply(t, f)
				f.undo(t, original)
				ids := e1SuppressionIDs(t, f)
				if len(ids) != 2 || ids[test.subject] == "" || ids[test.subject+"_source"] == "" {
					t.Fatalf("撤销后的标记=%v，期望一对 %s 标记", ids, test.subject)
				}
				clearCurationSuppression(t, f, ids[subject])
				if remaining := e1SuppressionIDs(t, f); len(remaining) != 0 {
					t.Fatalf("清除 %s 后仍有标记 %v", subject, remaining)
				}
				if next := test.apply(t, f); next == original {
					t.Fatalf("再次写入返回了原操作 ID %s", next)
				}
			})
		}
	}
}

func TestPostgreSQLCurationE1ClearFieldSuppression(t *testing.T) {
	f := newCurationFixture(t)
	f.enable(t)
	f.undo(t, f.apply(t, f.a, "fixture-catalog", "", map[string]any{"title": "自动标题", "label": "自动厂牌"}))
	ids := e1SuppressionIDs(t, f)
	if len(ids) != 2 || ids["title"] == "" || ids["label"] == "" {
		t.Fatalf("撤销后的标记=%v，期望 title 和 label", ids)
	}
	clearCurationSuppression(t, f, ids["title"])
	if remaining := e1SuppressionIDs(t, f); len(remaining) != 1 || remaining["label"] != ids["label"] {
		t.Fatalf("清除 title 后的标记=%v，期望保留 label=%s", remaining, ids["label"])
	}
	response := f.application.request(t, http.MethodDelete, curationBase+"/suppressions/"+ids["title"], nil, &f.admin, nil)
	requireAPIError(t, response, http.StatusNotFound, "not_found")
}
