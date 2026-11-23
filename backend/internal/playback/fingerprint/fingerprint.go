// Package fingerprint 用已核对的文件句柄或 PCM 流计算 Chromaprint 指纹。
package fingerprint

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

const Version int16 = 1

var ErrTooShort = errors.New("audio is too short for a fingerprint")

type Fingerprinter struct {
	ffmpeg, fpcalc string
	fpcalcVersion  string
}

// FPCalcVersion 返回自检读到的 fpcalc 版本行。
func (f *Fingerprinter) FPCalcVersion() string { return f.fpcalcVersion }

type UnavailableError struct {
	Reason string
	Err    error
}

func (e *UnavailableError) Error() string { return "fingerprint unavailable: " + e.Reason }
func (e *UnavailableError) Unwrap() error { return e.Err }

// Open 探测两个程序的版本，并将十秒合成 PCM 送入完整管道自检。
func Open(ctx context.Context, ffmpegPath, fpcalcPath string) (*Fingerprinter, error) {
	if !platformSupported {
		return nil, &UnavailableError{Reason: "unsupported_platform"}
	}
	f := &Fingerprinter{}
	for _, tool := range []struct {
		name, path, prefix string
		dest               *string
	}{
		{"ffmpeg", ffmpegPath, "ffmpeg version ", &f.ffmpeg},
		{"fpcalc", fpcalcPath, "fpcalc version 1.6.", &f.fpcalc},
	} {
		path := tool.path
		if path == "" {
			path = tool.name
		}
		found, err := exec.LookPath(path)
		if err != nil {
			return nil, &UnavailableError{Reason: "not_found", Err: err}
		}
		*tool.dest = found
		probe, cancel := context.WithTimeout(ctx, 5*time.Second)
		command := subprocess(probe, found, "-version")
		output, err := command.Output()
		cancel()
		if err != nil || !strings.HasPrefix(string(output), tool.prefix) {
			return nil, &UnavailableError{Reason: "version_failed", Err: err}
		}
		if tool.name == "fpcalc" {
			version, _, _ := strings.Cut(strings.TrimSpace(string(output)), "\n")
			f.fpcalcVersion = version[:min(len(version), 256)]
		}
	}
	const rate = 11025
	data := make([]byte, 10*rate*2)
	for frame := 0; frame < len(data)/2; frame++ {
		value := int16(8000 * math.Sin(2*math.Pi*440*float64(frame)/rate))
		data[frame*2] = byte(value)
		data[frame*2+1] = byte(value >> 8)
	}
	selfTest, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := f.calculate(selfTest, nil, bytes.NewReader(data), []string{"-f", "s16le", "-ar", "11025", "-ac", "1", "-t", "120", "-i", "pipe:0"}); err != nil {
		return nil, &UnavailableError{Reason: "selftest_failed", Err: err}
	}
	return f, nil
}

// File 从可定位的描述符读取普通曲目或给定的 75 Hz CUE 区间。
func (f *Fingerprinter) File(ctx context.Context, source *os.File, cueIndex, cueEnd *int32) (string, error) {
	var input []string
	duration := "120"
	if cueIndex != nil {
		input = append(input, "-ss", seconds75(*cueIndex))
		if cueEnd != nil && *cueEnd-*cueIndex < 120*75 {
			duration = seconds75(*cueEnd - *cueIndex)
		}
	}
	input = append(input, "-t", duration, "-i", "file:/dev/fd/3")
	return f.calculate(ctx, source, nil, input)
}

func seconds75(frames int32) string { return strconv.FormatFloat(float64(frames)/75, 'f', 6, 64) }

// PCM 接收已经截取曲目区间的 f32le 流；关闭流会解除转换器的读阻塞。
func (f *Fingerprinter) PCM(ctx context.Context, pcm io.ReadCloser, sampleRate, channels int) (string, error) {
	input := []string{"-f", "f32le", "-ar", strconv.Itoa(sampleRate), "-ac", strconv.Itoa(channels), "-t", "120", "-i", "pipe:0"}
	// 在 PCM 帧边界截断，让输入转换器与 ffmpeg 在同一位置自然读完。
	limited := struct {
		io.Reader
		io.Closer
	}{io.LimitReader(pcm, int64(sampleRate)*int64(channels)*4*120), pcm}
	return f.calculate(ctx, nil, limited, input)
}

func subprocess(ctx context.Context, path string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = []string{"LC_ALL=C"}
	configureProcess(cmd)
	return cmd
}

func start(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	setLowPriority(cmd.Process.Pid)
	setIdleIOPriority(cmd.Process.Pid)
	return nil
}

func (f *Fingerprinter) calculate(parent context.Context, source *os.File, pcm io.Reader, input []string) (string, error) {
	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()
	closePCM := sync.OnceFunc(func() {
		if stream, ok := pcm.(io.Closer); ok {
			_ = stream.Close()
		}
	})
	stop := context.AfterFunc(ctx, closePCM)
	defer func() {
		stop()
		closePCM()
	}()

	args := append([]string{"-nostdin", "-hide_banner", "-loglevel", "error", "-protocol_whitelist", "file,pipe", "-threads", "1"}, input...)
	args = append(args, "-map", "0:a:0", "-ac", "1", "-ar", "11025", "-f", "s16le", "pipe:1")
	decode := subprocess(ctx, f.ffmpeg, args...)
	if source != nil {
		decode.ExtraFiles = []*os.File{source}
	}
	decode.Stdin = pcm
	var decodeLog, printLog tail
	decode.Stderr = &decodeLog
	pipe, err := decode.StdoutPipe()
	if err != nil {
		return "", err
	}
	defer pipe.Close()

	print := subprocess(ctx, f.fpcalc, "-format", "s16le", "-rate", "11025", "-channels", "1", "-length", "120", "-json", "-")
	print.Stdin = pipe
	var output bytes.Buffer
	print.Stdout, print.Stderr = &output, &printLog
	if err := start(decode); err != nil {
		return "", err
	}
	if err := start(print); err != nil {
		cancel()
		_ = decode.Wait()
		return "", err
	}
	// 子进程持有独立的读端。父进程关闭自己的副本，指纹程序退出时解码器能收到 EPIPE。
	_ = pipe.Close()
	printErr := print.Wait()
	closePCM()
	decodeErr := decode.Wait()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if decodeErr != nil {
		return "", fmt.Errorf("ffmpeg: %w: %s", decodeErr, decodeLog)
	}
	if printErr != nil {
		if strings.Contains(string(printLog), "ERROR: Empty fingerprint") {
			return "", ErrTooShort
		}
		return "", fmt.Errorf("fpcalc: %w: %s", printErr, printLog)
	}
	var result struct{ Fingerprint string }
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		return "", err
	}
	if result.Fingerprint == "" {
		return "", ErrTooShort
	}
	if len(result.Fingerprint) > 65536 {
		return "", errors.New("fpcalc fingerprint exceeds size limit")
	}
	return result.Fingerprint, nil
}

type tail []byte

func (b *tail) Write(p []byte) (int, error) {
	const limit = 4096
	n := len(p)
	if n >= limit {
		*b = append((*b)[:0], p[n-limit:]...)
	} else {
		drop := max(0, len(*b)+n-limit)
		*b = append((*b)[drop:], p...)
	}
	return n, nil
}
