package sources

import (
	"reflect"
	"testing"
)

func TestFrontImage(t *testing.T) {
	back := CoverImage{ID: "back", Back: true, Types: []string{"Back"}}
	fallback := CoverImage{ID: "typed-front", Types: []string{"Booklet", "fRoNt"}, URL: "https://example.test/fallback"}
	second := CoverImage{ID: "second", Types: []string{"Front"}}
	explicit := CoverImage{ID: "flagged-front", Front: true, Thumbnails: map[string]string{"250": "https://example.test/front"}}
	later := CoverImage{ID: "later", Front: true}
	invalid := CoverImage{Front: true, Types: []string{"Front"}}
	for _, tc := range []struct {
		name   string
		images []CoverImage
		want   CoverImage
	}{
		{"explicit_wins", []CoverImage{invalid, fallback, explicit, later}, explicit},
		{"first_front_type", []CoverImage{back, invalid, fallback, second}, fallback},
		{"no_front", []CoverImage{invalid, back}, CoverImage{}},
		{"empty", nil, CoverImage{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := FrontImage(tc.images); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("FrontImage=%+v，期望 %+v", got, tc.want)
			}
		})
	}
}
