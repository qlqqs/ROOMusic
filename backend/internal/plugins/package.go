package plugins

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
)

const MaxPackage = 32 << 20

type bundle struct {
	manifest  Manifest
	raw, wasm []byte
	hash      string
}

func checksum(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }
func limited(r io.Reader, n int64) ([]byte, error) {
	b, e := io.ReadAll(io.LimitReader(r, n+1))
	if int64(len(b)) > n {
		return nil, errors.New("插件文件超过大小限制")
	}
	return b, e
}

func readFile(path string, limit int64) ([]byte, error) {
	s, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !s.Mode().IsRegular() {
		return nil, errors.New("插件文件类型无效")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return limited(f, limit)
}

func unpack(data []byte) (bundle, error) {
	var b bundle
	if len(data) > MaxPackage {
		return b, errors.New("插件包大小超过 32 MiB 上限")
	}
	z, e := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if e != nil || len(z.File) != 2 {
		return b, errors.New("zip 须含 manifest.json 和 plugin.wasm")
	}
	for _, f := range z.File {
		var dst *[]byte
		var limit int64 = MaxPackage
		switch f.Name {
		case "manifest.json":
			dst = &b.raw
			limit = 64 << 10
		case "plugin.wasm":
			dst = &b.wasm
		default:
			return b, errors.New("zip 含有未知文件")
		}
		if !f.Mode().IsRegular() || *dst != nil || f.UncompressedSize64 > uint64(limit) {
			return b, errors.New("zip 条目重复、过大或类型无效")
		}
		r, e := f.Open()
		if e != nil {
			return b, e
		}
		*dst, e = limited(r, limit)
		r.Close()
		if e != nil {
			return b, e
		}
	}
	if len(b.raw)+len(b.wasm) > MaxPackage || len(b.wasm) == 0 {
		return b, errors.New("解包大小无效")
	}
	b.hash = checksum(data)
	b.manifest, e = parseManifest(b.raw)
	return b, e
}
