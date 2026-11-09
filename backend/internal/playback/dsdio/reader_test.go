package dsdio_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/playback/dsdio"
)

func TestDSFReadBlockInterleaveAndBitOrder(t *testing.T) {
	for _, tc := range []struct {
		name         string
		order        dsdio.BitOrder
		stored, want []byte
	}{
		{"LSB反转", dsdio.LSBFirst, []byte{0x01, 0x80, 0x96, 0x35}, []byte{0x80, 0x01, 0x69, 0xac}},
		{"MSB直接", dsdio.MSBFirst, []byte{0x01, 0x80, 0x96, 0x35}, []byte{0x01, 0x80, 0x96, 0x35}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const frames = 4096 + 19
			stored, want := make([][]byte, 2), make([][]byte, 2)
			for ch := range stored {
				stored[ch], want[ch] = make([]byte, frames), make([]byte, frames)
				for i := range stored[ch] {
					stored[ch][i] = tc.stored[(i+ch)%len(tc.stored)]
					want[ch][i] = tc.want[(i+ch)%len(tc.want)]
				}
			}
			data := readerTestDSF(stored, uint64(frames*8), tc.order)
			reader, layout := readerTestOpen(t, data)
			if layout.Container != dsdio.DSF || layout.Channels != 2 || layout.Mask != 3 || layout.Rate != 2822400 || layout.DSFBlockSize != 4096 || layout.BitOrder != tc.order || layout.ByteFrames() != frames {
				t.Fatalf("容器事实错误：%+v", layout)
			}
			position := 0
			// 第二次读取同时跨越物理声道块和下一组块，最后一次越过有效末尾。
			for _, size := range []int{4095, 7, 32} {
				dst := [][]byte{bytes.Repeat([]byte{0xee}, size), bytes.Repeat([]byte{0xee}, size)}
				n, err := reader.Read(dst)
				wantN := min(size, frames-position)
				if n != wantN || (wantN < size && !errors.Is(err, io.EOF)) || (wantN == size && err != nil) {
					t.Fatalf("位置 %d：读取 (%d, %v)，期望 %d 字节", position, n, err, wantN)
				}
				for ch := range dst {
					if !bytes.Equal(dst[ch][:n], want[ch][position:position+n]) {
						t.Fatalf("声道 %d 在位置 %d 的解交错或位序错误", ch, position)
					}
					if !bytes.Equal(dst[ch][n:], bytes.Repeat([]byte{0xee}, size-n)) {
						t.Fatalf("声道 %d 在有效末尾之后被写入", ch)
					}
				}
				position += n
			}
		})
	}
}

func TestDSFReadMasksInvalidTailBits(t *testing.T) {
	for _, order := range []dsdio.BitOrder{dsdio.LSBFirst, dsdio.MSBFirst} {
		for valid := uint64(1); valid <= 7; valid++ {
			data := readerTestDSF([][]byte{{0xff, 0xff}, {0, 0}}, 8+valid, order)
			reader, layout := readerTestOpen(t, data)
			if layout.Samples != 8+valid || layout.ByteFrames() != 2 {
				t.Fatalf("位序 %d，有效位 %d：长度错误 %+v", order, valid, layout)
			}
			dst := [][]byte{make([]byte, 3), make([]byte, 3)}
			n, err := reader.Read(dst)
			if n != 2 || !errors.Is(err, io.EOF) {
				t.Fatalf("位序 %d，有效位 %d：读取 (%d, %v)", order, valid, n, err)
			}
			mask := byte(0xff << (8 - valid))
			if dst[0][0] != 0xff || dst[1][0] != 0 || dst[0][1] != mask|(0x69 & ^mask) || dst[1][1] != 0x69 & ^mask {
				t.Fatalf("位序 %d，有效位 %d：无效位未填静音：%x", order, valid, dst)
			}
		}
	}
}

