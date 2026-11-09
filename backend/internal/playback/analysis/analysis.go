// Package analysis 提供不依赖数据库的 ffmpeg 音频分析器。
package analysis

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	Version           int16 = 2
	TargetLUFS              = -14.0
	PeakMarginDB            = 0.5
	probeTimeout            = 5 * time.Second
	selfTestTimeout         = 30 * time.Second
	trackTimeout            = 10 * time.Minute
	stderrLimit             = 64 << 10
	windowSamples           = 80
	maxWaveformPoints       = 1000
)

type Result struct {
	LoudnessRangeLU, SamplePeakDBFS                 *float64
	IntegratedLUFS, TruePeakDBTP                    *float64
	DurationMS, LeadingSilenceMS, TrailingSilenceMS *int32
	Waveform                                        []byte
}

// PCMInput 描述 stdin 上无文件头的交错 PCM；目前只接收 f32le。
type PCMInput struct {
	Format     string
	SampleRate int
	Channels   int
}

type Analyzer struct{ path, version string }

type UnavailableError struct {
	Reason string
	Err    error
}

func (e *UnavailableError) Error() string { return "audio analysis unavailable: " + e.Reason }
func (e *UnavailableError) Unwrap() error { return e.Err }

type Failure struct {
	Category string
	Err      error
}

func (e *Failure) Error() string { return e.Category + ": " + e.Err.Error() }
func (e *Failure) Unwrap() error { return e.Err }

var (
	integratedPattern    = regexp.MustCompile(`(?m)Integrated loudness:\s*I:\s*([-+]?inf|[-+]?[0-9]+(?:\.[0-9]+)?)\s*LUFS`)
	peakPattern          = regexp.MustCompile(`(?m)True peak:\s*Peak:\s*([-+]?inf|[-+]?[0-9]+(?:\.[0-9]+)?)\s*dBFS`)
	loudnessRangePattern = regexp.MustCompile(`(?m)Loudness range:\s*LRA:\s*([-+]?[0-9]+(?:\.[0-9]+)?)\s*LU`)
	samplePeakPattern    = regexp.MustCompile(`(?m)Sample peak:\s*Peak:\s*([-+]?inf|[-+]?[0-9]+(?:\.[0-9]+)?)\s*dBFS`)
)

// Open 定位 ffmpeg、读取版本并以规定的静音+正弦 WAV 自检分析链路。
func Open(ctx context.Context, configuredPath string) (*Analyzer, error) {
	if !platformSupported {
		return nil, &UnavailableError{Reason: "unsupported_platform"}
	}
	path := strings.TrimSpace(configuredPath)
	var err error
	if path == "" {
		path, err = exec.LookPath("ffmpeg")
	} else {
		path, err = exec.LookPath(path)
	}
	if err != nil {
		return nil, &UnavailableError{Reason: "not_found", Err: err}
	}
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	output, err := runText(probeCtx, path, "-hide_banner", "-version")
	if err != nil {
		return nil, &UnavailableError{Reason: "version_failed", Err: err}
	}
	version, _, _ := strings.Cut(strings.TrimSpace(output), "\n")
	if !strings.HasPrefix(version, "ffmpeg version") {
		return nil, &UnavailableError{Reason: "version_failed", Err: errors.New("unexpected version output")}
	}
	if len(version) > 256 {
		version = version[:256]
	}
	analyzer := &Analyzer{path: path, version: version}
	selfCtx, cancelSelf := context.WithTimeout(ctx, selfTestTimeout)
	defer cancelSelf()
	result, err := analyzer.analyzeInput(selfCtx, nil, bytes.NewReader(selfTestWAV()), nil, nil, nil)
	if err != nil {
		return nil, &UnavailableError{Reason: "selftest_failed", Err: err}
	}
	if result.LoudnessRangeLU == nil || result.IntegratedLUFS == nil || math.Abs(*result.IntegratedLUFS-(-23.3)) > 1.5 || result.LeadingSilenceMS == nil || abs32(*result.LeadingSilenceMS-500) > 30 {
		return nil, &UnavailableError{Reason: "selftest_failed", Err: errors.New("self-test measurements are outside tolerance")}
	}
	return analyzer, nil
}

func (a *Analyzer) Version() int16        { return Version }
func (a *Analyzer) FFmpegVersion() string { return a.version }

// Analyze 从 fd 3 读取已验证文件；CUE 区间以 75Hz 帧为单位。
func (a *Analyzer) Analyze(ctx context.Context, source *os.File, cueIndex, cueEnd *int32) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, trackTimeout)
	defer cancel()
	return a.analyzeInput(ctx, source, nil, nil, cueIndex, cueEnd)
}

// AnalyzePCM 分析已按曲目区间截取的 PCM，不再应用 CUE 的 -ss/-t。
// 若 stdin 实现 io.Closer，本方法在取消、启动失败、输出结束时关闭它；
// 流式来源必须提供可并发调用的 Close，以取消转换并解除 Read 阻塞。
func (a *Analyzer) AnalyzePCM(ctx context.Context, stdin io.Reader, input PCMInput) (Result, error) {
	return a.analyzeInput(ctx, nil, stdin, &input, nil, nil)
}

