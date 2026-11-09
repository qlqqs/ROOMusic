package parser

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/qlqq/roomusic/backend/internal/playback/dsdio"
)

// ErrUnsupportedEncoding 表示已识别容器中的编码不受支持；不是容器损坏或读取失败。
// 扫描接入时通过 errors.Is 将其映射为 unsupported_format。
var ErrUnsupportedEncoding = errors.New("unsupported audio encoding")

func parseDSDFile(path string, container dsdio.Container, tags func(io.ReaderAt, int64, dsdio.Layout, *AudioObservation) error) (AudioObservation, error) {
	file, err := os.Open(path)
	if err != nil {
		return AudioObservation{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return AudioObservation{}, err
	}
	layout, err := probeDSDContainer(file, info.Size(), container)
	if err != nil {
		return AudioObservation{}, err
	}
	kind := "dsf_id3"
	if container == dsdio.DFF {
		kind = "dff_diin"
	}
	observation := AudioObservation{
		SourceKind: kind, Codec: string(container), BitDepth: 1,
		SampleRate: layout.Rate, Channels: layout.Channels,
		DurationSeconds: float64(layout.Samples) / float64(layout.Rate),
		// 沿用观察值的 kbps 单位，不把 DSD 比特率直接写成 bps。
		Bitrate: int(int64(layout.Rate) * int64(layout.Channels) / 1000),
	}
	if err := tags(file, info.Size(), layout, &observation); err != nil {
		return AudioObservation{}, err
	}
	finalizeTags(&observation)
	return applyFilenameFallback(path, observation), nil
}

func probeDSDContainer(reader io.ReaderAt, size int64, expected dsdio.Container) (dsdio.Layout, error) {
	// 先核对预期签名，避免扩展名错误的文件被另一容器的编码错误误分类。
	var signature [4]byte
	if size < 4 {
		return dsdio.Layout{}, fmt.Errorf("truncated %s stream", expected)
	}
	if _, err := reader.ReadAt(signature[:], 0); err != nil {
		return dsdio.Layout{}, err
	}
	if (expected == dsdio.DSF && string(signature[:]) != "DSD ") || (expected == dsdio.DFF && string(signature[:]) != "FRM8") {
		return dsdio.Layout{}, fmt.Errorf("invalid %s stream", expected)
	}
	layout, err := dsdio.Probe(reader, size)
	if err != nil {
		var unsupported *dsdio.UnsupportedEncodingError
		if errors.As(err, &unsupported) {
			return dsdio.Layout{}, fmt.Errorf("%w: %w", ErrUnsupportedEncoding, err)
		}
		return dsdio.Layout{}, err
	}
	return layout, nil
}

// readDSDID3 只读取一个有界 ID3 标签体，保留现有帧解析、封面、歌词与署名规则。
func readDSDID3(reader io.ReaderAt, start, end int64, observation *AudioObservation) error {
	if start < 0 || end-start < 10 {
		return fmt.Errorf("truncated DSD ID3 header")
	}
	var header [10]byte
	if _, err := reader.ReadAt(header[:], start); err != nil {
		return fmt.Errorf("read DSD ID3 header: %w", err)
	}
	if string(header[:3]) != "ID3" || header[6]|header[7]|header[8]|header[9] >= 0x80 {
		return fmt.Errorf("invalid DSD ID3 header")
	}
	length := int64(header[6])<<21 | int64(header[7])<<14 | int64(header[8])<<7 | int64(header[9])
	total := 10 + length
	if header[3] == 4 && header[5]&0x10 != 0 {
		total += 10
	}
	if total > 16<<20 {
		return fmt.Errorf("DSD ID3 metadata too large")
	}
	if total > end-start {
		return fmt.Errorf("truncated DSD ID3 tag")
	}
	data := make([]byte, length)
	if _, err := reader.ReadAt(data, start+10); err != nil {
		return fmt.Errorf("read DSD ID3 frames: %w", err)
	}
	return parseID3Frames(data, observation, header[3], header[5])
}