func TestDFFReadByteInterleaveAndChannelOrder(t *testing.T) {
	for _, tc := range []struct {
		name  string
		ids   []string
		order []int
		mask  uint32
	}{
		{"立体声", []string{"SLFT", "SRGT"}, []int{0, 1}, 3},
		{"重排5.1", []string{"LFE ", "RS  ", "MLFT", "C   ", "LS  ", "MRGT"}, []int{2, 5, 3, 0, 4, 1}, 0x3f},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var payload []byte
			for frame := 0; frame < 5; frame++ {
				for ch := range tc.ids {
					payload = append(payload, byte(0x80+ch*9+frame))
				}
			}
			reader, layout := readerTestOpen(t, readerTestDFF(tc.ids, payload))
			if layout.Container != dsdio.DFF || layout.Mask != tc.mask || layout.BitOrder != dsdio.MSBFirst || layout.Samples != 40 || layout.Rate != 2822400 {
				t.Fatalf("容器事实错误：%+v", layout)
			}
			dst := make([][]byte, len(tc.ids))
			for ch := range dst {
				dst[ch] = make([]byte, 6)
			}
			n, err := reader.Read(dst)
			if n != 5 || !errors.Is(err, io.EOF) {
				t.Fatalf("读取 (%d, %v)", n, err)
			}
			for ch, stored := range tc.order {
				for frame := 0; frame < n; frame++ {
					if dst[ch][frame] != byte(0x80+stored*9+frame) {
						t.Fatalf("输出声道 %d 帧 %d：得到 %02x", ch, frame, dst[ch][frame])
					}
				}
			}
		})
	}
}

func TestDSDSeekByteFrame(t *testing.T) {
	const frames = 4101
	channels := [][]byte{make([]byte, frames), make([]byte, frames)}
	var interleaved []byte
	for i := 0; i < frames; i++ {
		channels[0][i], channels[1][i] = byte(i*7), byte(i*13+3)
		interleaved = append(interleaved, channels[0][i], channels[1][i])
	}
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"DSF", readerTestDSF(channels, frames*8, dsdio.MSBFirst)},
		{"DFF", readerTestDFF([]string{"SLFT", "SRGT"}, interleaved)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader, _ := readerTestOpen(t, tc.data)
			for _, position := range []int64{4095, 4096, frames - 1, frames, 1, 0} {
				if err := reader.SeekByteFrame(position); err != nil {
					t.Fatal(err)
				}
				dst := [][]byte{make([]byte, 3), make([]byte, 3)}
				n, err := reader.Read(dst)
				wantN := min(3, frames-int(position))
				if n != wantN || (wantN < 3 && !errors.Is(err, io.EOF)) || (wantN == 3 && err != nil) {
					t.Fatalf("定位 %d 后读取 (%d, %v)", position, n, err)
				}
				for ch := range dst {
					if !bytes.Equal(dst[ch][:n], channels[ch][int(position):int(position)+n]) {
						t.Fatalf("定位 %d 后声道 %d 内容错误", position, ch)
					}
				}
			}
			for _, invalid := range []int64{-1, frames + 1} {
				if err := reader.SeekByteFrame(4096); err != nil {
					t.Fatal(err)
				}
				if err := reader.SeekByteFrame(invalid); err == nil {
					t.Fatalf("非法位置 %d 被接受", invalid)
				}
				dst := [][]byte{make([]byte, 1), make([]byte, 1)}
				if n, err := reader.Read(dst); n != 1 || err != nil {
					t.Fatalf("非法定位后读取 (%d, %v)", n, err)
				}
				if dst[0][0] != channels[0][4096] || dst[1][0] != channels[1][4096] {
					t.Fatal("非法定位改变了位置")
				}
			}
		})
	}
}

