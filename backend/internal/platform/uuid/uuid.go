// Package uuid 生成并校验 REST 路由与数据库列使用的 UUID 文本。
package uuid

import (
	"crypto/rand"
	"encoding/hex"
)

// New 返回随机（版本 4）UUID 的小写文本。系统随机源失败时 panic，调用方无法继续生成身份。
func New() string {
	identifierBytes := make([]byte, 16)
	if _, err := rand.Read(identifierBytes); err != nil {
		panic(err)
	}
	identifierBytes[6] = (identifierBytes[6] & 0x0f) | 0x40
	identifierBytes[8] = (identifierBytes[8] & 0x3f) | 0x80
	return hex.EncodeToString(identifierBytes[:4]) + "-" + hex.EncodeToString(identifierBytes[4:6]) + "-" + hex.EncodeToString(identifierBytes[6:8]) + "-" + hex.EncodeToString(identifierBytes[8:10]) + "-" + hex.EncodeToString(identifierBytes[10:])
}

// Valid 报告 value 是否具有 UUID 文本的形状：8-4-4-4-12 的十六进制，大小写均可。
// 它只检查形状，不检查版本位，用于在数据库类型转换前拒绝畸形输入。
func Valid(value string) bool {
	if len(value) != 36 {
		return false
	}
	for index := 0; index < len(value); index++ {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			if value[index] != '-' {
				return false
			}
			continue
		}
		character := value[index]
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') || (character >= 'A' && character <= 'F')) {
			return false
		}
	}
	return true
}
