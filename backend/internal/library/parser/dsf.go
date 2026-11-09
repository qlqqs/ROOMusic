package parser

import (
	"io"

	"github.com/qlqq/roomusic/backend/internal/playback/dsdio"
)

func parseDSF(path string) (AudioObservation, error) {
	return parseDSDFile(path, dsdio.DSF, readDSFTags)
}

func readDSFTags(reader io.ReaderAt, size int64, layout dsdio.Layout, observation *AudioObservation) error {
	// 只认可音频之后的标签；坏指针不能让音频中的偶然 ID3 字节成为元数据。
	if layout.MetadataOffset >= uint64(layout.DataEnd) && layout.MetadataOffset <= uint64(size) {
		position := int64(layout.MetadataOffset)
		ok, err := hasDSFID3(reader, position, size)
		if err != nil {
			return err
		}
		if ok {
			return readDSDID3(reader, position, size, observation)
		}
	}
	// 零指针、越界指针或非 ID3 指针，仅回退到 data 块末尾，不扫描音频猜标签。
	ok, err := hasDSFID3(reader, layout.DataEnd, size)
	if err != nil {
		return err
	}
	if ok {
		return readDSDID3(reader, layout.DataEnd, size, observation)
	}
	return nil
}

func hasDSFID3(reader io.ReaderAt, position, size int64) (bool, error) {
	if position < 0 || position > size || size-position < 3 {
		return false, nil
	}
	var magic [3]byte
	if _, err := reader.ReadAt(magic[:], position); err != nil {
		return false, err
	}
	return string(magic[:]) == "ID3", nil
}
