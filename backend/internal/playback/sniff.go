package playback

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
)

// maxLeadingMP4Atoms 是为找到 ftyp 最多跳过的顶层 atom 数。扫描器的解析器接受 ftyp 之前有别的顶层 atom，
// 所以这里不能只看偏移 4；上限让畸形文件不能让核对无界循环。
const maxLeadingMP4Atoms = 8

// matchesContainer 读取文件头，报告文件是否具有 kind 容器的标识。它与扫描器解析器的接受范围一致
// （FLAC 与 Ogg 以魔数开头，MP3 以 ID3 或帧同步开头，WAV 是 RIFF/WAVE，MP4 在顶层有 ftyp），
// 所以扫描时解析成功的文件不会在这里被误判。读取失败（不是文件太短）返回错误。
func matchesContainer(source io.ReaderAt, size int64, kind container) (bool, error) {
	if kind == containerMP4 {
		return hasMP4FileType(source, size)
	}
	var header [12]byte
	read, err := source.ReadAt(header[:], 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	head := header[:read]
	switch kind {
	case containerFLAC:
		return bytes.HasPrefix(head, []byte("fLaC")), nil
	case containerOgg:
		return bytes.HasPrefix(head, []byte("OggS")), nil
	case containerMP3:
		return bytes.HasPrefix(head, []byte("ID3")) || (len(head) >= 2 && head[0] == 0xff && head[1]&0xe0 == 0xe0), nil
	case containerWAV:
		return len(head) == len(header) && bytes.Equal(head[:4], []byte("RIFF")) && bytes.Equal(head[8:12], []byte("WAVE")), nil
	}
	return false, nil
}

func hasMP4FileType(source io.ReaderAt, size int64) (bool, error) {
	offset := int64(0)
	for atoms := 0; atoms < maxLeadingMP4Atoms && offset+8 <= size; atoms++ {
		var header [16]byte
		read, err := source.ReadAt(header[:8], offset)
		if err != nil && !(errors.Is(err, io.EOF) && read == 8) {
			if errors.Is(err, io.EOF) {
				return false, nil
			}
			return false, err
		}
		if bytes.Equal(header[4:8], []byte("ftyp")) {
			return true, nil
		}
		atomSize := int64(binary.BigEndian.Uint32(header[0:4]))
		headerLength := int64(8)
		switch atomSize {
		case 0: // 延伸到文件末尾，后面不会再有顶层 atom。
			return false, nil
		case 1: // 64 位大小。
			read, err = source.ReadAt(header[8:16], offset+8)
			if err != nil && !(errors.Is(err, io.EOF) && read == 8) {
				if errors.Is(err, io.EOF) {
					return false, nil
				}
				return false, err
			}
			largeSize := binary.BigEndian.Uint64(header[8:16])
			if largeSize > uint64(size) {
				return false, nil
			}
			atomSize, headerLength = int64(largeSize), 16
		}
		if atomSize < headerLength {
			return false, nil
		}
		offset += atomSize
	}
	return false, nil
}
