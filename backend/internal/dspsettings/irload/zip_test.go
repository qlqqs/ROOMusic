package irload

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/dsp/convolution"
)

func TestParseZIPSelectsCFGByPriority(t *testing.T) {
	for _, tt := range []struct {
		name    string
		entries []zipEntry
		want    string
	}{
		{"sample_rate_before_channels", []zipEntry{
			{"a.cfg", cfgData(44100, 2)}, {"z.cfg", cfgData(48000, 1)},
		}, "z.cfg"},
		{"channels_before_name", []zipEntry{
			{"a.cfg", cfgData(48000, 1)}, {"z.cfg", cfgData(48000, 2)},
		}, "z.cfg"},
		{"name_breaks_tie", []zipEntry{
			{"z.cfg", cfgData(48000, 2)}, {"a.cfg", cfgData(48000, 2)},
		}, "a.cfg"},
		{"channels_without_rate_match", []zipEntry{
			{"a.cfg", cfgData(44100, 1)}, {"z.cfg", cfgData(96000, 2)},
		}, "z.cfg"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			entries := append(tt.entries, zipEntry{"impulse.pcm", rawSamples([]float64{1}, 4)})
			// 有 cfg 时不使用额外的音频文件作为路由来源。
			entries = append(entries, zipEntry{"unused.wav", []byte("仅作容器选择测试，不进入音频解码")})
			got, err := ParseZIP(makeZIP(t, entries), 48000, 2)
			if err != nil {
				t.Fatal(err)
			}
			if got.ConfigName != tt.want || got.Config == nil || len(got.Sources) != 1 || got.Sources[0].Name != "impulse.pcm" {
				t.Fatalf("选择结果 = %+v, want cfg=%s, source=impulse.pcm", got, tt.want)
			}
		})
	}
}

func TestZIPCFGImpulseRouting(t *testing.T) {
	data := makeZIP(t, []zipEntry{
		{"Room/route.cfg", []byte("48000 2 2 3\n1 0\n0 0\nfilter.PCM\n0\n0.0 1.5\n1.0\nfilter.PCM\n0\n1.0\n-0.99999\n")},
		{"Room/Filter.pcm", rawSamples([]float64{1}, 4)},
	})
	a, err := ParseZIP(data, 48000, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Sources) != 2 || a.Sources[0].Name != "Room/Filter.pcm" || a.Sources[1].Name != "Room/Filter.pcm" {
		t.Fatalf("cfg 相对路径或大小写匹配错误：%+v", a.Sources)
	}
	// 本例声道数已经一致；仅将解析结果送入纯卷积器，不实现装载层声道匹配。
	config := convolution.Config{Channels: 2}
	for _, ms := range a.Config.InDelayMS {
		n, err := DelaySamples(ms, 48000)
		if err != nil {
			t.Fatal(err)
		}
		config.InDelay = append(config.InDelay, n)
	}
	for _, ms := range a.Config.OutDelayMS {
		n, err := DelaySamples(ms, 48000)
		if err != nil {
			t.Fatal(err)
		}
		config.OutDelay = append(config.OutDelay, n)
	}
	for i, route := range a.Config.Paths {
		h, err := ParseRaw(a.Sources[i].Data, ".pcm")
		if err != nil {
			t.Fatal(err)
		}
		p := convolution.Path{IR: h}
		for _, w := range route.Inputs {
			p.Inputs = append(p.Inputs, convolution.Weighted{Channel: w.Channel, Weight: w.Weight})
		}
		for _, w := range route.Outputs {
			p.Outputs = append(p.Outputs, convolution.Weighted{Channel: w.Channel, Weight: w.Weight})
		}
		config.Paths = append(config.Paths, p)
	}
	c, err := convolution.New(config)
	if err != nil {
		t.Fatal(err)
	}
	for start := 0; start < 60; start += 10 {
		x := [][]float64{make([]float64, 10), make([]float64, 10)}
		if start == 0 {
			x[0][0], x[1][0] = 1, 2
		}
		y := c.Process(x)
		if len(y) != 2 || len(y[0]) != 10 || len(y[1]) != 10 {
			t.Fatalf("输出形状不匹配：%v", y)
		}
		for ch := range y {
			for i, got := range y[ch] {
				want := 0.0
				if ch == 0 && start+i == 0 {
					want = -1.99998
				}
				if ch == 1 && (start+i == 0 || start+i == 48) {
					want = 1
				}
				if math.IsNaN(got) || math.Abs(got-want) > 1e-12 {
					t.Fatalf("声道 %d 帧 %d = %g, want %g", ch, start+i, got, want)
				}
			}
		}
	}
}

