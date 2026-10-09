package derivation

import (
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// versionTimeout 是 `ffmpeg -version` 的超时。
	versionTimeout = 5 * time.Second
	// selfTestTimeout 是启动自检（完整的一次派生与核对）的超时。
	selfTestTimeout = 30 * time.Second
	// selfTestLimit 是自检输出的大小上限；素材只有约 10 KiB，输出远小于它。
	selfTestLimit = 4 << 20

	digestOutputLimit  = 128
	versionOutputLimit = 4096
	versionLineLimit   = 256
	stderrTailLimit    = 4096
)

// selfTestAsset 是 0.25 秒、16 bit/44.1 kHz 的立体声 ALAC（两个声道是不同频率的正弦，moov 在文件末尾），
// 由 scripts/gen-playback-fixtures.sh 生成。启动自检用它跑一遍完整的派生与核对，证明这个 ffmpeg 能按我们的命令行
// 无损转换，包括经 /dev/fd 读取源句柄、读取末尾的 moov。
//
//go:embed selftest.m4a
var selfTestAsset []byte

// Expect 是转换输出必须满足的音频参数，取自扫描记录；0 表示扫描没有取得，不核对。
type Expect struct {
	BitDepth   int
	SampleRate int
	Channels   int
}

// ConvertJob 是一次转换：Source 是已核对的只读源句柄，Output 是尚不存在的输出文件的绝对路径，
// Limit 是输出大小的硬上限（达到即视为超限）。
type ConvertJob struct {
	Source *os.File
	Output string
	Limit  int64
	Expect Expect
}

// Converter 把源句柄转换成经过核对的 FLAC。ffmpeg 适配器是生产实现，管理器的测试使用假实现。
// Convert 成功时 Output 是完整的 FLAC，参数与扫描记录一致，解码后的 PCM 与源相同；失败时返回 *FailedError，
// context 取消时返回 context 的错误。Convert 可以留下 Output 文件，由调用方删除。
type Converter interface {
	// Version 是转换器的版本串，参与缓存键：升级转换器后旧条目不再命中。
	Version() string
	Convert(ctx context.Context, job ConvertJob) error
}

// FFmpeg 是 ffmpeg 适配器。可执行文件由部署者安装；本包只按固定的参数数组启动它，不经 shell。
type FFmpeg struct {
	path    string
	version string
}

// ProbeFFmpeg 定位 ffmpeg（configuredPath 为空时在 PATH 中查找）、读取版本串，并用内置素材跑一遍完整的派生与核对。
// 任一步失败返回 *UnavailableError；workDir 是自检写临时文件的目录（缓存的 tmp/）。
func ProbeFFmpeg(ctx context.Context, configuredPath, workDir string) (*FFmpeg, error) {
	if !platformSupported {
		return nil, &UnavailableError{Reason: ReasonUnsupportedHost}
	}
	path, err := locateFFmpeg(configuredPath)
	if err != nil {
		return nil, &UnavailableError{Reason: ReasonNotFound, Err: err}
	}
	versionCtx, cancelVersion := context.WithTimeout(ctx, versionTimeout)
	defer cancelVersion()
	output, err := runFFmpeg(versionCtx, path, workDir, nil, versionOutputLimit, "-hide_banner", "-version")
	if err != nil {
		return nil, &UnavailableError{Reason: ReasonVersionFailed, Err: err}
	}
	version, _, _ := strings.Cut(strings.TrimSpace(output), "\n")
	version = strings.TrimSpace(version)
	if !strings.HasPrefix(version, "ffmpeg version") {
		return nil, &UnavailableError{Reason: ReasonVersionFailed, Err: errors.New("unexpected version output")}
	}
	if len(version) > versionLineLimit {
		version = version[:versionLineLimit]
	}
	ffmpeg := &FFmpeg{path: path, version: version}
	selfTestCtx, cancelSelfTest := context.WithTimeout(ctx, selfTestTimeout)
	defer cancelSelfTest()
	if err := ffmpeg.selfTest(selfTestCtx, workDir); err != nil {
		return nil, &UnavailableError{Reason: ReasonSelfTestFailed, Err: err}
	}
	return ffmpeg, nil
}