func (a *Analyzer) analyzeInput(ctx context.Context, source *os.File, stdin io.Reader, inputFormat *PCMInput, cueIndex, cueEnd *int32) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, trackTimeout)
	defer cancel()
	closeInput := sync.OnceFunc(func() {
		if closer, ok := stdin.(io.Closer); ok {
			_ = closer.Close()
		}
	})
	stopClose := context.AfterFunc(ctx, closeInput)
	defer func() {
		stopClose()
		closeInput()
	}()
	if inputFormat != nil && (stdin == nil || inputFormat.Format != "f32le" || inputFormat.SampleRate <= 0 || inputFormat.Channels < 1 || inputFormat.Channels > 32) {
		return Result{}, &Failure{Category: "parse", Err: errors.New("invalid PCM input description")}
	}
	args := []string{"-nostdin", "-hide_banner", "-nostats", "-loglevel", "info", "-threads", "1", "-protocol_whitelist", "file,pipe"}
	if cueIndex != nil {
		args = append(args, "-ss", seconds75(*cueIndex))
		if cueEnd != nil {
			args = append(args, "-t", seconds75(*cueEnd-*cueIndex))
		}
	}
	input := "file:/dev/fd/3"
	if stdin != nil {
		input = "pipe:0"
	}
	if inputFormat != nil {
		args = append(args, "-f", inputFormat.Format, "-ar", strconv.Itoa(inputFormat.SampleRate), "-ac", strconv.Itoa(inputFormat.Channels))
	}
	args = append(args, "-i", input, "-filter_complex", "[0:a:0]asplit=2[l][w];[l]ebur128=peak=sample+true:framelog=quiet,anullsink;[w]aresample=8000,aformat=sample_fmts=s16:channel_layouts=mono[pcm]", "-map", "[pcm]", "-f", "s16le", "pipe:1")
	cmd := exec.CommandContext(ctx, a.path, args...)
	cmd.Env = []string{"LC_ALL=C"}
	if source != nil {
		cmd.ExtraFiles = []*os.File{source}
	}
	if stdin != nil {
		cmd.Stdin = stdin
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{}, &Failure{Category: "ffmpeg_exit", Err: err}
	}
	stderr := newTailBuffer(stderrLimit)
	cmd.Stderr = stderr
	configureProcess(cmd)
	if err := cmd.Start(); err != nil {
		return Result{}, &Failure{Category: "ffmpeg_exit", Err: err}
	}
	setLowPriority(cmd.Process.Pid)
	setIdleIOPriority(cmd.Process.Pid)
	peaks, readErr := readWindowPeaks(stdout)
	if readErr != nil {
		_ = cmd.Process.Kill()
	}
	// stdout 提前结束也必须先解除 stdin 的生产者阻塞，再等待 exec 的复制协程。
	closeInput()
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return Result{}, &Failure{Category: "timeout", Err: ctx.Err()}
		}
		return Result{}, ctx.Err()
	}
	if readErr != nil {
		return Result{}, &Failure{Category: "ffmpeg_exit", Err: readErr}
	}
	if waitErr != nil {
		return Result{}, &Failure{Category: "ffmpeg_exit", Err: fmt.Errorf("ffmpeg exited: %w: %s", waitErr, stderr.String())}
	}
	integrated, peak, loudnessRange, samplePeak, err := parseSummary(stderr.String())
	if err != nil {
		return Result{}, &Failure{Category: "parse", Err: err}
	}
	if len(peaks) == 0 {
		return Result{}, &Failure{Category: "parse", Err: errors.New("ffmpeg produced no PCM samples")}
	}
	result := measure(peaks, integrated, peak)
	result.LoudnessRangeLU = loudnessRange
	result.SamplePeakDBFS = samplePeak
	return result, nil
}

func parseSummary(stderr string) (integrated, peak, loudnessRange, samplePeak *float64, err error) {
	integratedMatch := integratedPattern.FindStringSubmatch(stderr)
	peakMatch := peakPattern.FindStringSubmatch(stderr)
	if len(integratedMatch) != 2 || len(peakMatch) != 2 {
		return nil, nil, nil, nil, errors.New("ebur128 summary is missing")
	}
	integrated, err = parseDB(integratedMatch[1])
	if err != nil {
		return nil, nil, nil, nil, err
	}
	peak, err = parseDB(peakMatch[1])
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if math.IsInf(*integrated, -1) || *integrated <= -70 {
		integrated = nil
	}
	if math.IsInf(*peak, -1) {
		peak = nil
	}
	if match := loudnessRangePattern.FindStringSubmatch(stderr); len(match) == 2 && integrated != nil {
		loudnessRange, err = parseDB(match[1])
		if err != nil {
			return nil, nil, nil, nil, err
		}
	}
	if match := samplePeakPattern.FindStringSubmatch(stderr); len(match) == 2 {
		samplePeak, err = parseDB(match[1])
		if err != nil {
			return nil, nil, nil, nil, err
		}
		if math.IsInf(*samplePeak, -1) {
			samplePeak = nil
		}
	}
	return integrated, peak, loudnessRange, samplePeak, nil
}

