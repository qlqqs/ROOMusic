package ffmpeg

import (
	"bytes"
	"context"
	"crypto/md5"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

// selfTestAsset 是旧派生自检使用的合成立体声 ALAC，16 bit / 44.1 kHz / 0.25 秒。
// moov 在末尾，因此同时验证安全文件描述符输入的可定位性。
//
//go:embed testdata/selftest.m4a
var selfTestAsset []byte

const selfTestLimit = 4 << 20

func (t *Tool) probe(ctx context.Context, limit int64, args ...string) ([]byte, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	prefix := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-threads", "1"}
	return t.capture(probeCtx, StartOptions{Args: append(prefix, args...)}, nil, limit)
}

// capture 并行写入和读取管道，避免自检的 PCM 输入与 FLAC 输出互相阻塞。
func (t *Tool) capture(ctx context.Context, options StartOptions, input []byte, limit int64) ([]byte, error) {
	options.Stdin = input != nil
	p, err := t.Start(ctx, options)
	if err != nil {
		return nil, err
	}
	defer p.Close()
	var written chan error
	if input != nil {
		written = make(chan error, 1)
		go func() {
			_, writeErr := io.Copy(p, bytes.NewReader(input))
			closeErr := p.CloseInput()
			written <- errors.Join(writeErr, closeErr)
		}()
	}
	output, readErr := io.ReadAll(io.LimitReader(p, limit+1))
	if int64(len(output)) > limit {
		readErr = errors.New("ffmpeg output exceeded limit")
	}
	if readErr != nil {
		_ = p.Close()
		if written != nil {
			<-written
		}
		return nil, readErr
	}
	if written != nil {
		if err := <-written; err != nil {
			return nil, err
		}
	}
	if err := p.Wait(); err != nil {
		return nil, err
	}
	return output, nil
}

func (t *Tool) selfTest(ctx context.Context) error {
	if !t.HasDecoder("alac") || !t.HasDecoder("flac") || !t.HasEncoder("flac") {
		return errors.New("ALAC/FLAC self-test codecs unavailable")
	}
	source, err := os.CreateTemp("", "roomusic-render-selftest-*.m4a")
	if err != nil {
		return fmt.Errorf("create self-test source: %w", err)
	}
	defer os.Remove(source.Name())
	defer source.Close()
	if _, err = source.Write(selfTestAsset); err != nil {
		return fmt.Errorf("write self-test source: %w", err)
	}
	if _, err = source.Seek(0, io.SeekStart); err != nil {
		return err
	}
	prefix := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-xerror", "-threads", "1", "-protocol_whitelist", "file,pipe"}
	args := append(append([]string{}, prefix...), "-f", "mov", "-c:a", "alac", "-i", "file:/dev/fd/3", "-map", "0:a:0", "-vn", "-ar", "44100", "-ac", "2", "-f", "s16le", "pipe:1")
	pcm, err := t.capture(ctx, StartOptions{Args: args, Source: source, ReadTimeout: true}, nil, selfTestLimit)
	if err != nil {
		return fmt.Errorf("decode self-test source: %w", err)
	}
	if len(pcm) == 0 || len(pcm)%4 != 0 {
		return errors.New("invalid self-test PCM length")
	}
	args = append(append([]string{}, prefix...), "-f", "s16le", "-ar", "44100", "-ac", "2", "-i", "pipe:0", "-c:a", "flac", "-compression_level", "5", "-flush_packets", "1", "-f", "flac", "pipe:1")
	encoded, err := t.capture(ctx, StartOptions{Args: args}, pcm, selfTestLimit)
	if err != nil {
		return fmt.Errorf("encode self-test FLAC: %w", err)
	}
	if len(encoded) == 0 {
		return errors.New("empty self-test FLAC")
	}
	args = append(append([]string{}, prefix...), "-f", "flac", "-i", "pipe:0", "-map", "0:a:0", "-f", "s16le", "pipe:1")
	decoded, err := t.capture(ctx, StartOptions{Args: args, ReadTimeout: true}, encoded, selfTestLimit)
	if err != nil {
		return fmt.Errorf("decode self-test FLAC: %w", err)
	}
	// MD5 只用于无损样本一致性，不承担安全散列职责。
	if len(decoded) != len(pcm) || md5.Sum(decoded) != md5.Sum(pcm) {
		return errors.New("self-test PCM digest mismatch")
	}
	return nil
}
