package opra

import (
	"compress/gzip"
	"embed"
	"fmt"
	"sync"
)

//go:embed data/*
var embeddedFiles embed.FS

var embedded struct {
	once    sync.Once
	catalog *Catalog
	err     error
}

// LoadEmbedded 首次调用时解压并校验内置数据；结果及错误常驻，不在 init 中加载或联网。
func LoadEmbedded() (*Catalog, error) {
	embedded.once.Do(func() {
		embedded.catalog, embedded.err = loadEmbedded()
	})
	return embedded.catalog, embedded.err
}

func loadEmbedded() (*Catalog, error) {
	file, err := embeddedFiles.Open("data/opra-v1.jsonl.gz")
	if err != nil {
		return nil, fmt.Errorf("opra: 读取内置数据: %w", err)
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return nil, fmt.Errorf("opra: 解压内置数据: %w", err)
	}
	catalog, parseErr := Load(gz)
	closeErr := gz.Close()
	if parseErr != nil {
		return nil, parseErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	counts := catalog.Counts()
	if counts.Vendors == 0 || counts.Products == 0 || counts.EQs == 0 {
		return nil, fmt.Errorf("opra: 内置数据缺少厂商、产品或 EQ")
	}
	return catalog, nil
}

// SourceNotice 和 LicenseText 供分发方展示来源说明和完整上游许可证。
func SourceNotice() (string, error) {
	data, err := embeddedFiles.ReadFile("data/SOURCE.md")
	return string(data), err
}

func LicenseText() (string, error) {
	data, err := embeddedFiles.ReadFile("data/LICENSE")
	return string(data), err
}
