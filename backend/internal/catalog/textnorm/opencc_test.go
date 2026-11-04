package textnorm

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
)

func TestEmbeddedOpenCCIntegrity(t *testing.T) {
	requireEmbedded(t)
	t.Parallel()
	files := []struct {
		name, checksum string
		entries        int
	}{
		{"TSCharacters.txt", "6b5a0a799bea2bb22c001f635eaa3fc2904310f0c08addbff275477a80ecf09a", 4113},
		{"STCharacters.txt", "ed1d268e0ad028511dcf5b0089faed0a980ad332449ec11d481ceefde6879f41", 3980},
		{"LICENSE", "b534e465949558eec2597b04f5092b5e161236a68dfbfd04d547592ac3964308", 0},
	}
	seen := make(map[rune]bool)
	for _, file := range files {
		data, err := openCCFiles.ReadFile("data/opencc/" + file.name)
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != file.checksum {
			t.Errorf("%s SHA-256=%s, want %s", file.name, got, file.checksum)
		}
		if file.entries == 0 {
			if !strings.Contains(string(data), "END OF TERMS AND CONDITIONS") {
				t.Fatal("内置许可证不完整")
			}
			continue
		}
		entries, err := parseDictionary(file.name, data)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != file.entries {
			t.Fatalf("%s 有 %d 行，want %d", file.name, len(entries), file.entries)
		}
		for key, candidates := range entries {
			seen[key] = true
			for _, candidate := range candidates {
				seen[candidate] = true
			}
		}
	}
	source, err := openCCFiles.ReadFile("data/opencc/SOURCE.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "556ed22496d650bd0b13b6c163be9814637970ae") {
		t.Fatal("未内置固定提交的来源记录")
	}
	for r := range seen {
		// 真实字表的每个键及全部候选都检查，而不只验证几组常用汉字。
		for _, raw := range []string{string(r), "Ａ-" + string(r) + "\u0301\u00a0ガ"} {
			for _, convert := range []func(string) string{Text, CatalogNumber} {
				first := convert(raw)
				if second := convert(first); second != first {
					t.Fatalf("真实字表 U+%04X 不幂等: %q→%q→%q", r, raw, first, second)
				}
			}
		}
	}
	if Version != 1 {
		t.Fatal("本夹具只对应 Version=1")
	}
}

func TestEmbeddedLoadRejectsMissingCorruptOrOversizedFiles(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"TSCharacters.txt", "STCharacters.txt", "LICENSE"} {
		for _, mode := range []string{"missing", "checksum", "oversized"} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				files := embeddedFixture(t)
				path := "data/opencc/" + name
				switch mode {
				case "missing":
					delete(files, path)
				case "checksum":
					files[path].Data = append(files[path].Data, '\n')
				case "oversized":
					files[path].Data = make([]byte, maxEmbeddedFileBytes+1)
				}
				var cache embeddedCache
				err := cache.load(files)
				if err == nil || cache.table.Load() != nil {
					t.Fatalf("损坏字表/许可证不能发布: err=%v table=%v", err, cache.table.Load())
				}
				if !strings.Contains(err.Error(), name) {
					t.Fatalf("诊断缺文件上下文: %v", err)
				}
				// 错误也缓存；即使随后提供正确 FS，也不能悄悄替换本进程规则。
				if again := cache.load(openCCFiles); again != err || cache.table.Load() != nil {
					t.Fatalf("错误未缓存: first=%v second=%v", err, again)
				}
				assertUnavailableTablePanics(t, &cache)
			})
		}
	}
}

func TestEmbeddedCacheConcurrentColdLoad(t *testing.T) {
	t.Parallel()
	for _, broken := range []bool{false, true} {
		t.Run(fmt.Sprint(broken), func(t *testing.T) {
			var cache embeddedCache
			files := embeddedFixture(t)
			if broken {
				delete(files, "data/opencc/TSCharacters.txt")
			}
			counted := &countingFS{FS: files}
			var workers sync.WaitGroup
			start := make(chan struct{})
			errorsSeen := make(chan error, 32)
			for range 32 {
				workers.Go(func() {
					<-start
					err := cache.load(counted)
					errorsSeen <- err
					if err == nil {
						if got := normalizeText("Ｂeyoncé ガ 周杰倫", cache.require()); got != "beyonce が 周杰伦" {
							t.Errorf("并发加载后=%q", got)
						}
					}
				})
			}
			close(start)
			workers.Wait()
			close(errorsSeen)
			for err := range errorsSeen {
				if (err != nil) != broken || err != cache.err {
					t.Errorf("并发调用结果不同: err=%v cache=%v broken=%t", err, cache.err, broken)
				}
			}
			wantOpens := int32(3)
			if broken {
				wantOpens = 1
				assertUnavailableTablePanics(t, &cache)
			}
			if actual := counted.opens.Load(); actual != wantOpens {
				t.Errorf("没有一次性缓存: open=%d, want %d", actual, wantOpens)
			}
		})
	}
}

func TestReadEmbeddedFileErrors(t *testing.T) {
	t.Parallel()
	failure := errors.New("测试读取失败")
	for _, test := range []struct {
		name        string
		read, close error
	}{
		{"read", failure, nil}, {"close", io.EOF, failure},
	} {
		t.Run(test.name, func(t *testing.T) {
			file := &failingFile{readErr: test.read, closeErr: test.close}
			data, err := readEmbeddedFile(failingFS{file: file}, "fixture")
			if !errors.Is(err, failure) || data != nil || !file.closed {
				t.Fatalf("读取失败未传播/未关闭: data=%v err=%v closed=%t", data, err, file.closed)
			}
		})
	}
}

func assertUnavailableTablePanics(t *testing.T, cache *embeddedCache) {
	t.Helper()
	defer func() {
		if recovered := recover(); recovered != "textnorm: 必须先成功调用 LoadEmbedded" {
			t.Errorf("失败预检仍可取得规则: %v", recovered)
		}
	}()
	cache.require()
}

func embeddedFixture(t *testing.T) fstest.MapFS {
	t.Helper()
	files := make(fstest.MapFS)
	for _, name := range []string{"TSCharacters.txt", "STCharacters.txt", "LICENSE"} {
		path := "data/opencc/" + name
		data, err := openCCFiles.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		files[path] = &fstest.MapFile{Data: data}
	}
	return files
}

type countingFS struct {
	fs.FS
	opens atomic.Int32
}

func (source *countingFS) Open(name string) (fs.File, error) {
	source.opens.Add(1)
	return source.FS.Open(name)
}

type failingFS struct{ file fs.File }

func (source failingFS) Open(string) (fs.File, error) { return source.file, nil }

type failingFile struct {
	readErr, closeErr error
	closed            bool
}

func (file *failingFile) Stat() (fs.FileInfo, error) { return nil, errors.New("测试不支持 Stat") }
func (file *failingFile) Read([]byte) (int, error)   { return 0, file.readErr }
func (file *failingFile) Close() error {
	file.closed = true
	return file.closeErr
}
