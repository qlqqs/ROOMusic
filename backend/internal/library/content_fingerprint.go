package library

import (
	"context"
	"io"

	"github.com/qlqq/roomusic/backend/internal/library/parser"
)

// ContentFingerprintOf 对调用方已经打开的来源文件计算与标签无关的内容指纹：extension 是带点的扩展名（如 ".m4a"），
// size 是文件大小。它与扫描时记录的指纹同源，所以播放可以对已打开的句柄重新取证，不再按路径访问文件；
// playback 不能直接依赖 parser，经这个公开入口使用同一份算法。没有可识别音频内容的文件返回空串。
func ContentFingerprintOf(ctx context.Context, extension string, reader io.ReaderAt, size int64) (string, error) {
	return parser.ContentFingerprintOf(ctx, extension, reader, size)
}
