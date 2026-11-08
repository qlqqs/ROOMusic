package parser

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
)

type aiffChunk struct {
	id          string
	start, size int64
}

func parseAIFF(path string) (AudioObservation, error) {
	file, err := os.Open(path)
	if err != nil {
		return AudioObservation{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return AudioObservation{}, err
	}
	observation := AudioObservation{SourceKind: "aiff_id3", Codec: "aiff"}
	var foundCOMM, foundSSND bool
	var name, author string
	err = walkAIFFChunks(context.Background(), file, info.Size(), func(chunk aiffChunk, compressed bool) error {
		switch chunk.id {
		case "COMM":
			if foundCOMM {
				return fmt.Errorf("duplicate AIFF COMM chunk")
			}
			comm, err := readAIFFCOMM(file, chunk, compressed)
			if err != nil {
				return err
			}
			if err := applyAIFFCOMM(comm, &observation); err != nil {
				return err
			}
			foundCOMM = true
		case "SSND":
			if _, _, err := aiffSoundRange(file, chunk); err != nil {
				return err
			}
			foundSSND = true
		case "ID3 ", "id3 ", "NAME", "AUTH":
			if chunk.size > 16<<20 {
				return fmt.Errorf("AIFF metadata too large")
			}
			data := make([]byte, chunk.size)
			if _, err := file.ReadAt(data, chunk.start); err != nil {
				return fmt.Errorf("read AIFF metadata: %w", err)
			}
			switch chunk.id {
			case "ID3 ", "id3 ":
				if err := parseAIFFID3(data, &observation); err != nil {
					return err
				}
			case "NAME":
				name = string(bytes.TrimRight(data, "\x00"))
			case "AUTH":
				author = string(bytes.TrimRight(data, "\x00"))
			}
		}
		return nil
	})
	if err != nil {
		return AudioObservation{}, err
	}
	if !foundCOMM || !foundSSND {
		return AudioObservation{}, fmt.Errorf("invalid AIFF stream: missing audio chunks")
	}
	if observation.Title == "" {
		if err := setAudioTag(&observation, "TITLE", name); err != nil {
			return AudioObservation{}, err
		}
	}
	if observation.Artist == "" {
		if err := setAudioTag(&observation, "ARTIST", author); err != nil {
			return AudioObservation{}, err
		}
	}
	observation.Bitrate = estimatedBitrateKbps(info.Size(), observation.DurationSeconds)
	return applyFilenameFallback(path, observation), nil
}

func walkAIFFChunks(ctx context.Context, reader io.ReaderAt, size int64, visit func(aiffChunk, bool) error) error {
	var header [12]byte
	if _, err := reader.ReadAt(header[:], 0); err != nil || string(header[:4]) != "FORM" || (string(header[8:12]) != "AIFF" && string(header[8:12]) != "AIFC") {
		return fmt.Errorf("invalid AIFF stream")
	}
	end := int64(binary.BigEndian.Uint32(header[4:8])) + 8
	if end < 12 || end > size {
		return fmt.Errorf("truncated AIFF FORM")
	}
	compressed := string(header[8:12]) == "AIFC"
	for position := int64(12); position < end; {
		if err := ctx.Err(); err != nil {
			return err
		}
		var chunkHeader [8]byte
		if end-position < 8 {
			return fmt.Errorf("truncated AIFF chunk header")
		}
		if _, err := reader.ReadAt(chunkHeader[:], position); err != nil {
			return fmt.Errorf("read AIFF chunk header: %w", err)
		}
		length := int64(binary.BigEndian.Uint32(chunkHeader[4:8]))
		position += 8
		padded := length + length%2
		if padded > end-position {
			return fmt.Errorf("truncated AIFF chunk")
		}
		if err := visit(aiffChunk{id: string(chunkHeader[:4]), start: position, size: length}, compressed); err != nil {
			return err
		}
		position += padded
	}
	return nil
}

// readAIFFCOMM 只保留音频参数；AIFC 后面的压缩名称不参与内容身份。
func readAIFFCOMM(reader io.ReaderAt, chunk aiffChunk, compressed bool) ([]byte, error) {
	length := int64(18)
	if compressed {
		length = 22
	}
	if chunk.size < length || chunk.size > 16<<20 {
		return nil, fmt.Errorf("invalid AIFF COMM size")
	}
	data := make([]byte, length)
	if _, err := reader.ReadAt(data, chunk.start); err != nil {
		return nil, fmt.Errorf("read AIFF COMM: %w", err)
	}
	return data, nil
}

func applyAIFFCOMM(data []byte, observation *AudioObservation) error {
	observation.Channels = int(binary.BigEndian.Uint16(data[:2]))
	frames := binary.BigEndian.Uint32(data[2:6])
	observation.BitDepth = int(binary.BigEndian.Uint16(data[6:8]))
	exponent := binary.BigEndian.Uint16(data[8:10])
	mantissa := binary.BigEndian.Uint64(data[10:18])
	if exponent&0x8000 != 0 || exponent == 0x7fff || mantissa>>63 == 0 {
		return fmt.Errorf("invalid AIFF sample rate")
	}
	rate := math.Ldexp(float64(mantissa), int(exponent)-16383-63)
	if math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 1 || rate > math.MaxInt32 {
		return fmt.Errorf("invalid AIFF sample rate")
	}
	observation.SampleRate = int(math.Round(rate))
	if observation.Channels == 0 || observation.BitDepth == 0 {
		return fmt.Errorf("invalid AIFF audio facts")
	}
	observation.DurationSeconds = float64(frames) / float64(observation.SampleRate)
	observation.Codec = "aiff"
	if len(data) == 22 {
		compression := strings.ToLower(strings.TrimSpace(string(data[18:22])))
		switch compression {
		case "none", "twos", "sowt":
		case "fl32", "fl64":
			observation.Codec = "aiff_float"
		default:
			observation.Codec = "aifc_" + compression
		}
	}
	return nil
}

func aiffSoundRange(reader io.ReaderAt, chunk aiffChunk) (int64, int64, error) {
	if chunk.size < 8 {
		return 0, 0, fmt.Errorf("invalid AIFF SSND size")
	}
	var soundHeader [8]byte
	if _, err := reader.ReadAt(soundHeader[:], chunk.start); err != nil {
		return 0, 0, fmt.Errorf("read AIFF SSND: %w", err)
	}
	offset := int64(binary.BigEndian.Uint32(soundHeader[:4]))
	if offset > chunk.size-8 {
		return 0, 0, fmt.Errorf("invalid AIFF SSND offset")
	}
	return chunk.start + 8 + offset, chunk.start + chunk.size, nil
}

func parseAIFFID3(data []byte, observation *AudioObservation) error {
	if len(data) < 10 || string(data[:3]) != "ID3" || data[6]|data[7]|data[8]|data[9] >= 0x80 {
		return fmt.Errorf("invalid AIFF ID3 header")
	}
	length := int(data[6])<<21 | int(data[7])<<14 | int(data[8])<<7 | int(data[9])
	end := 10 + length
	if data[5]&0x10 != 0 {
		end += 10
	}
	if end > len(data) {
		return fmt.Errorf("truncated AIFF ID3 tag")
	}
	return parseID3Frames(data[10:10+length], observation, data[3], data[5])
}