func locateFFmpeg(configuredPath string) (string, error) {
	if configuredPath != "" {
		return exec.LookPath(configuredPath)
	}
	return exec.LookPath("ffmpeg")
}

// selfTest 把内置素材写到 workDir，走与真实派生相同的转换与核对，结束后删除两个临时文件。
func (f *FFmpeg) selfTest(ctx context.Context, workDir string) error {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	stem := filepath.Join(workDir, "selftest-"+hex.EncodeToString(random[:]))
	sourcePath, outputPath := stem+".m4a", stem+partialSuffix
	defer os.Remove(sourcePath)
	defer os.Remove(outputPath)
	if err := os.WriteFile(sourcePath, selfTestAsset, 0o600); err != nil {
		return fmt.Errorf("write self-test asset: %w", err)
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("open self-test asset: %w", err)
	}
	defer source.Close()
	return f.Convert(ctx, ConvertJob{
		Source: source,
		Output: outputPath,
		Limit:  selfTestLimit,
		Expect: Expect{BitDepth: 16, SampleRate: 44100, Channels: 2},
	})
}

// Version 返回 ffmpeg 的版本串（`ffmpeg -version` 的第一行）。
func (f *FFmpeg) Version() string { return f.version }

// Convert 做两遍转换与核对：
//  1. 一次 ffmpeg 同时输出 FLAC 与“源解码后 PCM（32 位整数）的 MD5”；输入强制用 ALAC 解码器，不是 ALAC 就失败；
//  2. 核对输出未触及大小上限、STREAMINFO 的位深/采样率/声道与扫描记录一致；
//  3. 再解码输出文件取同一种 MD5，两个摘要必须相同。
//
// 摘要取 32 位整数，使 16 位与 24 位的比较都在完整精度上进行。源句柄经 fd 3 传给 ffmpeg 并以 /dev/fd/3 读取：
// 即使路径在检查之后被替换，读到的仍是已核对的那个 inode。
func (f *FFmpeg) Convert(ctx context.Context, job ConvertJob) error {
	if job.Source == nil || job.Output == "" || job.Limit <= 0 {
		return &FailedError{Category: FailureIO, Err: errors.New("invalid conversion job")}
	}
	directory := filepath.Dir(job.Output)
	output, err := runFFmpeg(ctx, f.path, directory, job.Source, digestOutputLimit, convertArguments(job)...)
	if err != nil {
		return err
	}
	// 没有摘要行说明 ffmpeg 没有做完工作（例如输出文件已存在时 -n 让它直接退出，退出码仍可能是 0）。
	sourceDigest, err := parseDigest(output)
	if err != nil {
		return &FailedError{Category: FailureFFmpegExit, Err: fmt.Errorf("source digest: %w", err)}
	}
	info, err := os.Stat(job.Output)
	if err != nil {
		return &FailedError{Category: FailureIO, Err: fmt.Errorf("stat derived output: %w", err)}
	}
	// -fs 只让 ffmpeg 在达到上限后停止写入、不报错，所以输出达到上限就是被截断了。
	if info.Size() >= job.Limit {
		return &FailedError{Category: FailureOutputLimit, Err: errors.New("derived output reached the size limit")}
	}
	if err := verifyStreamInfo(job.Output, job.Expect); err != nil {
		return &FailedError{Category: FailureVerifyMismatch, Err: err}
	}
	output, err = runFFmpeg(ctx, f.path, directory, nil, digestOutputLimit, verifyArguments(job.Output)...)
	if err != nil {
		return err
	}
	outputDigest, err := parseDigest(output)
	if err != nil {
		return &FailedError{Category: FailureFFmpegExit, Err: fmt.Errorf("output digest: %w", err)}
	}
	if outputDigest != sourceDigest {
		return &FailedError{Category: FailureVerifyMismatch, Err: errors.New("decoded output differs from the decoded source")}
	}
	return nil
}

func verifyStreamInfo(path string, expect Expect) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open derived output: %w", err)
	}
	defer file.Close()
	info, err := readFLACStreamInfo(file)
	if err != nil {
		return err
	}
	return info.verify(expect)
}

