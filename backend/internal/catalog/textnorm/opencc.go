package textnorm

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"sync"
	"sync/atomic"
)

//go:embed data/opencc/*
var openCCFiles embed.FS

const maxEmbeddedFileBytes = 1 << 20

var embeddedTables embeddedCache

type characterTable map[rune]rune

type embeddedCache struct {
	once  sync.Once
	table atomic.Pointer[characterTable]
	err   error
}

// LoadEmbedded 有界校验内置 OpenCC 字表与完整许可证的固定 SHA-256，并构建只读转换表。
// 结果和错误均由 sync.Once 缓存；并发调用安全，不联网、不在 init 中加载或 panic。
// 调用方必须先检查返回错误；失败只阻止依赖新规则的投影，不应阻断旧目录/播放/搜索。
func LoadEmbedded() error {
	return embeddedTables.load(openCCFiles)
}

func (cache *embeddedCache) load(source fs.FS) error {
	cache.once.Do(func() {
		table, err := loadEmbedded(source)
		cache.err = err
		if err == nil {
			// 完整解析/收敛成功之后才发布，失败不会留下可调用的半套字表。
			cache.table.Store(&table)
		}
	})
	return cache.err
}

func (cache *embeddedCache) require() characterTable {
	table := cache.table.Load()
	if table == nil {
		panic("textnorm: 必须先成功调用 LoadEmbedded")
	}
	return *table
}

func loadEmbedded(source fs.FS) (characterTable, error) {
	files := []struct {
		name   string
		sha256 string
	}{
		{"TSCharacters.txt", "6b5a0a799bea2bb22c001f635eaa3fc2904310f0c08addbff275477a80ecf09a"},
		{"STCharacters.txt", "ed1d268e0ad028511dcf5b0089faed0a980ad332449ec11d481ceefde6879f41"},
		{"LICENSE", "b534e465949558eec2597b04f5092b5e161236a68dfbfd04d547592ac3964308"},
	}
	data := make(map[string][]byte, len(files))
	for _, file := range files {
		content, err := readEmbeddedFile(source, "data/opencc/"+file.name)
		if err != nil {
			return nil, fmt.Errorf("textnorm: 读取 %s: %w", file.name, err)
		}
		if actual := fmt.Sprintf("%x", sha256.Sum256(content)); actual != file.sha256 {
			return nil, fmt.Errorf("textnorm: %s SHA-256 不匹配", file.name)
		}
		data[file.name] = content
	}
	return loadCharacterTables(data["TSCharacters.txt"], data["STCharacters.txt"])
}

func readEmbeddedFile(source fs.FS, name string) ([]byte, error) {
	file, err := source.Open(name)
	if err != nil {
		return nil, err
	}
	content, readErr := io.ReadAll(io.LimitReader(file, maxEmbeddedFileBytes+1))
	closeErr := file.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(content) > maxEmbeddedFileBytes {
		return nil, fmt.Errorf("内置文件超过 %d 字节", maxEmbeddedFileBytes)
	}
	return content, nil
}
