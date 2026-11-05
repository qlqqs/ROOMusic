package artistgraph

import (
	"bytes"
	"fmt"
	"io/fs"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
)

func TestInitialGroup(t *testing.T) {
	tests := []struct{ name, want string }{
		{"", "#"}, {" \t\n\u3000", "#"}, {"\u3000\u00a0Beyoncé", "B"},
		{"éclair", "E"}, {"E\u0301dith Piaf", "E"}, {"Ångström", "A"},
		{"İbrahim", "I"}, {"ßeta", "S"}, {"Łukasz", "#"}, {"Æther", "#"},
		{"Ｂｅｙｏｎｃé", "B"}, {"ﬃ Artist", "F"}, {"Ⓐrtist", "A"},
		{"周杰伦", "Z"}, {"周杰倫", "Z"}, {"邓丽君", "D"}, {"鄧麗君", "D"},
		{"久石譲", "J"}, {"坂本龍一", "B"}, {"乾隆", "Q"},
		{"重阳", "Z"}, {"单田芳", "D"}, // 固定首音，不按姓氏或词义猜读法。
		{"㐀", "Q"}, {"龘", "D"}, {"𠀀", "H"}, {"\U00032389", "M"},
		{"𠮷", "#"}, {"\U0003238A", "#"}, // 固定表无读音，不承诺覆盖全部汉字。
		{"さユり", "kana"}, {"カラヤン", "kana"}, {"ﾊﾟ", "kana"},
		{"ゔ", "kana"}, {"ヷ", "kana"}, {"ㇰ", "kana"},
		{"123 Artist", "#"}, {"'Bach", "#"}, {"（周杰伦）", "#"},
		{"- Édith", "#"}, {"\u0301A", "#"}, {"\u200bA", "#"},
		{"αβ", "#"}, {"Борис", "#"}, {"한글", "#"}, {"🎵周杰伦", "#"},
		{"\ufffd", "#"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := InitialGroup(tt.name)
			if err != nil || got != tt.want {
				t.Fatalf("InitialGroup(%q) = %q, %v; want %q", tt.name, got, err, tt.want)
			}
		})
	}
	if group, err := InitialGroup("\xff"); err == nil || group != "" {
		t.Fatalf("非法 UTF-8 必须报错，不返回伪成功分组: %q, %v", group, err)
	}
}

func TestInitialGroupHanFallbackOnlyWhenMissing(t *testing.T) {
	if err := LoadInitials(); err != nil {
		t.Fatal(err)
	}
	if got := initialGroup("國", initialsTable{'国': 'G'}); got != "G" {
		t.Fatalf("缺表时应尝试明确繁简对应: %s", got)
	}
	if got := initialGroup("乾", initialsTable{'乾': 'Q', '干': 'G'}); got != "Q" {
		t.Fatalf("有本字读音时不得先做繁简转换: %s", got)
	}
	if got := initialGroup("國", initialsTable{}); got != "#" {
		t.Fatalf("繁简对应也无读音时归 #: %s", got)
	}
}

func TestInitialsEmbeddedTable(t *testing.T) {
	table, err := loadInitialsData(pinyinFiles)
	if err != nil {
		t.Fatal(err)
	}
	if len(table) != 44348 {
		t.Fatalf("条目数 = %d", len(table))
	}
	for cp, want := range map[rune]byte{'㐀': 'Q', '周': 'Z', '乾': 'Q', '龘': 'D', '𠀀': 'H', '\U00032389': 'M'} {
		if got := table[cp]; got != want {
			t.Fatalf("U+%04X = %c; want %c", cp, got, want)
		}
	}
	if _, exists := table['𠮷']; exists {
		t.Fatal("缺表测试夹具已失效")
	}
	license, err := fs.ReadFile(pinyinFiles, "data/pinyin/LICENSE")
	if err != nil || !bytes.Contains(license, []byte("UNICODE LICENSE V3")) {
		t.Fatalf("完整 Unicode V3 许可证未内置: %v", err)
	}
}

