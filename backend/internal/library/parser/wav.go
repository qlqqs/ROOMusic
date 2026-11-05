package parser

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strings"
)

func parseWAV(path string) (AudioObservation, error) {
	file, err := os.Open(path)
	if err != nil {
		return AudioObservation{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return AudioObservation{}, err
	}
	var header [12]byte
	if _, err := io.ReadFull(file, header[:]); err != nil || string(header[:4]) != "RIFF" || string(header[8:12]) != "WAVE" {
		return AudioObservation{}, fmt.Errorf("invalid WAV stream")
	}
	declaredEnd := int64(binary.LittleEndian.Uint32(header[4:8])) + 8
	if declaredEnd < 12 || declaredEnd > info.Size() {
		return AudioObservation{}, fmt.Errorf("truncated WAV stream")
	}
	o := AudioObservation{SourceKind: "wav_info", InferredFields: map[string]bool{}, Codec: "wav"}
	var byteRate int
	var foundFormat bool
	var dataBytes int64
	for position := int64(12); position+8 <= declaredEnd; {
		var chunkHeader [8]byte
		if _, err := io.ReadFull(file, chunkHeader[:]); err != nil {
			return o, fmt.Errorf("truncated WAV chunk header")
		}
		position += 8
		id := string(chunkHeader[:4])
		size := int64(binary.LittleEndian.Uint32(chunkHeader[4:8]))
		paddedSize := size + size%2
		if size < 0 || paddedSize < size || position+paddedSize > declaredEnd {
			return o, fmt.Errorf("truncated WAV chunk")
		}
		switch id {
		case "fmt ":
			if size > 1<<20 {
				return o, fmt.Errorf("WAV format metadata too large")
			}
			chunk := make([]byte, int(size))
			if _, err := io.ReadFull(file, chunk); err != nil {
				return o, fmt.Errorf("truncated WAV format chunk")
			}
			if err := parseWAVFormat(chunk, &o, &byteRate); err != nil {
				return o, err
			}
			foundFormat = true
		case "LIST":
			if size > 16<<20 {
				return o, fmt.Errorf("WAV metadata too large")
			}
			chunk := make([]byte, int(size))
			if _, err := io.ReadFull(file, chunk); err != nil {
				return o, fmt.Errorf("truncated WAV LIST chunk")
			}
			if err := parseWAVInfo(chunk, &o); err != nil {
				return o, err
			}
		case "data":
			dataBytes += size
			if _, err := file.Seek(size, io.SeekCurrent); err != nil {
				return o, fmt.Errorf("seek WAV data: %w", err)
			}
		default:
			if _, err := file.Seek(size, io.SeekCurrent); err != nil {
				return o, fmt.Errorf("seek WAV chunk: %w", err)
			}
		}
		if size%2 == 1 {
			if _, err := file.Seek(1, io.SeekCurrent); err != nil {
				return o, fmt.Errorf("seek WAV padding: %w", err)
			}
		}
		position += paddedSize
	}
	if !foundFormat || dataBytes <= 0 {
		return AudioObservation{}, fmt.Errorf("invalid WAV stream: missing audio chunks")
	}
	if byteRate > 0 && dataBytes > 0 {
		o.DurationSeconds = float64(dataBytes) / float64(byteRate)
		o.Bitrate = byteRate * 8 / 1000
	}
	return applyFilenameFallback(path, o), nil
}

func parseWAVFormat(chunk []byte, observation *AudioObservation, byteRate *int) error {
	if len(chunk) < 16 {
		return fmt.Errorf("invalid WAV format chunk")
	}
	format := binary.LittleEndian.Uint16(chunk[:2])
	observation.Channels = int(binary.LittleEndian.Uint16(chunk[2:4]))
	observation.SampleRate = int(binary.LittleEndian.Uint32(chunk[4:8]))
	*byteRate = int(binary.LittleEndian.Uint32(chunk[8:12]))
	observation.BitDepth = int(binary.LittleEndian.Uint16(chunk[14:16]))
	switch format {
	case 1:
		observation.Codec = "pcm"
	case 3:
		observation.Codec = "ieee_float"
	case 0xfffe:
		observation.Codec = "wav_extensible"
	default:
		observation.Codec = fmt.Sprintf("wav_%d", format)
	}
	if observation.Channels < 1 || observation.SampleRate < 1 || *byteRate < 1 {
		return fmt.Errorf("invalid WAV audio facts")
	}
	return nil
}

func parseWAVInfo(chunk []byte, observation *AudioObservation) error {
	if len(chunk) < 4 || string(chunk[:4]) != "INFO" {
		return nil
	}
	for position := 4; position+8 <= len(chunk); {
		key := string(chunk[position : position+4])
		size := int(binary.LittleEndian.Uint32(chunk[position+4 : position+8]))
		position += 8
		if size < 0 || position+size > len(chunk) {
			return nil
		}
		raw := string(bytes.TrimRight(chunk[position:position+size], "\x00"))
		if err := captureArtistTag(observation, key, raw); err != nil {
			return err
		}
		value := strings.TrimSpace(raw)
		position += size
		if size%2 == 1 {
			position++
		}
		switch key {
		case "INAM":
			observation.Title = value
		case "IPRD":
			observation.Album = value
		}
	}
	return nil
}
