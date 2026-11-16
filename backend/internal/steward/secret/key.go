package secret

import (
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
)

func loadKey(path string, create bool) ([]byte, error) {
	key, err := os.ReadFile(path)
	if err == nil {
		if len(key) != 32 {
			return nil, errors.New("实例密钥长度必须为 32 字节")
		}
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("读取实例密钥失败")
	}
	if !create {
		return nil, ErrCannotOpen
	}
	key = make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, errors.New("生成实例密钥失败")
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, errors.New("创建实例密钥目录失败")
	}
	file, err := os.CreateTemp(directory, ".steward-key-*")
	if err != nil {
		return nil, errors.New("创建实例密钥临时文件失败")
	}
	defer os.Remove(file.Name())
	// CreateTemp 以 0600 创建文件；先完整写入并关闭，再原子发布。
	if _, err := file.Write(key); err != nil {
		file.Close()
		return nil, errors.New("写入实例密钥失败")
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return nil, errors.New("同步实例密钥失败")
	}
	if err := file.Close(); err != nil {
		return nil, errors.New("关闭实例密钥文件失败")
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return nil, errors.New("保存实例密钥失败")
	}
	return key, nil
}
