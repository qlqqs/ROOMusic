package artistgraph

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
)

//go:embed data/pinyin/initials.tsv data/pinyin/LICENSE
var pinyinFiles embed.FS

const (
	initialsEntryCount    = 44348
	initialsSHA256        = "8a419bf46181d6026aa6780202480461c4269266779b28f83fd499cfa045d364"
	initialsLicenseSHA256 = "e7a93b009565cfce55919a381437ac4db883e9da2126fa28b91d12732bc53d96"
	maxInitialsFileBytes  = 1 << 20
)

type initialsTable map[rune]byte

type initialsCache struct {
	once  sync.Once
	table initialsTable
	err   error
}

var embeddedInitials initialsCache

func (cache *initialsCache) load(source fs.FS) error {
	cache.once.Do(func() {
		table, err := loadInitialsData(source)
		if err == nil {
			err = textnorm.LoadEmbedded()
		}
		if err != nil {
			cache.err = fmt.Errorf("artistgraph: 加载首字母数据: %w", err)
			return
		}
		// 所有数据均通过后才发布；读取方均先经过同一个 Once，无部分表或数据竞争。
		cache.table = table
	})
	return cache.err
}

func loadInitialsData(source fs.FS) (initialsTable, error) {
	data, err := readInitialsFile(source, "initials.tsv", initialsSHA256)
	if err != nil {
		return nil, err
	}
	if _, err := readInitialsFile(source, "LICENSE", initialsLicenseSHA256); err != nil {
		return nil, err
	}
	table, err := parseInitials(data)
	if err != nil {
		return nil, err
	}
	if len(table) != initialsEntryCount {
		return nil, fmt.Errorf("首字母表条目数不是 %d", initialsEntryCount)
	}
	return table, nil
}

func readInitialsFile(source fs.FS, name, expectedHash string) ([]byte, error) {
	file, err := source.Open("data/pinyin/" + name)
	if err != nil {
		return nil, fmt.Errorf("读取 %s: %w", name, err)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxInitialsFileBytes+1))
	closeErr := file.Close()
	if readErr != nil {
		return nil, fmt.Errorf("读取 %s: %w", name, readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("关闭 %s: %w", name, closeErr)
	}
	if len(data) > maxInitialsFileBytes {
		return nil, fmt.Errorf("%s 超出大小限制", name)
	}
	if actual := fmt.Sprintf("%x", sha256.Sum256(data)); actual != expectedHash {
		return nil, fmt.Errorf("%s SHA-256 不匹配", name)
	}
	return data, nil
}

func parseInitials(data []byte) (initialsTable, error) {
	if len(data) == 0 || len(data) > maxInitialsFileBytes {
		return nil, fmt.Errorf("首字母表为空或超出大小限制")
	}
	if !utf8.Valid(data) || data[len(data)-1] != '\n' {
		return nil, fmt.Errorf("首字母表必须是有效 UTF-8 且以 LF 结尾")
	}
	table := make(initialsTable)
	var previous rune = -1
	for index, row := range bytes.Split(data[:len(data)-1], []byte{'\n'}) {
		key, value, ok := strings.Cut(string(row), "\t")
		if !ok || len(key) < 6 || len(key) > 8 || !strings.HasPrefix(key, "U+") {
			return nil, initialsRowError(index, "必须是 U+码点与首字母两个 TAB 字段")
		}
		cp, err := strconv.ParseUint(key[2:], 16, 32)
		if err != nil || !utf8.ValidRune(rune(cp)) || key != fmt.Sprintf("U+%04X", cp) {
			return nil, initialsRowError(index, "不是规范 Unicode scalar")
		}
		if len(value) != 1 || value[0] < 'A' || value[0] > 'Z' {
			return nil, initialsRowError(index, "首字母必须是单个 A–Z")
		}
		current := rune(cp)
		if _, exists := table[current]; exists {
			return nil, initialsRowError(index, "重复键")
		}
		if current <= previous {
			return nil, initialsRowError(index, "码点必须严格递增")
		}
		table[current] = value[0]
		previous = current
	}
	return table, nil
}

func initialsRowError(index int, reason string) error {
	// 不回显原始行，避免坏数据污染诊断。
	return fmt.Errorf("首字母表第 %d 行: %s", index+1, reason)
}
