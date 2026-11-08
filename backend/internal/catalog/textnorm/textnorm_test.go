package textnorm

import (
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"unicode"
	"unicode/utf8"
)

func requireEmbedded(t testing.TB) {
	t.Helper()
	if err := LoadEmbedded(); err != nil {
		t.Fatal(err)
	}
}

func TestTextEquivalent(t *testing.T) {
	requireEmbedded(t)
	t.Parallel()
	pairs := [][2]string{
		{"周杰倫", "周杰伦"}, {"ＡＢＣ", "abc"}, {"Beyoncé", "beyonce"},
		{"さユり", "サユリ"}, {"Straße", "STRASSE"}, {"Beyonce\u0301", "Beyoncé"},
		{"ガ", "が"}, {"カ\u3099", "か\u3099"}, {"ｶﾞ", "が"}, {"ﾊﾟ", "ぱ"},
		{"ハ\u309a", "は\u309a"}, {"パ", "ぱ"}, {"ヴ", "う\u3099"},
		{"ヷヸヹヺ", "わ\u3099ゐ\u3099ゑ\u3099を\u3099"},
		{"ヽヾ", "ゝゞ"}, {"ヵヶ", "ゕゖ"}, {"カーテン", "かーてん"},
		{"𠗣", "㓆"}, {"  A\u00a0\t B\u3000C  ", "a b c"},
	}
	for _, pair := range pairs {
		t.Run(pair[0], func(t *testing.T) {
			// 两个方向均以同一个键相互命中，不使用单向包含来替代相等断言。
			left, right := Text(pair[0]), Text(pair[1])
			if left != right {
				t.Fatalf("Text(%q)=%q; Text(%q)=%q", pair[0], left, pair[1], right)
			}
			if Text(left) != right || Text(right) != left {
				t.Fatalf("规范化后的两向匹配不幂等: %q / %q", left, right)
			}
		})
	}
}

func TestTextExact(t *testing.T) {
	requireEmbedded(t)
	t.Parallel()
	cases := []struct{ raw, want string }{
		{"", ""}, {" \t\n\r\u0085\u00a0\u2003\u2028\u2029\u202f\u3000", ""},
		{"ＡＢＣ Café\tStraße 周杰倫 さユり", "abc cafe strasse 周杰伦 さゆり"},
		{"Beyoncé", "beyonce"}, {"ÀÉÎÖÜ Ç Ñ ǰ ẖ", "aeiou c n j h"},
		{"İ I ı", "i i ı"}, {"Ł Æ Œ", "ł æ œ"}, {"Σ ς σ", "σ σ σ"},
		{"ά α", "ά α"}, {"Й И", "й и"}, {"שָׁלוֹם", "שָׁלוֹם"}, {"कि", "कि"},
		{"A\u0301\u0327", "a"}, {"\u0301a", "\u0301a"}, {"a \u0301", "a \u0301"},
		{"a\ufe0f", "a\ufe0f"}, {"a\u034f\u0301", "a\u034f"},
		{"がばぱは", "がばぱは"}, {"ヷヸヹヺ", "わ\u3099ゐ\u3099ゑ\u3099を\u3099"},
		{"ヽヾ ー ㇰㇱㇲㇳㇺ", "ゝゞ ー ㇰㇱㇲㇳㇺ"}, {"ヿ", "こと"},
		{"鍊鏈煉炼链", "炼链炼炼链"}, {"發髮乾幹", "发发干干"},
		{"伕牠暱緪揹", "夫它昵縆背"}, {"㑮", "𫝈"},
		{"% _ ~", "% _ ~"}, {"Ａ－Ｂ − C", "a-b − c"}, {"❤\ufe0f", "❤\ufe0f"},
	}
	for _, test := range cases {
		t.Run(test.raw, func(t *testing.T) {
			if got := Text(test.raw); got != test.want {
				t.Fatalf("Text(%q)=%q, want %q", test.raw, got, test.want)
			}
			if got := Text(test.want); got != test.want {
				t.Fatalf("Text 二次执行=%q, want %q", got, test.want)
			}
		})
	}
}

