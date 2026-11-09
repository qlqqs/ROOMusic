package render

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/qlqq/roomusic/backend/internal/dsp/chain"
	"github.com/qlqq/roomusic/backend/internal/dsp/leveling"
	"github.com/qlqq/roomusic/backend/internal/playback/render/ffmpeg"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

// pcmRange 保存源 PCM 帧范围；本层不解析 CUE 索引。
type pcmRange struct {
	start int64
	end   *int64
}

type decoder struct {
	track          Track
	entry          zones.Entry
	process        *ffmpeg.Process
	cancel         context.CancelFunc
	done           chan struct{}
	ring           *byteRing
	format         chain.Format
	integer, exact bool
	sourceBits     int
	gain           float64
	leveling       bool
	consumed       int64
	startSeconds   float64
	raw            []byte
	buffer         chain.Buffer
}

type processStarter interface {
	Start(context.Context, ffmpeg.StartOptions) (*ffmpeg.Process, error)
}

func openDecoder(ctx context.Context, starter processStarter, tool *ffmpeg.Tool, track Track, entry zones.Entry, format chain.Format, startSeconds float64, mode string) (*decoder, error) {
	span := pcmRange{start: int64(math.Round(startSeconds * float64(track.SampleRate)))}
	if track.CueStartFrame != nil {
		span.start += *track.CueStartFrame
	}
	if track.CueEndFrame != nil {
		end := *track.CueEndFrame
		span.end = &end
	}
	if span.start < 0 || span.end != nil && *span.end < span.start {
		return nil, &SourceError{Reason: "decode_failed"}
	}
	capacity := int64(format.SampleRate) * int64(format.Channels) * 4 * 10
	if capacity > 256<<20 {
		return nil, &SourceError{Reason: "unsupported_format"}
	}
	integer := integerSource(track)
	pcmKind := "f32le"
	if integer {
		pcmKind = "s32le"
	}
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-threads", "1", "-protocol_whitelist", "file,pipe"}
	cue := track.CueStartFrame != nil || track.CueEndFrame != nil
	if cue {
		// 粗定位向前留至少一秒，容器再回到此前的可解码块；保留其绝对时间戳供精确截取。
		seekSeconds := max(int64(0), span.start/int64(track.SampleRate)-1)
		args = append(args, "-copyts", "-ss", strconv.FormatInt(seekSeconds, 10), "-noaccurate_seek")
	} else if span.start > 0 {
		args = append(args, "-ss", strconv.FormatFloat(float64(span.start)/float64(track.SampleRate), 'f', 9, 64))
	}
	args = append(args, "-i", "file:/dev/fd/3", "-map", "0:a:0", "-vn", "-map_metadata", "-1", "-threads", "1")
	var filters []string
	if cue {
		// 先把时间基设为一个源样本，再按绝对样本位置截取，不从粗定位落点重新数样本。
		trim := "atrim=start_pts=" + strconv.FormatInt(span.start, 10)
		if span.end != nil {
			trim += ":end_pts=" + strconv.FormatInt(*span.end, 10)
		}
		filters = append(filters, "asettb=1/"+strconv.Itoa(track.SampleRate), trim, "asetpts=PTS-STARTPTS")
	}
	if len(filters) > 0 {
		args = append(args, "-af", strings.Join(filters, ","))
	}
	args = append(args, "-ac", strconv.Itoa(format.Channels), "-f", pcmKind, "pipe:1")
	child, cancel := context.WithCancel(ctx)
	process, err := starter.Start(child, ffmpeg.StartOptions{Args: args, Source: track.File, ReadTimeout: true})
	if err != nil {
		cancel()
		return nil, &SourceError{Reason: "decode_failed", Err: err}
	}
	gainDB, _, enabled := leveling.Choose(leveling.Mode(mode), track.TrackGainDB, track.AlbumGainDB, entry.AlbumContext)
	d := &decoder{track: track, entry: entry, process: process, cancel: cancel, done: make(chan struct{}), ring: newByteRing(int(capacity)), format: format, integer: integer,
		exact:      integer && track.SampleRate == format.SampleRate && track.Channels == format.Channels,
		sourceBits: track.BitDepth, gain: leveling.Linear(gainDB), leveling: enabled, startSeconds: startSeconds}
	if integer && d.sourceBits == 0 {
		d.sourceBits = 32
	}
	go d.fill(child)
	return d, nil
}

