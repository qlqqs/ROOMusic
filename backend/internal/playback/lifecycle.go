package playback

import (
	"context"
	"io"
)

// LifecycleReader 包装已打开的音频文件，供 http.ServeContent 读取；应用生命周期结束后不再交付字节。
// 它不强制打断底层阻塞的 Read，但会在其返回后再次检查取消，丢弃该次读取的字节。
// 它只在处理请求的 goroutine 里使用，不是并发安全的；调用方仍负责关闭文件。
type LifecycleReader struct {
	file      io.ReadSeeker
	lifecycle context.Context
	aborted   bool
}

// NewLifecycleReader 返回仅受应用生命周期约束的读取器。
func NewLifecycleReader(file io.ReadSeeker, lifecycle context.Context) *LifecycleReader {
	return &LifecycleReader{file: file, lifecycle: lifecycle}
}

// Read 实现 io.Reader；取消错误使 ServeContent 停止写出，不向音频尾部追加错误正文。
func (reader *LifecycleReader) Read(buffer []byte) (int, error) {
	if err := reader.lifecycle.Err(); err != nil {
		reader.aborted = true
		return 0, err
	}
	n, err := reader.file.Read(buffer)
	if canceled := reader.lifecycle.Err(); canceled != nil {
		reader.aborted = true
		return 0, canceled
	}
	return n, err
}

// Seek 实现 io.Seeker，直接透传定位；取消在读取时处理。
func (reader *LifecycleReader) Seek(offset int64, whence int) (int64, error) {
	return reader.file.Seek(offset, whence)
}

// Aborted 报告读取是否因停机中止，供 ServeContent 返回后记录一次日志。
func (reader *LifecycleReader) Aborted() bool { return reader.aborted }