// convertArguments 是第一遍的固定参数数组：只允许 file 协议；丢弃元数据与章节，只取第一路音频；
// 输出一侧 bitexact 使同一输入得到相同字节；-n 保证不覆盖已有文件；第二个输出把解码后的 PCM 摘要写到标准输出。
func convertArguments(job ConvertJob) []string {
	return []string{
		"-nostdin", "-hide_banner", "-loglevel", "error", "-xerror", "-n", "-threads", "1",
		"-protocol_whitelist", "file", "-c:a", "alac", "-f", "mov", "-i", "file:/dev/fd/3",
		"-map", "0:a:0", "-map_metadata", "-1", "-map_chapters", "-1",
		"-fflags", "+bitexact", "-flags:a", "+bitexact",
		"-c:a", "flac", "-compression_level", "5", "-fs", strconv.FormatInt(job.Limit, 10), "-f", "flac", "file:" + job.Output,
		"-map", "0:a:0", "-c:a", "pcm_s32le", "-f", "md5", "pipe:1",
	}
}

// verifyArguments 是第二遍的固定参数数组：解码输出文件并输出同一种摘要。
func verifyArguments(output string) []string {
	return []string{
		"-nostdin", "-hide_banner", "-loglevel", "error", "-xerror", "-threads", "1",
		"-protocol_whitelist", "file", "-f", "flac", "-i", "file:" + output,
		"-map", "0:a:0", "-c:a", "pcm_s32le", "-f", "md5", "pipe:1",
	}
}

// parseDigest 解析 md5 封装器写出的 "MD5=<32 位十六进制>" 行。
func parseDigest(output string) (string, error) {
	digest, found := strings.CutPrefix(strings.TrimSpace(output), "MD5=")
	if !found || len(digest) != 32 {
		return "", errors.New("missing MD5 line")
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return "", errors.New("malformed MD5 line")
	}
	return strings.ToLower(digest), nil
}

// runFFmpeg 启动一次 ffmpeg 并等待结束，返回标准输出（至多 stdoutLimit 字节）。source 非空时作为 fd 3 传入。
// 环境只给 LC_ALL=C，工作目录是 dir，标准输入为空。context 结束时终止整个进程组；此时返回 context 的错误，
// 由调用方区分超时与取消。ffmpeg 退出码非零返回 *FailedError（stderr 末尾只放在 Stderr 字段）。
func runFFmpeg(ctx context.Context, path, dir string, source *os.File, stdoutLimit int, arguments ...string) (string, error) {
	command := exec.CommandContext(ctx, path, arguments...)
	command.Dir = dir
	command.Env = []string{"LC_ALL=C"}
	if source != nil {
		command.ExtraFiles = []*os.File{source}
	}
	stdout := &tailBuffer{limit: stdoutLimit, head: true}
	stderr := &tailBuffer{limit: stderrTailLimit}
	command.Stdout, command.Stderr = stdout, stderr
	configureProcess(command)
	err := command.Run()
	if err == nil {
		return stdout.String(), nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", ctxErr
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return "", &FailedError{Category: FailureFFmpegExit, Err: fmt.Errorf("ffmpeg exited with status %d", exit.ExitCode()), Stderr: stderr.String()}
	}
	return "", &FailedError{Category: FailureIO, Err: fmt.Errorf("run ffmpeg: %w", err), Stderr: stderr.String()}
}

// tailBuffer 是有界的输出缓冲：Write 总是报告全部写入（不让 ffmpeg 因管道阻塞或 EPIPE 失败），
// head 为真时保留开头的 limit 字节（标准输出），否则保留末尾的 limit 字节（stderr）。
type tailBuffer struct {
	limit int
	head  bool
	data  []byte
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	written := len(p)
	if b.head {
		if room := b.limit - len(b.data); room > 0 {
			b.data = append(b.data, p[:min(room, len(p))]...)
		}
		return written, nil
	}
	b.data = append(b.data, p...)
	if len(b.data) > b.limit {
		b.data = bytes.Clone(b.data[len(b.data)-b.limit:])
	}
	return written, nil
}

func (b *tailBuffer) String() string { return string(b.data) }
