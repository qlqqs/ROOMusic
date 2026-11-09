package parser

import (
	"context"
	"crypto/sha256"
	"io"

	"github.com/qlqq/roomusic/backend/internal/playback/dsdio"
)

const (
	fingerprintDSFData = "dsf-data-v1"
	fingerprintDFFDSD  = "dff-dsd-v1"
)

func dsfFingerprint(ctx context.Context, reader io.ReaderAt, size int64) (string, error) {
	return dsdFingerprint(ctx, reader, size, dsdio.DSF, fingerprintDSFData)
}

func dffFingerprint(ctx context.Context, reader io.ReaderAt, size int64) (string, error) {
	return dsdFingerprint(ctx, reader, size, dsdio.DFF, fingerprintDFFDSD)
}

func dsdFingerprint(ctx context.Context, reader io.ReaderAt, size int64, container dsdio.Container, kind string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// 容器遍历同样响应取消；标签内容不读入内存，音频沿用 hashSection 的分块读取。
	layout, err := probeDSDContainer(dsdContextReaderAt{ctx, reader}, size, container)
	if err != nil {
		return "", err
	}
	if layout.DataStart == layout.DataEnd {
		return "", nil
	}
	hash := sha256.New()
	if err := hashSection(ctx, hash, reader, layout.DataStart, layout.DataEnd); err != nil {
		return "", err
	}
	return formatFingerprint(kind, hash.Sum(nil)), nil
}

type dsdContextReaderAt struct {
	ctx    context.Context
	reader io.ReaderAt
}

func (r dsdContextReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.ReadAt(p, off)
}
