package stewardtools

import "testing"

func TestStewardArtistToolRevertibility(t *testing.T) {
	want := map[string]bool{"merge_artists": true, "rename_artist": true, "ignore_artist_candidate": true, "split_artist": false}
	for _, definition := range StewardTools(nil, nil) {
		if revertible, ok := want[definition.Name]; ok {
			if definition.Revertible != revertible {
				t.Errorf("%s 的 Revertible 为 %t，期望 %t", definition.Name, definition.Revertible, revertible)
			}
			delete(want, definition.Name)
		}
	}
	if len(want) != 0 {
		t.Errorf("缺少艺人工具定义：%v", want)
	}
}