func TestParseZIPEntryLimit(t *testing.T) {
	entries := []zipEntry{{"impulse.wav", []byte{1}}}
	for i := 1; i < MaxZIPEntries; i++ {
		entries = append(entries, zipEntry{fmt.Sprintf("note-%d.txt", i), nil})
	}
	if _, err := ParseZIP(makeZIP(t, entries), 48000, 1); err != nil {
		t.Fatalf("恰好 %d 个条目被拒绝：%v", MaxZIPEntries, err)
	}
	entries = append(entries, zipEntry{"extra.txt", nil})
	if _, err := ParseZIP(makeZIP(t, entries), 48000, 1); err == nil || !strings.Contains(err.Error(), "entry limit") {
		t.Fatalf("超出条目限额的错误 = %v", err)
	}
}

func TestParseZIPDecompressedSizeLimit(t *testing.T) {
	for _, prefix := range []string{"", "12345678"} {
		t.Run(fmt.Sprintf("previous_bytes_%d", len(prefix)), func(t *testing.T) {
			var buf bytes.Buffer
			w := zip.NewWriter(&buf)
			if prefix != "" {
				entry, err := w.Create("note.txt")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := entry.Write([]byte(prefix)); err != nil {
					t.Fatal(err)
				}
			}
			// 只声明超额大小，核实提前拒绝及前一条目的累计扣减，不分配巨型素材。
			_, err := w.CreateRaw(&zip.FileHeader{Name: "impulse.wav", Method: zip.Store,
				UncompressedSize64: uint64(MaxZIPBytes - len(prefix) + 1)})
			if err != nil {
				t.Fatal(err)
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			_, err = ParseZIP(buf.Bytes(), 48000, 1)
			if err == nil || !strings.Contains(err.Error(), "decompressed size limit") {
				t.Fatalf("解压总量超限的错误 = %v", err)
			}
		})
	}
}

func TestReadEntryByteBudget(t *testing.T) {
	data := makeZIP(t, []zipEntry{{"impulse.pcm", []byte("12345678")}})
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	got, err := readEntry(r.File[0], 8)
	if err != nil || string(got) != "12345678" {
		t.Fatalf("恰好用完剩余额度：got %q, err %v", got, err)
	}
	if _, err := readEntry(r.File[0], 7); err == nil {
		t.Fatal("条目超过剩余解压额度仍被接受")
	}
	// 篡改声明大小，实际解压仍产生 8 字节，不能仅信任元数据放行。
	r.File[0].UncompressedSize64 = 1
	if _, err := readEntry(r.File[0], 7); err == nil {
		t.Fatal("实际解压大小超过声明/剩余额度仍被接受")
	}
}

func TestParseZIPRejectsCRCError(t *testing.T) {
	data := makeZIP(t, []zipEntry{{"impulse.wav", []byte{1, 2, 3, 4}}})
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	offset, err := r.File[0].DataOffset()
	if err != nil {
		t.Fatal(err)
	}
	// Store 条目的长度和目录保持不变，仅破坏数据字节以触发真实 CRC 校验。
	data[offset] ^= 0xff
	if _, err := ParseZIP(data, 48000, 1); !errors.Is(err, zip.ErrChecksum) {
		t.Fatalf("CRC 错误 = %v, want zip.ErrChecksum", err)
	}
}

func TestParseZIPRejectsDuplicateNames(t *testing.T) {
	for _, name := range []string{"room.wav", "ROOM.WAV", "sub/../room.wav"} {
		t.Run(name, func(t *testing.T) {
			data := makeZIP(t, []zipEntry{{"room.wav", []byte{1}}, {name, []byte{2}}})
			if _, err := ParseZIP(data, 48000, 1); err == nil || !strings.Contains(err.Error(), "ambiguous zip filename") {
				t.Fatalf("重名错误 = %v", err)
			}
		})
	}
}

func TestParseZIPMissingReferenceReportsCFGLine(t *testing.T) {
	data := makeZIP(t, []zipEntry{{"route.cfg", cfgData(48000, 2)}})
	_, err := ParseZIP(data, 48000, 2)
	assertCFGErrorLine(t, err, 4)
}

type zipEntry struct {
	name string
	data []byte
}

func makeZIP(t *testing.T, entries []zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, entry := range entries {
		f, err := w.CreateHeader(&zip.FileHeader{Name: entry.name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func cfgData(rate, channels int) []byte {
	return []byte(fmt.Sprintf("%d %d 2 3\n%s\n0 0\nimpulse.pcm\n0\n0.0\n0.0\n",
		rate, channels, strings.TrimSpace(strings.Repeat("0 ", channels))))
}
