package parser

import (
	"fmt"
	"strings"
)

// CommonVersion 在观察结构或共用解析规则改变时递增，使所有格式的缓存失效。
const CommonVersion = 2

// 只改变某一格式的读取规则时递增对应版本；新增格式从 1 开始。
var formatVersions = map[string]int{
	".ape":  1,
	".wv":   1,
	".aif":  1,
	".aiff": 1,
	".flac": 1,
	".mp3":  1,
	".ogg":  1,
	".opus": 1,
	".wav":  1,
	".m4a":  1,
}

// CacheVersion 返回扩展名的解析缓存版本；未支持的格式不缓存。
func CacheVersion(extension string) string {
	version, ok := formatVersions[strings.ToLower(extension)]
	if !ok {
		return ""
	}
	return fmt.Sprintf("c%d.f%d", CommonVersion, version)
}
