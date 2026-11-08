package classical

import (
	"reflect"
	"testing"
)

func TestSegmentsReferenceTable(t *testing.T) {
	for _, tc := range []struct {
		title string
		want  []string
	}{
		{"Symphony No. 5: I. Allegro con brio", []string{"Symphony No. 5:", " I.", " Allegro con brio"}},
		{"Symphony No. 5 in C minor, Op. 67: I. Allegro", []string{"Symphony No. 5 in C minor,", " Op. 67:", " I.", " Allegro"}},
		{"Pictures at an Exhibition: Promenade", []string{"Pictures at an Exhibition:", " Promenade"}},
		{"Concerto in D major, K. 331: II. Andante", []string{"Concerto in D major,", " K.", " 331:", " II.", " Andante"}},
		{"Sonata in B-flat major: III. Rondo", []string{"Sonata in B-flat major:", " III.", " Rondo"}},
		{"Prelude: III. Allegro (1998)", []string{"Prelude: III.", " Allegro (1998)"}},
		{"Waltz, Op. 64 No. 1: I. Molto vivace", []string{"Waltz, Op. 64 No. 1:", " I.", " Molto vivace"}},
		{"Mass in B minor, BWV 232: 3:45 intro", []string{"Mass in B minor,", " BWV 232:", " 3:", "45 intro"}},
	} {
		t.Run(tc.title, func(t *testing.T) {
			if got := Segments(tc.title); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("段 = %#v，期望 %#v", got, tc.want)
			}
		})
	}
}

func TestSegmentsSpaceGate(t *testing.T) {
	for _, tc := range []struct {
		title string
		want  []string
	}{
		{"", []string{""}},
		{"A, B", []string{"A, B"}},
		{"Prelude: III.", []string{"Prelude: III."}},
		{"第五交响曲：第一乐章", []string{"第五交响曲：", "第一乐章"}},
	} {
		t.Run(tc.title, func(t *testing.T) {
			if got := Segments(tc.title); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("闸门切分 = %#v，期望 %#v", got, tc.want)
			}
		})
	}
}

func TestSplitWorksReferenceExamples(t *testing.T) {
	for _, tc := range []struct {
		name      string
		titles    []string
		work      string
		movements []string
	}{
		{"A", []string{"Symphony No. 5 in C minor, Op. 67: I. Allegro con brio", "II. Andante con moto", "III. Scherzo. Allegro", "IV. Allegro"}, "Symphony No. 5 in C minor, Op. 67", []string{"I. Allegro con brio", "II. Andante con moto", "III. Scherzo. Allegro", "IV. Allegro"}},
		{"B", []string{"Piano Sonata No. 14: I. Adagio sostenuto", "Piano Sonata No. 14: II. Allegretto", "Piano Sonata No. 14: III. Presto agitato"}, "Piano Sonata No. 14", []string{"I. Adagio sostenuto", "II. Allegretto", "III. Presto agitato"}},
		{"C", []string{"Pictures at an Exhibition: Promenade", "Pictures at an Exhibition: Gnomus", "Pictures at an Exhibition: Promenade"}, "Pictures at an Exhibition", []string{"Promenade", "Gnomus", "Promenade"}},
		{"D", []string{"Untitled", "Untitled"}, "", nil},
		{"E", []string{"Alpha One", "Beta Two"}, "", nil},
		{"中文完整标题", []string{"第五交响曲 c小调 作品67：第一乐章 有活力的快板", "第五交响曲 c小调 作品67：第二乐章 稍快的行板"}, "第五交响曲 c小调 作品67", []string{"第一乐章 有活力的快板", "第二乐章 稍快的行板"}},
		{"中文序号反向定位", []string{"第五交响曲：第一乐章", "第二乐章", "第三乐章"}, "第五交响曲", []string{"第一乐章", "第二乐章", "第三乐章"}},
		{"阿拉伯序号反向定位", []string{"Concerto: 1. Allegro", "2. Adagio", "3. Presto"}, "Concerto", []string{"1. Allegro", "2. Adagio", "3. Presto"}},
		{"古典专辑中的普通歌名", []string{"晴天", "七里香"}, "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := make([]WorkSplit, len(tc.titles))
			if tc.work != "" {
				for i, movement := range tc.movements {
					want[i] = WorkSplit{Work: tc.work, Movement: movement, Group: 1}
				}
			}
			if got := SplitWorks(tc.titles); !reflect.DeepEqual(got, want) {
				t.Fatalf("作品拆分 = %#v，期望 %#v", got, want)
			}
		})
	}
}