func (d *decoder) fill(ctx context.Context) {
	defer close(d.done)
	stop := context.AfterFunc(ctx, func() { d.ring.finish(ctx.Err()) })
	defer stop()
	defer d.track.File.Close()
	defer d.process.Close()
	chunk := make([]byte, 32*1024)
	var total int64
	for {
		available, err := d.ring.free(ctx)
		if err != nil {
			d.ring.finish(err)
			return
		}
		size := min(len(chunk), available)
		// 只有真实 stdout 读取才启用 ffmpeg 的 10 秒超时；环满的等待在上面。
		n, readErr := d.process.Read(chunk[:size])
		if n > 0 {
			d.ring.put(chunk[:n])
			total += int64(n)
		}
		if readErr != nil {
			if readErr == io.EOF {
				if waitErr := d.process.Wait(); waitErr != nil {
					readErr = waitErr
				}
				if total == 0 || total%int64(d.format.Channels*4) != 0 {
					readErr = errors.New("render: 解码结果为空或帧不完整")
				}
			}
			if readErr != io.EOF {
				readErr = &SourceError{Reason: "decode_failed", Err: readErr}
			}
			d.ring.finish(readErr)
			return
		}
	}
}

func (d *decoder) close() { d.cancel(); <-d.done }

func (d *decoder) position() int64 {
	return int64(math.Round(d.startSeconds*float64(d.format.SampleRate))) + d.consumed
}

// read 的等待发生在计时区外；只累计环拷贝、格式换算与电平化的耗时。
func (d *decoder) read(ctx context.Context, frames int, ready bool, clock func() time.Time) (*chain.Buffer, time.Duration, error) {
	needed := frames * d.format.Channels * 4
	if err := d.ring.wait(ctx, needed, ready); err != nil {
		return nil, 0, err
	}
	begin := clock()
	if cap(d.raw) < needed {
		d.raw = make([]byte, needed)
	} else {
		d.raw = d.raw[:needed]
	}
	raw := d.raw
	n, err := d.ring.take(raw, d.format.Channels*4)
	count := n / (d.format.Channels * 4)
	b := &d.buffer
	b.Frames, b.Exact, b.SourceBits = count, d.exact, d.sourceBits
	b.Regions = b.Regions[:0]
	if len(b.Samples) != d.format.Channels {
		b.Samples = make([][]float64, d.format.Channels)
	}
	for channel := range b.Samples {
		if cap(b.Samples[channel]) < count {
			b.Samples[channel] = make([]float64, count)
		} else {
			b.Samples[channel] = b.Samples[channel][:count]
		}
		for frame := range count {
			bits := binary.LittleEndian.Uint32(raw[(frame*d.format.Channels+channel)*4:])
			if d.integer {
				b.Samples[channel][frame] = float64(int32(bits)) / 2147483648
			} else {
				b.Samples[channel][frame] = float64(math.Float32frombits(bits))
			}
		}
	}
	if d.leveling && count > 0 {
		b.Regions = append(b.Regions, chain.GainRegion{Frames: count, Gain: d.gain})
		leveling.Apply(b.Samples, &b.Regions, &b.Exact)
	}
	d.consumed += int64(count)
	return b, clock().Sub(begin), err
}

// byteRing 单写单读，EOF 与字节同锁发布；上下文取消广播两个条件变量。
type byteRing struct {
	mu                 sync.Mutex
	readable, writable *sync.Cond
	data               []byte
	head, size         int
	terminal           error
	changed            chan struct{}
}

func newByteRing(size int) *byteRing {
	r := &byteRing{data: make([]byte, size), changed: make(chan struct{}, 1)}
	r.readable, r.writable = sync.NewCond(&r.mu), sync.NewCond(&r.mu)
	return r
}
func (r *byteRing) finish(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.terminal == nil {
		r.terminal = err
	}
	wake(r.changed)
	r.readable.Broadcast()
	r.writable.Broadcast()
}
func (r *byteRing) free(ctx context.Context) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for r.size == len(r.data) && ctx.Err() == nil {
		r.writable.Wait()
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return len(r.data) - r.size, nil
}
func (r *byteRing) put(data []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	tail := (r.head + r.size) % len(r.data)
	first := copy(r.data[tail:], data)
	copy(r.data, data[first:])
	r.size += len(data)
	wake(r.changed)
	r.readable.Signal()
}
func (r *byteRing) wait(ctx context.Context, size int, initial bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if initial {
		size = max(size, (len(r.data)*33+99)/100)
	}
	for r.size < size && r.terminal == nil && ctx.Err() == nil {
		r.readable.Wait()
	}
	return ctx.Err()
}
func (r *byteRing) take(dst []byte, frameBytes int) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := min(len(dst), r.size)
	n -= n % frameBytes
	first := copy(dst[:n], r.data[r.head:min(len(r.data), r.head+n)])
	copy(dst[first:n], r.data[:n-first])
	r.head = (r.head + n) % len(r.data)
	r.size -= n
	var err error
	if r.size < frameBytes {
		err = r.terminal
	}
	r.writable.Signal()
	return n, err
}

// readiness 与通知通道在同一把锁下取得，避免检查后、开始等待前漏掉就绪。
func (r *byteRing) readiness(frameBytes int) (bool, <-chan struct{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.size >= max(frameBytes, (len(r.data)*33+99)/100) || r.terminal != nil, r.changed
}
