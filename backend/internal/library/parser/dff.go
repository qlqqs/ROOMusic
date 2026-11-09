package parser

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/qlqq/roomusic/backend/internal/playback/dsdio"
)

func parseDFF(path string) (AudioObservation, error) {
	return parseDSDFile(path, dsdio.DFF, readDFFTags)
}

func readDFFTags(reader io.ReaderAt, _ int64, layout dsdio.Layout, observation *AudioObservation) error {
	var title, artist string
	var foundID3 bool
	err := dsdio.WalkDFFChunks(reader, 16, layout.ContainerEnd, func(chunk dsdio.Chunk) error {
		switch chunk.ID {
		case "ID3 ":
			if foundID3 {
				return fmt.Errorf("duplicate DFF ID3 chunk")
			}
			foundID3 = true
			return readDSDID3(reader, chunk.Start, chunk.End, observation)
		case "DIIN":
			return dsdio.WalkDFFChunks(reader, chunk.Start, chunk.End, func(child dsdio.Chunk) error {
				if child.ID != "DITI" && child.ID != "DIAR" {
					return nil
				}
				value, err := readDFFText(reader, child)
				if err != nil {
					return err
				}
				if child.ID == "DITI" {
					title = value
				} else {
					artist = value
				}
				return nil
			})
		}
		return nil
	})
	if err != nil {
		return err
	}
	// 不依赖物理块顺序：ID3 的字段优先，DIIN 只填空缺，不混入被覆盖的艺人证据。
	if observation.Title == "" {
		if err := setAudioTag(observation, "TITLE", title); err != nil {
			return err
		}
	}
	if observation.Artist == "" {
		if err := setAudioTag(observation, "ARTIST", artist); err != nil {
			return err
		}
	}
	return nil
}

func readDFFText(reader io.ReaderAt, chunk dsdio.Chunk) (string, error) {
	if chunk.End-chunk.Start < 4 {
		return "", fmt.Errorf("truncated DFF text header")
	}
	var header [4]byte
	if _, err := reader.ReadAt(header[:], chunk.Start); err != nil {
		return "", err
	}
	length := int64(binary.BigEndian.Uint32(header[:]))
	if length > 16<<20 || length > chunk.End-chunk.Start-4 {
		return "", fmt.Errorf("invalid DFF text length")
	}
	data := make([]byte, length)
	if _, err := reader.ReadAt(data, chunk.Start+4); err != nil {
		return "", err
	}
	return string(bytes.TrimRight(data, "\x00")), nil
}