func parseDB(value string) (*float64, error) {
	if strings.EqualFold(value, "-inf") {
		v := math.Inf(-1)
		return &v, nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return nil, errors.New("invalid ebur128 value")
	}
	return &parsed, nil
}

func readWindowPeaks(reader io.Reader) ([]uint16, error) {
	buf := make([]byte, windowSamples*2)
	var peaks []uint16
	for {
		n, err := io.ReadFull(reader, buf)
		if n > 0 {
			var maximum uint16
			for i := 0; i+1 < n; i += 2 {
				sample := int16(uint16(buf[i]) | uint16(buf[i+1])<<8)
				value := uint16(abs16(sample))
				if value > maximum {
					maximum = value
				}
			}
			peaks = append(peaks, maximum)
		}
		if err == io.EOF {
			return peaks, nil
		}
		if err == io.ErrUnexpectedEOF {
			return peaks, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

func measure(windows []uint16, integrated, peak *float64) Result {
	result := Result{IntegratedLUFS: integrated, TruePeakDBTP: peak}
	if len(windows) == 0 {
		return result
	}
	duration := int32(len(windows) * 10)
	result.DurationMS = &duration
	first, last := -1, -1
	for i, v := range windows {
		if v > 33 {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first >= 0 {
		leading := int32(first * 10)
		trailing := int32((last + 1) * 10)
		result.LeadingSilenceMS = &leading
		result.TrailingSilenceMS = &trailing
	}
	count := len(windows)
	points := count
	if points > maxWaveformPoints {
		points = maxWaveformPoints
	}
	waveform := make([]byte, points)
	for i := 0; i < points; i++ {
		start := i * count / points
		end := (i + 1) * count / points
		var max uint16
		for _, value := range windows[start:end] {
			if value > max {
				max = value
			}
		}
		waveform[i] = byte(math.Round(float64(max) / 32768 * 255))
	}
	result.Waveform = waveform
	return result
}

func seconds75(frames int32) string { return strconv.FormatFloat(float64(frames)/75, 'f', 6, 64) }
func abs16(value int16) int {
	if value < 0 {
		return -int(value)
	}
	return int(value)
}
func abs32(value int32) int32 {
	if value < 0 {
		return -value
	}
	return value
}

func runText(ctx context.Context, path string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = []string{"LC_ALL=C"}
	configureProcess(cmd)
	var output strings.Builder
	cmd.Stdout = &output
	stderr := newTailBuffer(4096)
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return "", err
	}
	setLowPriority(cmd.Process.Pid)
	setIdleIOPriority(cmd.Process.Pid)
	if err := cmd.Wait(); err != nil {
		return "", fmt.Errorf("command failed: %w: %s", err, stderr.String())
	}
	return output.String(), nil
}

func selfTestWAV() []byte {
	const rate = 44100
	pcm := make([]byte, rate*3/2*4)
	for frame := 0; frame < rate*3/2; frame++ {
		var sample int16
		if frame >= rate/2 {
			sample = int16(math.Round(32767 * math.Pow(10, -20.0/20) * math.Sin(2*math.Pi*1000*float64(frame-rate/2)/rate)))
		}
		at := frame * 4
		pcm[at] = byte(sample)
		pcm[at+1] = byte(uint16(sample) >> 8)
	}
	wav := make([]byte, 44+len(pcm))
	copy(wav, "RIFF")
	putLE32(wav[4:], uint32(36+len(pcm)))
	copy(wav[8:], "WAVEfmt ")
	putLE32(wav[16:], 16)
	putLE16(wav[20:], 1)
	putLE16(wav[22:], 2)
	putLE32(wav[24:], rate)
	putLE32(wav[28:], rate*4)
	putLE16(wav[32:], 4)
	putLE16(wav[34:], 16)
	copy(wav[36:], "data")
	putLE32(wav[40:], uint32(len(pcm)))
	copy(wav[44:], pcm)
	return wav
}
func putLE16(dst []byte, value uint16) { dst[0] = byte(value); dst[1] = byte(value >> 8) }
func putLE32(dst []byte, value uint32) {
	dst[0] = byte(value)
	dst[1] = byte(value >> 8)
	dst[2] = byte(value >> 16)
	dst[3] = byte(value >> 24)
}

type tailBuffer struct {
	data  []byte
	limit int
}

func newTailBuffer(limit int) *tailBuffer { return &tailBuffer{limit: limit} }
func (b *tailBuffer) Write(value []byte) (int, error) {
	n := len(value)
	b.data = append(b.data, value...)
	if len(b.data) > b.limit {
		b.data = append([]byte(nil), b.data[len(b.data)-b.limit:]...)
	}
	return n, nil
}
func (b *tailBuffer) String() string { return string(b.data) }
