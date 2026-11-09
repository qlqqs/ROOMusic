package irload

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/dsp/convolution"
	"github.com/qlqq/roomusic/backend/internal/playback/render/ffmpeg"
)

const MaxFileBytes = 32 << 20
const ProcessingTimeout = 120 * time.Second

var (
	ErrFormatUnsupported = errors.New("不支持这种 IR 格式")
	ErrTooLarge          = errors.New("IR 文件或解码结果太大")
	ErrDecodeFailed      = errors.New("IR 文件无法解码")
)

type Metadata struct {
	Container                         string
	SampleRate, Channels, Paths, Taps int
}

type Loaded struct {
	Config        convolution.Config
	ResampledFrom int
}

// Loader 独占一个解码名额；不借用播放会话预留的进程容量。
// gate 同时限制解压、解码与重采样的在途内存，不持数据库事务。
type Loader struct {
	tool *ffmpeg.Tool
	gate chan struct{}
}

func NewLoader(tool *ffmpeg.Tool) *Loader {
	l := &Loader{gate: make(chan struct{}, 1)}
	if tool != nil {
		l.tool = tool.Independent(1)
	}
	return l
}

func (l *Loader) acquire(ctx context.Context) error {
	select {
	case l.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *Loader) Validate(ctx context.Context, fileName string, data []byte) (Metadata, error) {
	ctx, cancel := context.WithTimeout(ctx, ProcessingTimeout)
	defer cancel()
	if err := l.acquire(ctx); err != nil {
		return Metadata{}, err
	}
	defer func() { <-l.gate }()
	container, err := detectContainer(fileName, data)
	if err != nil {
		return Metadata{}, err
	}
	archives, err := l.archives(ctx, container, fileName, data, 48000, 2, true)
	if err != nil {
		return Metadata{}, err
	}
	var metadata Metadata
	for i, archive := range archives {
		sources, err := l.decodeSources(ctx, archive)
		if err != nil {
			return Metadata{}, err
		}
		rate, channels, paths := sources[0].SampleRate, len(sources[0].Samples), len(sources[0].Samples)
		if archive.Config != nil {
			rate, channels, paths = archive.Config.SampleRate, archive.Config.OutputChannels, len(archive.Config.Paths)
		}
		if err := validatePreparation(archive.Config, sources, rate, channels); err != nil {
			return Metadata{}, classifyDecode(err)
		}
		taps := 0
		for _, source := range sources {
			n, err := resampledLength(len(source.Samples[0]), source.SampleRate, rate)
			if err != nil {
				return Metadata{}, err
			}
			taps = max(taps, n)
		}
		if archive.Config != nil {
			in, err := prepareDelays(archive.Config.InDelayMS, rate)
			if err != nil {
				return Metadata{}, classifyDecode(err)
			}
			out, err := prepareDelays(archive.Config.OutDelayMS, rate)
			if err != nil {
				return Metadata{}, classifyDecode(err)
			}
			if err := validateDelayBudget(in, out); err != nil {
				return Metadata{}, err
			}
		}
		if i == 0 {
			metadata = Metadata{container, rate, channels, paths, taps}
		}
	}
	if err := ctx.Err(); err != nil {
		return Metadata{}, err
	}
	return metadata, nil
}

func (l *Loader) Load(ctx context.Context, fileName string, data []byte, rate, channels int) (Loaded, error) {
	ctx, cancel := context.WithTimeout(ctx, ProcessingTimeout)
	defer cancel()
	if err := l.acquire(ctx); err != nil {
		return Loaded{}, err
	}
	defer func() { <-l.gate }()
	container, err := detectContainer(fileName, data)
	if err != nil {
		return Loaded{}, err
	}
	archives, err := l.archives(ctx, container, fileName, data, rate, channels, false)
	if err != nil {
		return Loaded{}, err
	}
	archive := archives[0]
	sources, err := l.decodeSources(ctx, archive)
	if err != nil {
		return Loaded{}, err
	}
	config, err := PrepareContext(ctx, archive.Config, sources, rate, channels)
	if err != nil {
		return Loaded{}, classifyDecode(err)
	}
	original := 0
	for _, source := range sources {
		if source.SampleRate != rate {
			original = source.SampleRate
			break
		}
	}
	return Loaded{Config: config, ResampledFrom: original}, nil
}

func (l *Loader) archives(ctx context.Context, container, fileName string, data []byte, rate, channels int, all bool) ([]*Archive, error) {
	if container == "zip" {
		archives, err := parseZIPArchives(ctx, data, rate, channels, all)
		return archives, classifyDecode(err)
	}
	return []*Archive{{Sources: []Source{{Name: fileName, Data: data}}}}, nil
}

func (l *Loader) decodeSources(ctx context.Context, archive *Archive) ([]*DecodedIR, error) {
	sources := make([]*DecodedIR, 0, len(archive.Sources))
	decoded := make(map[string]*DecodedIR)
	totalBytes := 0
	for _, source := range archive.Sources {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		key := strings.ToLower(source.Name)
		ir := decoded[key]
		if ir == nil {
			ext := strings.ToLower(path.Ext(source.Name))
			if ext == ".pcm" || ext == ".dbl" {
				if archive.Config == nil {
					return nil, ErrDecodeFailed
				}
				samples, err := ParseRaw(source.Data, ext)
				if err != nil {
					return nil, classifyDecode(err)
				}
				ir = &DecodedIR{SampleRate: archive.Config.SampleRate, Samples: [][]float64{samples}}
			} else {
				var err error
				ir, err = l.decodeAudio(ctx, source)
				if err != nil {
					return nil, err
				}
			}
			totalBytes += len(ir.Samples) * len(ir.Samples[0]) * 8
			if totalBytes > MaxZIPBytes {
				return nil, ErrTooLarge
			}
			decoded[key] = ir
		}
		sources = append(sources, ir)
	}
	return sources, nil
}

func detectContainer(fileName string, data []byte) (string, error) {
	if len(data) > MaxFileBytes {
		return "", ErrTooLarge
	}
	ext := strings.ToLower(path.Ext(fileName))
	valid := false
	switch ext {
	case ".zip":
		valid = len(data) >= 4 && string(data[:2]) == "PK"
	case ".wav":
		valid = len(data) >= 12 && (string(data[:4]) == "RIFF" || string(data[:4]) == "RF64") && string(data[8:12]) == "WAVE"
	case ".flac":
		valid = len(data) >= 4 && string(data[:4]) == "fLaC"
	case ".aif", ".aiff":
		valid = len(data) >= 12 && string(data[:4]) == "FORM" && (string(data[8:12]) == "AIFF" || string(data[8:12]) == "AIFC")
		ext = ".aiff"
	case ".m4a":
		valid = len(data) >= 12 && string(data[4:8]) == "ftyp"
	}
	if !valid {
		return "", ErrFormatUnsupported
	}
	return strings.TrimPrefix(ext, "."), nil
}

func (l *Loader) decodeAudio(ctx context.Context, source Source) (*DecodedIR, error) {
	if l.tool == nil {
		return nil, ErrDecodeFailed
	}
	dir, err := os.MkdirTemp("", "roomusic-ir-")
	if err != nil {
		return nil, classifyDecode(err)
	}
	defer os.RemoveAll(dir)
	file, err := os.CreateTemp(dir, "source-")
	if err != nil {
		return nil, classifyDecode(err)
	}
	defer file.Close()
	if len(source.Data) > MaxZIPBytes {
		return nil, ErrTooLarge
	}
	if _, err = file.Write(source.Data); err != nil {
		return nil, classifyDecode(err)
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return nil, classifyDecode(err)
	}
	demuxer := map[string]string{".wav": "wav", ".flac": "flac", ".aif": "aiff", ".aiff": "aiff", ".m4a": "mov"}[strings.ToLower(path.Ext(source.Name))]
	if demuxer == "" {
		return nil, ErrFormatUnsupported
	}
	// 强制容器解复用器，禁用 MOV 外部引用；只允许预先打开的本地句柄与输出管道。
	args := []string{"-hide_banner", "-loglevel", "error", "-xerror", "-nostdin", "-threads", "1", "-protocol_whitelist", "file,pipe", "-f", demuxer}
	if demuxer == "mov" {
		args = append(args, "-enable_drefs", "0", "-use_absolute_path", "0")
	}
	args = append(args, "-i", "file:/dev/fd/3", "-map", "0:a:0", "-map_metadata", "-1", "-vn", "-sn", "-dn", "-threads", "1", "-c:a", "pcm_f64le", "-f", "wav", "pipe:1")
	process, err := l.tool.Start(ctx, ffmpeg.StartOptions{Source: file, ReadTimeout: true, Args: args})
	if err != nil {
		return nil, classifyDecode(err)
	}
	defer process.Close()
	ir, err := readFloatWAV(ctx, process)
	if err != nil {
		return nil, classifyDecode(err)
	}
	if err := process.Wait(); err != nil {
		return nil, classifyDecode(err)
	}
	return ir, nil
}

func classifyDecode(err error) error {
	if err == nil {
		return nil
	}
	var cfg *CFGError
	if errors.As(err, &cfg) || errors.Is(err, ErrTooLarge) || errors.Is(err, ErrFormatUnsupported) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("%w: %w", ErrDecodeFailed, err)
}
