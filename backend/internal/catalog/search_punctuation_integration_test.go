package catalog

import (
	"reflect"
	"testing"
)

func TestPostgreSQLSearchPunctuationRanking(t *testing.T) {
	f := newPlacementFixture(t)
	for _, title := range []string{
		"ACDC", "AC/DC", "ACDC Tribute", "AC/DC Live",
		"Guns N' Roses", "Guns N’ Roses", "夜曲", "《夜曲》", "C#", "C", "C++",
	} {
		r := f.release(title)
		f.track(r, title, "present", nil)
	}
	publishSearchFixture(t, f)
	for _, tc := range []struct {
		query string
		want  []string
		kinds []string
	}{
		{"acdc", []string{"ACDC", "AC/DC", "ACDC Tribute", "AC/DC Live"}, []string{"exact", "exact", "prefix", "prefix"}},
		{"ac/dc", []string{"AC/DC", "ACDC", "AC/DC Live", "ACDC Tribute"}, []string{"exact", "exact", "prefix", "prefix"}},
		{"guns n roses", []string{"Guns N' Roses", "Guns N’ Roses"}, []string{"exact", "exact"}},
		{"guns n' roses", []string{"Guns N' Roses", "Guns N’ Roses"}, []string{"exact", "exact"}},
		{"guns n’ roses", []string{"Guns N’ Roses", "Guns N' Roses"}, []string{"exact", "exact"}},
		{"夜曲", []string{"夜曲", "《夜曲》"}, []string{"exact", "exact"}},
		{"c#", []string{"C#"}, []string{"exact"}},
	} {
		t.Run(tc.query, func(t *testing.T) {
			for _, kind := range []string{"albums", "tracks"} {
				result := searchFixturePage(t, f.store, kind, tc.query)
				var originals, kinds []string
				switch items := result.Items.(type) {
				case []SearchAlbumItem:
					for _, item := range items {
						originals = append(originals, item.Match.Original)
						kinds = append(kinds, item.Match.Kind)
					}
				case []SearchTrackItem:
					for _, item := range items {
						originals = append(originals, item.Match.Original)
						kinds = append(kinds, item.Match.Kind)
					}
				}
				if result.CatalogState.SearchMode != "normalized" || result.Pagination.Total != int64(len(tc.want)) ||
					result.Pagination.TotalRelation != "eq" || result.Pagination.HasMore ||
					!reflect.DeepEqual(originals, tc.want) || !reflect.DeepEqual(kinds, tc.kinds) {
					t.Fatalf("%s/%q 命中或排序不符：originals=%v kinds=%v result=%+v", kind, tc.query, originals, kinds, result)
				}
			}
		})
	}
}

func TestPostgreSQLArtistSearchPunctuation(t *testing.T) {
	f := newPlacementFixture(t)
	for _, name := range []string{"ac/dc", "acdc"} {
		r := f.release("测试发行 " + name)
		f.exec(`UPDATE releases SET artist=$2,album_artist=$2 WHERE id=$1::uuid`, r.releaseID, name)
		f.track(r, "测试曲目", "present", nil)
	}
	publishArtistReadFixture(t, f)
	slashID := findArtistReadFixture(t, f, "ac/dc")
	plainID := findArtistReadFixture(t, f, "acdc")
	for _, tc := range []struct {
		query string
		ids   []string
	}{
		{"acdc", []string{plainID, slashID}},
		{"ac/dc", []string{slashID, plainID}},
	} {
		result := searchFixturePage(t, f.store, "artists", tc.query)
		items := result.Items.([]SearchArtistItem)
		var ids []string
		for _, item := range items {
			ids = append(ids, item.Artist.ID)
			if item.Match.Kind != "exact" || item.Match.Field != "artist_name" || item.Match.Original != item.Artist.Name {
				t.Fatalf("艺人命中说明错误：%+v", item)
			}
		}
		if result.Pagination.Total != 2 || result.Pagination.TotalRelation != "eq" || result.Pagination.HasMore || !reflect.DeepEqual(ids, tc.ids) {
			t.Fatalf("艺人 %q 命中/顺序错误：ids=%v result=%+v", tc.query, ids, result)
		}
	}
}