func TestSplitWorksIndependentBracketNormalization(t *testing.T) {
	for _, tc := range []struct{ title, want string }{
		{" Sonata (see [note) x] tail ", "Sonata  tail"},
		{"Sonata ([note)] tail", "Sonata  tail"},
		{"[1998 (live] trailing", ""},
		{"Sonata ) [note] tail", "Sonata"},
	} {
		t.Run(tc.title, func(t *testing.T) {
			if got := normalizeTitle(tc.title); got != tc.want {
				t.Fatalf("括号归一化 = %q，期望 %q", got, tc.want)
			}
		})
	}
	if got := SplitWorks([]string{"Untitled (1998)", "Untitled [2001]"}); !reflect.DeepEqual(got, make([]WorkSplit, 2)) {
		t.Fatalf("判定未使用归一化标题：%+v", got)
	}
	titles := []string{"Piano Sonata (live): I. Allegro", "Piano Sonata (live): II. Andante"}
	want := []WorkSplit{{Work: "Piano Sonata (live)", Movement: "I. Allegro", Group: 1}, {Work: "Piano Sonata (live)", Movement: "II. Andante", Group: 1}}
	if got := SplitWorks(titles); !reflect.DeepEqual(got, want) {
		t.Fatalf("切分未保留原文括号：%+v", got)
	}
}

func TestSplitWorksSegmentExtractionAndCommonPrefix(t *testing.T) {
	title := "Work Title ,.;:-/\\ I. Allegro"
	if got := firstSegments(title, 1); got != "Work Title" {
		t.Fatalf("作品名未裁尾标点：%q", got)
	}
	if got := afterSegments(title, 0); got != title {
		t.Fatalf("零段后应返回原文：%q", got)
	}
	for _, titles := range [][2]string{
		{"Work Title: I. Allegro", "Work Title: I. Allegro"},
		{"Work Title:", "Work Title: I. Allegro"},
		{"Work Title: I. Allegro", "Work Title:"},
	} {
		if got := commonSegments(titles[0], titles[1]); got != 0 {
			t.Fatalf("相同或包含的标题 %q 公共段数 = %d", titles, got)
		}
	}
	if got := SplitWorks([]string{"Work Title: I. Allegro"}); !reflect.DeepEqual(got, []WorkSplit{{}}) {
		t.Fatalf("单曲不应产出作品：%+v", got)
	}
}

func TestSplitWorksAdjacentDuplicateMovement(t *testing.T) {
	// 直接检查结组阶段：相邻非白名单乐章只换组号，不改作品名。
	for _, tc := range []struct {
		name      string
		movements []string
		groups    []int
	}{
		{"非白名单相邻重复", []string{"Allegro", "Gnomus", "Gnomus", "Gnomus"}, []int{1, 1, 2, 3}},
		{"Promenade白名单", []string{"Promenade", "Promenade", "Gnomus"}, []int{1, 1, 1}},
		// C′ 的第三首并不相邻；按定稿的相邻语义，即使换成非白名单也不应分裂。
		{"CPrime非白名单不相邻重复", []string{"Allegro", "Gnomus", "Allegro"}, []int{1, 1, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := workGroup{common: 1}
			for _, movement := range tc.movements {
				g.titles = append(g.titles, "Pictures at an Exhibition: "+movement)
			}
			got, next := make([]WorkSplit, len(g.titles)), 0
			g.finish(got, &next)
			for i, split := range got {
				if split.Work != "Pictures at an Exhibition" || split.Movement != tc.movements[i] || split.Group != tc.groups[i] {
					t.Errorf("第 %d 轨 = %+v，期望乐章 %q / 组 %d", i+1, split, tc.movements[i], tc.groups[i])
				}
			}
		})
	}
}