func TestDSFTruncatedChannelBlockReturnsUnexpectedEOF(t *testing.T) {
	channels := [][]byte{bytes.Repeat([]byte{0xa5}, 4096), bytes.Repeat([]byte{0x3c}, 4096)}
	full := readerTestDSF(channels, 4096*8, dsdio.MSBFirst)
	for _, probeBeforeTruncate := range []bool{false, true} {
		for _, available := range []int{0, 7} {
			truncated := full[:92+4096+available]
			probeData := truncated
			if probeBeforeTruncate {
				probeData = full
			}
			layout, err := dsdio.Probe(bytes.NewReader(probeData), int64(len(probeData)))
			if err != nil {
				t.Fatal(err)
			}
			// Probe 接受设计规定的范围与样本数补偿；错误只由读取不完整声道帧触发。
			if !probeBeforeTruncate {
				wantSamples := uint64((4096 + available) / 2 * 8)
				if layout.DataEnd != int64(len(truncated)) || layout.Samples != wantSamples {
					t.Fatalf("Probe 未按实际负载裁剪：%+v，期望 %d 个样本", layout, wantSamples)
				}
			}
			reader, err := dsdio.NewReader(bytes.NewReader(truncated), layout)
			if err != nil {
				t.Fatal(err)
			}
			dst := [][]byte{make([]byte, 32), make([]byte, 32)}
			n, err := reader.Read(dst)
			if n != available || !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("先探测=%v，第二声道仅有 %d 字节：读取 (%d, %v)，期望 UnexpectedEOF", probeBeforeTruncate, available, n, err)
			}
			if !bytes.Equal(dst[0][:n], channels[0][:n]) || !bytes.Equal(dst[1][:n], channels[1][:n]) {
				t.Fatal("截断前的完整帧内容错误")
			}
		}
	}
}

func readerTestOpen(t *testing.T, data []byte) (*dsdio.Reader, dsdio.Layout) {
	t.Helper()
	source := bytes.NewReader(data)
	layout, err := dsdio.Probe(source, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	reader, err := dsdio.NewReader(source, layout)
	if err != nil {
		t.Fatal(err)
	}
	return reader, layout
}

// readerTestDSF 的输入是文件位序下的各声道字节，物理尾块填入非零值以暴露越界读取。
func readerTestDSF(channels [][]byte, samples uint64, order dsdio.BitOrder) []byte {
	data := make([]byte, 92)
	copy(data, "DSD ")
	le := binary.LittleEndian
	le.PutUint64(data[4:12], 28)
	copy(data[28:32], "fmt ")
	le.PutUint64(data[32:40], 52)
	le.PutUint32(data[40:44], 1)
	le.PutUint32(data[48:52], 2)
	le.PutUint32(data[52:56], uint32(len(channels)))
	le.PutUint32(data[56:60], 2822400)
	le.PutUint32(data[60:64], uint32(order))
	le.PutUint64(data[64:72], samples)
	le.PutUint32(data[72:76], 4096)
	copy(data[80:84], "data")
	for start := 0; start < len(channels[0]); start += 4096 {
		for _, channel := range channels {
			block := bytes.Repeat([]byte{0xde}, 4096)
			copy(block, channel[start:min(start+4096, len(channel))])
			data = append(data, block...)
		}
	}
	le.PutUint64(data[12:20], uint64(len(data)))
	le.PutUint64(data[84:92], uint64(len(data)-80))
	return data
}

func readerTestDFF(ids []string, payload []byte) []byte {
	channels := binary.BigEndian.AppendUint16(nil, uint16(len(ids)))
	for _, id := range ids {
		channels = append(channels, id...)
	}
	props := []byte("SND ")
	props = append(props, readerTestDFFChunk("FS  ", binary.BigEndian.AppendUint32(nil, 2822400))...)
	props = append(props, readerTestDFFChunk("CHNL", channels)...)
	props = append(props, readerTestDFFChunk("CMPR", []byte{'D', 'S', 'D', ' ', 0})...)
	body := []byte("DSD ")
	body = append(body, readerTestDFFChunk("FVER", []byte{1, 5, 0, 0})...)
	body = append(body, readerTestDFFChunk("PROP", props)...)
	body = append(body, readerTestDFFChunk("DSD ", payload)...)
	return readerTestDFFChunk("FRM8", body)
}

func readerTestDFFChunk(id string, body []byte) []byte {
	data := binary.BigEndian.AppendUint64([]byte(id), uint64(len(body)))
	data = append(data, body...)
	if len(body)%2 != 0 {
		data = append(data, 0)
	}
	return data
}
