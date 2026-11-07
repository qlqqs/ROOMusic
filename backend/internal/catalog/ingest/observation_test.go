package ingest

import (
	"reflect"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
)

func TestGenreFieldObservationJoinsValuesAndPreservesSource(t *testing.T) {
	for _, tc := range []struct {
		name   string
		genres []string
		value  string
	}{
		{"多流派往返", []string{"国语流行乐", "台语流行乐", "R&B/Soul", "Folk, World, & Country"}, "国语流行乐; 台语流行乐; R&B/Soul; Folk, World, & Country"},
		{"无流派不写观察", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observation := SourceObservation{Genres: tc.genres, SourceKind: "cue_virtual", FieldSources: map[string]string{"genre": "cue_sheet"}}
			var genres []fieldObservation
			for _, field := range observation.fieldObservations() {
				if field.Name == "genre" {
					genres = append(genres, field)
				}
			}
			if len(tc.genres) == 0 {
				if len(genres) != 0 {
					t.Fatalf("无流派时不应写入观察：%#v", genres)
				}
				return
			}
			want := []fieldObservation{{Name: "genre", Value: tc.value, SourceKind: "cue_sheet"}}
			if !reflect.DeepEqual(genres, want) {
				t.Fatalf("流派应保存为一条连接值及其来源：%#v，期望 %#v", genres, want)
			}
			if got := metadata.SplitGenres(genres[0].Value); !reflect.DeepEqual(got, tc.genres) {
				t.Fatalf("流派观察拆分后 = %#v，期望原列表 %#v", got, tc.genres)
			}
		})
	}
}