func TestParseInitialsRejectsBadTables(t *testing.T) {
	tests := map[string]string{
		"empty": "", "no final LF": "U+3400\tQ", "empty row": "\n",
		"blank row": "U+3400\tQ\n\n", "UTF-8": "U+3400\t\xff\n",
		"surrogate": "U+D800\tQ\n", "above scalar": "U+110000\tQ\n",
		"overflow": "U+FFFFFFFF\tQ\n", "negative": "U+-001\tQ\n",
		"invalid hex": "U+ZZZZ\tQ\n", "no prefix": "3400\tQ\n",
		"lowercase hex": "U+4e00\tY\n", "short key": "U+123\tA\n",
		"padding": "U+03400\tQ\n", "duplicate": "U+3400\tQ\nU+3400\tQ\n",
		"descending": "U+3401\tT\nU+3400\tQ\n", "space delimiter": "U+3400 Q\n",
		"extra column": "U+3400\tQ\tx\n", "empty initial": "U+3400\t\n",
		"lowercase": "U+3400\tq\n", "multiple initials": "U+3400\tQU\n",
		"accent": "U+3400\tǍ\n", "digit": "U+3400\t1\n",
		"punctuation": "U+3400\t#\n", "space": "U+3400\t Q\n",
		"CRLF": "U+3400\tQ\r\n", "too large": strings.Repeat("x", maxInitialsFileBytes+1),
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			table, err := parseInitials([]byte(data))
			if err == nil || table != nil {
				t.Fatal("坏表必须失败，不发布部分结果")
			}
		})
	}
	// 补充平面与 Unicode scalar 上界仍按码点解析，不依赖 UTF-16 截断。
	table, err := parseInitials([]byte("U+3400\tQ\nU+20000\tH\nU+10FFFF\tZ\n"))
	if err != nil || len(table) != 3 || table['\U0010FFFF'] != 'Z' {
		t.Fatalf("合法 scalar 边界: %v, %v", table, err)
	}
}

func initialsFixture(t *testing.T) fstest.MapFS {
	t.Helper()
	result := fstest.MapFS{}
	for _, name := range []string{"initials.tsv", "LICENSE"} {
		path := "data/pinyin/" + name
		data, err := fs.ReadFile(pinyinFiles, path)
		if err != nil {
			t.Fatal(err)
		}
		result[path] = &fstest.MapFile{Data: data}
	}
	return result
}

func TestLoadInitialsRejectsCorruptEmbeddedFiles(t *testing.T) {
	for _, name := range []string{"initials.tsv", "LICENSE"} {
		for _, mode := range []string{"missing", "hash", "oversized"} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				source := initialsFixture(t)
				path := "data/pinyin/" + name
				switch mode {
				case "missing":
					delete(source, path)
				case "hash":
					source[path].Data[0] ^= 1
				case "oversized":
					source[path].Data = bytes.Repeat([]byte{'x'}, maxInitialsFileBytes+1)
				}
				var cache initialsCache
				first := cache.load(source)
				if first == nil || cache.table != nil {
					t.Fatal("坏文件必须阻止发布")
				}
				if second := cache.load(pinyinFiles); second != first || cache.table != nil {
					t.Fatal("失败必须缓存，不能静默重新加载")
				}
			})
		}
	}
}

func TestLoadInitialsConcurrentFirstLoad(t *testing.T) {
	for _, valid := range []bool{true, false} {
		t.Run(fmt.Sprint(valid), func(t *testing.T) {
			var source fs.FS = pinyinFiles
			if !valid {
				source = fstest.MapFS{}
			}
			var cache initialsCache
			var workers sync.WaitGroup
			start := make(chan struct{})
			for range 24 {
				workers.Go(func() {
					<-start
					err := cache.load(source)
					if (err == nil) != valid {
						t.Errorf("加载结果不一致: %v", err)
					}
					if valid && initialGroup("乾隆", cache.table) != "Q" {
						t.Error("并发读取了部分表")
					}
				})
			}
			close(start)
			workers.Wait()
		})
	}
}

func FuzzParseInitials(f *testing.F) {
	for _, seed := range []string{"", "U+3400\tQ\n", "U+20000\tH\n", "U+D800\tX\n"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data string) {
		table, err := parseInitials([]byte(data))
		if err != nil {
			if table != nil {
				t.Fatal("失败返回了部分表")
			}
			return
		}
		if len(table) == 0 {
			t.Fatal("成功表为空")
		}
		for _, initial := range table {
			if initial < 'A' || initial > 'Z' {
				t.Fatal("成功表含非法首字母")
			}
		}
	})
}