func TestTextKeepsDistinctKeys(t *testing.T) {
	requireEmbedded(t)
	t.Parallel()
	pairs := [][2]string{
		{"が", "か"}, {"ば", "ぱ"}, {"ば", "は"}, {"ぱ", "は"},
		{"か\u3099", "か"}, {"は\u309a", "は"}, {"は\u3099", "は\u309a"},
		{"炼", "链"}, {"鍊", "鏈"}, {"Ł", "L"}, {"Æ", "AE"}, {"Œ", "OE"},
		{"ά", "α"}, {"й", "и"}, {"कि", "क"}, {"שָׁ", "ש"},
		{"ャ", "ヤ"}, {"ㇰ", "く"}, {"カ", "ka"}, {"カ", "漢"},
		{"おー", "おお"}, {"SRCL-1234", "srcl1234"}, {"a b", "ab"},
	}
	for _, pair := range pairs {
		if got := Text(pair[0]); got == Text(pair[1]) {
			t.Errorf("不能合并 %q 与 %q: %q", pair[0], pair[1], got)
		}
	}
}

func TestCatalogNumber(t *testing.T) {
	requireEmbedded(t)
	t.Parallel()
	cases := []struct{ raw, want string }{
		{"SRCL-1234", "srcl1234"}, {"srcl 1234", "srcl1234"},
		{"ＳＲＣＬ－１２３４", "srcl1234"}, {"SRCL−1234", "srcl1234"},
		{"S\u00a0R\tC\u3000L—1234", "srcl1234"}, {"Beyoncé - ガ - 倫", "beyonceが伦"},
		{"AB/CD.12_~%+34:56", "ab/cd.12_~%+34:56"}, {"a\u00adb", "a\u00adb"},
		{"— − \t", ""}, {"", ""}, {"A-\u0301", "a"}, {"a \u030a", "a"},
		{"ᄀ-ᅡ", "가"}, {"カ-\u3099", "が"}, {"は-\u309a", "ぱ"},
	}
	for _, test := range cases {
		if got := CatalogNumber(test.raw); got != test.want {
			t.Errorf("CatalogNumber(%q)=%q, want %q", test.raw, got, test.want)
		}
		if got := CatalogNumber(CatalogNumber(test.raw)); got != test.want {
			t.Errorf("编号不幂等 %q: %q, want %q", test.raw, got, test.want)
		}
	}
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if unicode.IsSpace(r) || unicode.Is(unicode.Pd, r) || r == '\u2212' {
			if got := CatalogNumber("a" + string(r) + "b"); got != "ab" {
				t.Errorf("未删除 U+%04X: %q", r, got)
			}
		}
	}
}

func TestLongLabelNotTruncated(t *testing.T) {
	requireEmbedded(t)
	t.Parallel()
	raw := strings.Repeat("Ａéガ倫\u00a0", 10000)
	want := strings.TrimSpace(strings.Repeat("aeが伦 ", 10000))
	if got := Text(raw); got != want || Text(got) != want {
		t.Fatalf("极长标签被截断或不幂等: got %d bytes, want %d", len(got), len(want))
	}
	want = strings.Repeat("aeが伦", 10000)
	if got := CatalogNumber(raw); got != want || CatalogNumber(got) != want {
		t.Fatalf("极长编号被截断或不幂等: got %d bytes, want %d", len(got), len(want))
	}
}

func TestConcurrentNormalization(t *testing.T) {
	requireEmbedded(t)
	t.Parallel()
	const raw = "Ｂeyoncé\u00a0Straße ガ ﾊﾟ 周杰倫 SRCL−1234"
	const want = "beyonce strasse が ぱ 周杰伦 srcl−1234"
	const wantNumber = "beyoncestrasseがぱ周杰伦srcl1234"
	var workers sync.WaitGroup
	start := make(chan struct{})
	for range 32 {
		workers.Go(func() {
			<-start
			for range 100 {
				if err := LoadEmbedded(); err != nil {
					t.Error(err)
					return
				}
				if got := Text(raw); got != want {
					t.Errorf("并发 Text=%q", got)
					return
				}
				if got := CatalogNumber(raw); got != wantNumber {
					t.Errorf("并发 CatalogNumber=%q", got)
					return
				}
			}
		})
	}
	close(start)
	workers.Wait()
}

