package render

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/dsp/chain"
	"github.com/qlqq/roomusic/backend/internal/playback/dsdio"
	"github.com/qlqq/roomusic/backend/internal/playback/dsdpcm"
	"github.com/qlqq/roomusic/backend/internal/playback/render/ffmpeg"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

// 4 KiB 双声道 DSD128 负载；两个不同频率的调制信号避免声道交换或定位偏移碰巧相同。
func makeDSD128(t *testing.T) audioFixture {
	t.Helper()
	const rate, frames = 5644800, 1024
	payload := make([]byte, frames*2*2)
	for ch, frequency := range []float64{997, 1693} {
		accumulator := 0.0
		for bit := 0; bit < frames*16; bit++ {
			accumulator += 0.3 * math.Sin(2*math.Pi*frequency*float64(bit)/rate)
			if accumulator >= 0 {
				payload[(bit/8)*2+ch] |= 1 << (7 - bit%8)
				accumulator--
			} else {
				accumulator++
			}
		}
	}
	chunk := func(id string, body []byte) []byte {
		out := make([]byte, 12)
		copy(out, id)
		binary.BigEndian.PutUint64(out[4:], uint64(len(body)))
		out = append(out, body...)
		if len(body)%2 != 0 {
			out = append(out, 0)
		}
		return out
	}
	fs := make([]byte, 4)
	binary.BigEndian.PutUint32(fs, rate)
	prop := append([]byte("SND "), chunk("FS  ", fs)...)
	prop = append(prop, chunk("CHNL", append([]byte{0, 2}, []byte("SLFTSRGT")...))...)
	prop = append(prop, chunk("CMPR", []byte("DSD \x00"))...)
	body := append([]byte("DSD "), chunk("FVER", []byte{1, 5, 0, 0})...)
	body = append(body, chunk("PROP", prop)...)
	body = append(body, chunk("DSD ", payload)...)
	data := chunk("FRM8", body)
	layout, err := dsdio.Probe(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "parent.dff")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return audioFixture{path: path, track: Track{SourceKind: "dff_diin", Codec: "dff", SampleRate: rate, Channels: 2, BitDepth: 1, Layout: 3, DSDLayout: &layout, DurationSeconds: float64(frames) / 352800}}
}

func dsdReference(t *testing.T, fixture audioFixture) *dsdpcm.Reader {
	t.Helper()
	f, err := os.Open(fixture.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	r, err := dsdpcm.New(context.Background(), f, *fixture.track.DSDLayout)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func dsdTestDecoder(t *testing.T, ctx context.Context, fixture audioFixture, seconds float64, mode string) (*decoder, func()) {
	t.Helper()
	track, err := fixtureSource(map[string]audioFixture{"a": fixture}).Prepare(ctx, "user", "a")
	if err != nil {
		t.Fatal(err)
	}
	d, err := openDecoder(ctx, nil, nil, track, zones.Entry{EntryID: "entry-a", TrackID: "a"}, effectiveSource(track).pcm, seconds, mode)
	if err != nil {
		track.File.Close()
		t.Fatal(err)
	}
	closeDecoder := sync.OnceFunc(d.close)
	t.Cleanup(closeDecoder)
	return d, closeDecoder
}

// 分块比较生产读取器而非整轨缓冲；分析侧也对同一默认读取合同做比较。
func TestDSDDecoderStreamingPCMAndLeveling(t *testing.T) {
	fixture := makeDSD128(t)
	gainDB := -7.0
	fixture.track.TrackGainDB = &gainDB
	for _, mode := range []string{"off", "track"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), testWait)
			defer cancel()
			d, _ := dsdTestDecoder(t, ctx, fixture, 0, mode)
			ref := dsdReference(t, fixture)
			if d.format.SampleRate != 352800 || d.integer || d.exact || d.sourceBits != 0 || d.process != nil {
				t.Fatalf("来源未变为内置浮点 DXD：%+v", d)
			}
			c, err := chain.Build(chain.Spec{Format: d.format})
			if err != nil {
				t.Fatal(err)
			}
			gain := 1.0
			if mode == "track" {
				gain = math.Pow(10, gainDB/20)
			}
			total := 0
			for {
				b, _, err := d.read(ctx, 137, false, time.Now)
				if err != nil && !errors.Is(err, io.EOF) {
					t.Fatal(err)
				}
				if b != nil {
					raw := make([]byte, b.Frames*8)
					if _, readErr := io.ReadFull(ref, raw); readErr != nil {
						t.Fatal(readErr)
					}
					if len(b.Regions) != 0 || b.Exact || b.SourceBits != 0 {
						t.Fatalf("电平化区域未消费或伪装整数：%+v", b)
					}
					out := c.Process(b)
					for frame := 0; frame < b.Frames; frame++ {
						for ch := range 2 {
							want := float64(math.Float32frombits(binary.LittleEndian.Uint32(raw[(frame*2+ch)*4:]))) * gain
							if got := out.Samples[ch][frame]; got != want {
								t.Fatalf("帧%d 声道%d：PCM=%g，转换后一次增益=%g", total+frame, ch, got, want)
							}
						}
					}
					total += b.Frames
				}
				if errors.Is(err, io.EOF) {
					break
				}
			}
			if total != 1024 {
				t.Fatalf("输出帧=%d，期望1024", total)
			}
			if n, err := ref.Read(make([]byte, 8)); n != 0 || err != io.EOF {
				t.Fatalf("参考流未同时结束：%d/%v", n, err)
			}
		})
	}
}

