package classical

import (
	"reflect"
	"testing"
)

func TestParseCatalogsDesignTable(t *testing.T) {
	for _, tc := range []struct{ title, system, number, fragment string }{
		{"Symphony No. 5 in C minor, Op. 67", "Op", "67", "Op. 67"},
		{"Piano Sonata No. 14 in C-sharp minor, Op. 27 No. 2", "Op", "27/2", "Op. 27 No. 2"},
		{"Cello Suite No. 1 in G major, BWV 1007", "BWV", "1007", "BWV 1007"},
		{"Piano Sonata No. 11 in A major, K. 331", "K", "331", "K. 331"},
		{"Serenade No. 13 in G major, KV 525", "K", "525", "KV 525"},
		{"Symphony No. 94 in G major, Hob. I:94", "Hob", "I:94", "Hob. I:94"},
		{"Piano Sonata in B-flat major, D. 960", "D", "960", "D. 960"},
		{"Concerto in D major, RV 93", "RV", "93", "RV 93"},
		{"Water Music Suite No. 1, HWV 348", "HWV", "348", "HWV 348"},
		{"Für Elise, WoO 59", "WoO", "59", "WoO 59"},
		{"c小调第五交响曲，作品67", "Op", "67", "作品67"},
		{"Op. 10 No. 3", "", "", ""},
		{"Prelude in C major", "", "", ""},
		{"Symphony No. 2 in D, Op. 73", "Op", "73", "Op. 73"},
		{"Op..27", "", "", ""},
	} {
		t.Run(tc.title, func(t *testing.T) {
			got := ParseCatalogs(tc.title)
			if tc.system == "" {
				if len(got) != 0 {
					t.Fatalf("编号 = %+v，期望无编号", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("编号 = %+v，期望一条", got)
			}
			entry := got[0]
			fragment := string([]rune(tc.title)[entry.Start : entry.Start+entry.Length])
			if entry.System != tc.system || entry.Number != tc.number || entry.Partial || fragment != tc.fragment {
				t.Fatalf("编号 = %+v，原文 = %q，期望 %s %s / %q", entry, fragment, tc.system, tc.number, tc.fragment)
			}
		})
	}
}

func TestCatalogAtomsShareWhiteoutAndProduceOneEntry(t *testing.T) {
	text := []rune(" BWV 1007; BWV 1008; K. 331")
	env := catalogEnv{}
	atom := catalogAtom("BWV", "BWV", 0)
	if !atom(&env, text) || len(env.entries) != 1 || env.entries[0].Number != "1007" {
		t.Fatalf("一次原子解析应只产一条：%+v", env.entries)
	}
	if got := string(text); got != " BWV     ; BWV 1008; K. 331" {
		t.Fatalf("共享抹白 = %q", got)
	}
	if !catalogSequence(atom, atom, catalogAtom("K", "K", 0))(&env, text) {
		t.Fatal("后续解析器未接力读取同一数组")
	}
	want := []string{"1007", "1008", "331"}
	var numbers []string
	for _, entry := range env.entries {
		numbers = append(numbers, entry.Number)
	}
	if !reflect.DeepEqual(numbers, want) {
		t.Fatalf("接力解析 = %v，期望 %v", numbers, want)
	}
	if got := ParseCatalogs(" Work BWV 1007; BWV 1008; K. 331"); len(got) != 2 || got[0].Number != "1007" || got[1].System != "K" {
		t.Fatalf("多体系解析及按体系去重 = %+v", got)
	}
}

func TestCatalogPrefixBoundaries(t *testing.T) {
	for _, title := range []string{"Op. 10", "Work 10 Op", "Work Op..27"} {
		t.Run(title, func(t *testing.T) {
			if got := ParseCatalogs(title); len(got) != 0 {
				t.Fatalf("不应识别编号：%+v", got)
			}
		})
	}
}

func TestCatalogVolumeSingleSeparatorAndPartial(t *testing.T) {
	for _, tc := range []struct {
		title, number, fragment string
		partial                 bool
	}{
		{"Work Hob I:94/2", "I:94", "Hob I:94", false},
		{"Work Hob I/94:2", "I:94", "Hob I/94", false},
		{"Work Hob I: 5", "I:5", "Hob I: 5", false},
		{"Work H 3", "3", "H 3", true},
	} {
		t.Run(tc.title, func(t *testing.T) {
			got := ParseCatalogs(tc.title)
			if len(got) != 1 {
				t.Fatalf("编号 = %+v", got)
			}
			entry := got[0]
			if entry.System != "Hob" || entry.Number != tc.number || entry.Partial != tc.partial || string([]rune(tc.title)[entry.Start:entry.Start+entry.Length]) != tc.fragment {
				t.Fatalf("卷号 = %+v，期望 %s / partial=%t / %q", entry, tc.number, tc.partial, tc.fragment)
			}
		})
	}
}

func TestCatalogSubnumberRecognitionAndRollback(t *testing.T) {
	for _, tc := range []struct{ title, number, fragment string }{
		{"Work Op. 27 No. 2", "27/2", "Op. 27 No. 2"},
		{"Work Op. 27 nr. 2a", "27/2a", "Op. 27 nr. 2a"},
		{"Work Op. 27, - Allegro 2", "27", "Op. 27"},
	} {
		t.Run(tc.title, func(t *testing.T) {
			got := ParseCatalogs(tc.title)
			if len(got) != 1 {
				t.Fatalf("编号 = %+v", got)
			}
			entry := got[0]
			if entry.Number != tc.number || string([]rune(tc.title)[entry.Start:entry.Start+entry.Length]) != tc.fragment {
				t.Fatalf("副号 = %+v，期望 %s / %q", entry, tc.number, tc.fragment)
			}
		})
	}
}

func TestCatalogSortKey(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"BWV 1043/2", "BWV 1043 2"},
		{"BWV 1043 : 2", "BWV 1043 2"},
		{"-BWV.  1043/2: ", " BWV. 1043 2 "},
	} {
		t.Run(tc.input, func(t *testing.T) {
			if got := CatalogSortKey(tc.input); got != tc.want {
				t.Fatalf("排序键 = %q，期望 %q", got, tc.want)
			}
		})
	}
}
