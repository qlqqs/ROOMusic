package metadata

import (
	"reflect"
	"strings"
	"testing"
)

func TestSplitGenres(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values []string
		want   []string
	}{
		{"拆分约定", []string{" 国语流行乐 ; 台语流行乐；R&B/Soul、Folk, World, & Country "}, []string{"国语流行乐", "台语流行乐", "R&B/Soul", "Folk, World, & Country"}},
		{"同键保留首次写法", []string{" Ｐｏｐ ; pop", "POP；Rock", " rock "}, []string{"Ｐｏｐ", "Rock"}},
		{"空项丢弃", []string{" ;；、 \t", ""}, []string{}},
		{"规范化单项", []string{" Alternative   Rock ; R\x00&B/Soul "}, []string{"Alternative Rock", "R&B/Soul"}},
		{"分别限定单项长度", []string{strings.Repeat("a", MaxTextBytes+1) + ";Jazz"}, []string{strings.Repeat("a", MaxTextBytes), "Jazz"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := SplitGenres(tc.values...); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("流派列表 = %#v，期望 %#v", got, tc.want)
			}
		})
	}
}

func TestGenreKey(t *testing.T) {
	for _, pair := range [][2]string{{" ＰＯＰ ", "pop"}, {"Straße", "STRASSE"}, {"Alternative   Rock", "alternative rock"}} {
		if GenreKey(pair[0]) != GenreKey(pair[1]) {
			t.Fatalf("大小写或兼容字形未合并：%q / %q", pair[0], pair[1])
		}
	}
	if GenreKey("國語流行樂") == GenreKey("国语流行乐") {
		t.Fatal("流派键不应合并繁简写法")
	}
}