func TestDSDCUEContinuousProducerAndSeek(t *testing.T) {
	fixture := makeDSD128(t)
	const start, cut = int64(17), int64(511)
	a, b := fixture, fixture
	a.track.SourceKind, b.track.SourceKind = "cue_virtual", "cue_virtual"
	first, boundary := start, cut
	a.track.CueStartFrame, a.track.CueEndFrame = &first, &boundary
	b.track.CueStartFrame = &boundary
	a.track.DurationSeconds, b.track.DurationSeconds = float64(cut-start)/352800, float64(1024-cut)/352800
	ctx, cancel := context.WithTimeout(context.Background(), testWait)
	defer cancel()
	d, closeCurrent := dsdTestDecoder(t, ctx, a, 0, "off")
	spec := renderSpec("wav")
	spec.Cursor = testCursor(nextB)
	r := newTestRenderer(t, nil, fixtureSource(map[string]audioFixture{"a": a, "b": b}))
	format, plan, err := concreteFormat(spec, a.track)
	if err != nil {
		t.Fatal(err)
	}
	s := &session{ctx: ctx, owner: r, spec: spec, format: format, sourcePCM: plan.sourcePCM, outputPCM: plan.outputPCM}
	p := &player{session: s, current: d, anchor: a.track, anchorKnown: true}
	ok, err := p.prepareNext(ctx, zones.Entry{EntryID: "entry-b", TrackID: "b"}, false)
	if err != nil || !ok || p.next == nil {
		t.Fatalf("预取失败：%v/%v", ok, err)
	}
	next := p.next
	t.Cleanup(next.close)
	if next.producer != d.producer || next.ring != d.ring || d.producer.refs != 2 {
		t.Fatal("相邻 CUE 没有共享生产者、环和滤波历史")
	}
	ref := dsdReference(t, fixture)
	if _, err := io.CopyN(io.Discard, ref, start*8); err != nil {
		t.Fatal(err)
	}
	for _, segment := range []struct {
		d      *decoder
		frames int
	}{{d, int(cut - start)}, {next, int(1024 - cut)}} {
		consumed := 0
		for {
			buf, _, err := segment.d.read(ctx, 193, false, time.Now)
			if err != nil && !errors.Is(err, io.EOF) {
				t.Fatal(err)
			}
			if buf != nil {
				raw := make([]byte, buf.Frames*8)
				if _, err := io.ReadFull(ref, raw); err != nil {
					t.Fatal(err)
				}
				for frame := range buf.Frames {
					for ch := range 2 {
						want := float64(math.Float32frombits(binary.LittleEndian.Uint32(raw[(frame*2+ch)*4:])))
						if buf.Samples[ch][frame] != want {
							t.Fatalf("CUE拼接帧%d声道%d与整轨不一致", consumed+frame, ch)
						}
					}
				}
				consumed += buf.Frames
			}
			if errors.Is(err, io.EOF) {
				break
			}
		}
		if consumed != segment.frames || segment.d.position() != int64(consumed) {
			t.Fatalf("分轨帧数/曲内位置=%d/%d，期望%d", consumed, segment.d.position(), segment.frames)
		}
		if segment.d == d {
			// 模拟 player 在第一段 EOF 释放 current，后一段仍必须保持同一连续 PCM。
			closeCurrent()
			if next.producer.refs != 1 {
				t.Fatalf("释放前段后引用数=%d，期望1", next.producer.refs)
			}
		}
	}
	// 非零曲内 seek 四舍五入到 24 帧，nil 结束边界读到父 EOF。
	const seconds = 23.6 / 352800
	seek, _ := dsdTestDecoder(t, ctx, b, seconds, "off")
	reference := dsdReference(t, fixture)
	if _, err := io.CopyN(io.Discard, reference, (cut+24)*8); err != nil {
		t.Fatal(err)
	}
	total := 0
	for {
		buf, _, err := seek.read(ctx, 113, false, time.Now)
		if err != nil && !errors.Is(err, io.EOF) {
			t.Fatal(err)
		}
		if buf != nil {
			raw := make([]byte, buf.Frames*8)
			if _, err := io.ReadFull(reference, raw); err != nil {
				t.Fatal(err)
			}
			for i := range buf.Frames {
				for ch := range 2 {
					if got, want := buf.Samples[ch][i], float64(math.Float32frombits(binary.LittleEndian.Uint32(raw[(i*2+ch)*4:]))); got != want {
						t.Fatalf("seek PCM=%g，整轨=%g", got, want)
					}
				}
			}
			total += buf.Frames
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}
	if total != 1024-int(cut)-24 || seek.position() != 1024-cut {
		t.Fatalf("seek后的帧数/曲内位置=%d/%d", total, seek.position())
	}
}

func TestDSDCancelReleasesBlockedProducer(t *testing.T) {
	fixture := makeDSD128(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	track, err := fixtureSource(map[string]audioFixture{"a": fixture}).Prepare(ctx, "user", "a")
	if err != nil {
		t.Fatal(err)
	}
	// 缩小同一个生产环使几KB素材就能确定性阻塞写端，无需构造十秒大文件。
	d, err := openDSDDecoder(ctx, track, zones.Entry{}, effectiveSource(track).pcm, 0, "off", pcmRange{}, 64)
	if err != nil {
		track.File.Close()
		t.Fatal(err)
	}
	t.Cleanup(d.close)
	waitUntil(t, "DSD生产环满", func() bool { d.ring.mu.Lock(); defer d.ring.mu.Unlock(); return d.ring.size == len(d.ring.data) })
	select {
	case <-d.producer.done:
		t.Fatal("取消前生产者应阻塞于满环")
	default:
	}
	cancel()
	select {
	case <-d.producer.done:
	case <-time.After(testWait):
		t.Fatal("取消后生产者未退出")
	}
	if _, err := track.File.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("取消后来源未关闭：%v", err)
	}
}

// 由既有会话表的DSD两行调用；容量恰为实际编码进程数，任何额外解码预留都会失败。
func testDSDSessionCapacityAndClocks(t *testing.T, tool *ffmpeg.Tool, codec string, slots int) {
	t.Helper()
	fixture := makeDSD128(t)
	r := newTestRenderer(t, tool.Independent(slots), fixtureSource(map[string]audioFixture{"a": fixture}))
	spec := renderSpec(codec)
	spec.Format.MaxSampleRate = 176400
	s := openTestSession(t, r, spec)
	data := readBody(t, s)
	waitRenderComplete(t, s)
	if s.sourcePCM.SampleRate != 352800 || s.outputPCM.SampleRate != 176400 || s.Format().SampleRate != 176400 || s.chain.InputFormat() != s.sourcePCM || s.chain.OutputFormat() != s.outputPCM {
		t.Fatalf("DSD比特率被误作PCM时钟：%+v → %+v", s.sourcePCM, s.outputPCM)
	}
	s.mu.Lock()
	submitted, written := s.inputSubmitted, s.written
	s.mu.Unlock()
	if submitted != 1024 || written != 512 {
		t.Fatalf("源/输出帧=%d/%d，期望1024/512", submitted, written)
	}
	if codec == "wav" {
		if len(data) != 44+512*2*3 || binary.LittleEndian.Uint32(data[24:28]) != 176400 {
			t.Fatalf("WAV长度或时钟错误：%d字节", len(data))
		}
	} else {
		if len(data) < 4 || string(data[:4]) != "fLaC" {
			t.Fatal("未实际产出FLAC")
		}
	}
	duration := float64(512) / 176400
	observe(s, duration)
	events := eventsThrough(t, s, "stream_ended", 0)
	for _, event := range events {
		switch event.Kind {
		case "track_failed", "format_boundary":
			t.Fatalf("DSD会话失败：%+v", event)
		case "stream_ended":
			if event.StreamSeconds != duration {
				t.Fatalf("输出时钟流尾=%g，期望%g", event.StreamSeconds, duration)
			}
		case "time":
			if math.Abs(event.Seconds-event.StreamSeconds) > 1e-12 || event.StreamSeconds > duration {
				t.Fatalf("时钟错误：%+v", event)
			}
		}
	}
}