func TestRequiresSuccessfulPreflight(t *testing.T) {
	if mode := os.Getenv("ROOMUSIC_TEXTNORM_PREFLIGHT_TEST"); mode != "" {
		var convert func(string) string
		switch mode {
		case "text":
			convert = Text
		case "number":
			convert = CatalogNumber
		default:
			t.Fatal("未知前置条件测试模式")
		}
		func() {
			defer func() {
				if recovered := recover(); recovered != "textnorm: 必须先成功调用 LoadEmbedded" {
					t.Fatalf("预检前应拒绝调用，得到 %v", recovered)
				}
			}()
			convert("ＡＢＣ")
		}()
		requireEmbedded(t)
		if got := convert("ＡＢＣ"); got != "abc" {
			t.Fatalf("成功预检后=%q", got)
		}
		return
	}
	// 独立进程验证真实公开入口，避免重置全局 sync.Once 与其他并行测试产生竞态。
	for _, mode := range []string{"text", "number"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestRequiresSuccessfulPreflight$", "-test.count=1")
		cmd.Env = append(os.Environ(), "ROOMUSIC_TEXTNORM_PREFLIGHT_TEST="+mode)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("前置条件子进程 %s: %v\n%s", mode, err, output)
		}
	}
}

func FuzzNormalizationIdempotent(f *testing.F) {
	requireEmbedded(f)
	for _, raw := range []string{
		"", "周杰倫 Beyoncé ＡＢＣ さユり", "鍊鏈炼链", "がばぱはガﾊﾟ", "ヷヾㇰー", "Straße",
		"a-\u0301", "ᄀ-ᅡ", "\u0345\u0301", "a\u034f\u0301", "a\ufe0f", "% _ ~ −",
		strings.Repeat("a\u0301", 64), strings.Repeat("\u0301", 64),
	} {
		f.Add(raw)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		if !utf8.ValidString(raw) || len(raw) > 16384 {
			t.Skip()
		}
		for _, convert := range []func(string) string{Text, CatalogNumber} {
			got := convert(raw)
			if !utf8.ValidString(got) {
				t.Fatalf("输出不是 UTF-8: %q", got)
			}
			if twice := convert(got); twice != got {
				t.Fatalf("不幂等: input=%q first=%q second=%q", raw, got, twice)
			}
		}
	})
}

func TestLooseSearchKeys(t *testing.T) {
	requireEmbedded(t)
	t.Parallel()
	for _, tc := range []struct {
		raw, normalized, loose string
		number                 bool
	}{
		{"AC/DC", "ac/dc", "acdc", false},
		{"Guns N' Roses", "guns n' roses", "guns n roses", false},
		{"Guns N’ Roses", "guns n’ roses", "guns n roses", false},
		{"Guns N‘ Roses", "guns n‘ roses", "guns n roses", false},
		{"Guns Nʼ Roses", "guns nʼ roses", "guns n roses", false},
		{"Guns N＇ Roses", "guns n' roses", "guns n roses", false},
		{"周杰伦·", "周杰伦·", "周杰伦", false},
		{"《夜曲》", "《夜曲》", "夜曲", false},
		{"S.H.E", "s.h.e", "she", false},
		{"!!!", "!!!", "", false},
		{"周杰伦", "周杰伦", "周杰伦", false},
		{"Op. 67", "op.67", "op67", true},
		{"SVWC-70594", "svwc70594", "svwc70594", true},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			key := Text(tc.raw)
			if tc.number {
				key = CatalogNumber(tc.raw)
			}
			if key != tc.normalized {
				t.Fatalf("原规范键改变：got %q, want %q", key, tc.normalized)
			}
			if got := Loose(key); got != tc.loose {
				t.Fatalf("Loose(%q)=%q, want %q", key, got, tc.loose)
			}
		})
	}
}
