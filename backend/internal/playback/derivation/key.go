package derivation

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"
)

const (
	// keyDomain 与 policyALACFLAC 参与缓存键：改变键的组成或转换策略必须升版本号，旧条目随之不再命中。
	keyDomain      = "roomusic-derivation-v1"
	policyALACFLAC = "alac-flac-lossless-v1"
)

// SourceVersion 是源文件在扫描时的版本事实。缓存键由它与转换器版本决定；
// BitDepth、SampleRate 与 Channels 只用于核对转换结果（0 表示扫描没有取得，不核对），不参与缓存键。
type SourceVersion struct {
	// Policy 是格式表指定的转换策略版本；ALAC 沿用 alac-flac-lossless-v1。
	Policy             string
	ContentFingerprint string
	Size               int64
	ModifiedAt         time.Time
	SourceKind         string
	Codec              string

	BitDepth   int
	SampleRate int
	Channels   int
}

// deriveKey 返回缓存键：对各字段加长度前缀后取 SHA-256 的十六进制（64 位）。
// 键不含曲目 ID：内容相同的两个来源共用派生文件，每次请求仍各自鉴权并核对来源。
func deriveKey(converterVersion string, version SourceVersion) string {
	hash := sha256.New()
	for _, field := range []string{
		keyDomain,
		version.Policy,
		converterVersion,
		version.ContentFingerprint,
		strconv.FormatInt(version.Size, 10),
		strconv.FormatInt(version.ModifiedAt.UTC().Truncate(time.Microsecond).UnixMicro(), 10),
		version.SourceKind,
		version.Codec,
	} {
		_, _ = fmt.Fprintf(hash, "%d:%s\n", len(field), field)
	}
	return hex.EncodeToString(hash.Sum(nil))
}
