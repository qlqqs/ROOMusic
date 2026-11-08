package parser

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

type apeHeader struct {
	sampleRate, channels, bitDepth int
	totalSamples                   uint64
	frameStart, frameEnd           int64
	md5                            [16]byte
}

func parseAPE(path string) (AudioObservation, error) {
	file, err := os.Open(path)
	if err != nil {
		return AudioObservation{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return AudioObservation{}, err
	}
	header, err := readAPEHeader(file, info.Size())
	if err != nil {
		return AudioObservation{}, err
	}
	observation := AudioObservation{
		SourceKind: "ape_apev2", Codec: "ape", SampleRate: header.sampleRate,
		Channels: header.channels, BitDepth: header.bitDepth,
		DurationSeconds: float64(header.totalSamples) / float64(header.sampleRate),
	}
	observation.Bitrate = estimatedBitrateKbps(info.Size(), observation.DurationSeconds)
	observation.ContentFingerprint = apeMD5Fingerprint(header)
	if err := readAPEv2(file, info.Size(), &observation); err != nil {
		return AudioObservation{}, err
	}
	return applyFilenameFallback(path, observation), nil
}

func apeStreamStart(reader io.ReaderAt, size int64) (int64, error) {
	var header [10]byte
	if size < 10 {
		return 0, fmt.Errorf("truncated APE stream")
	}
	if _, err := reader.ReadAt(header[:], 0); err != nil {
		return 0, err
	}
	if string(header[:3]) != "ID3" {
		return 0, nil
	}
	if header[6]|header[7]|header[8]|header[9] >= 0x80 {
		return 0, fmt.Errorf("invalid APE ID3 size")
	}
	length := int64(10) + (int64(header[6])<<21 | int64(header[7])<<14 | int64(header[8])<<7 | int64(header[9]))
	if length > 16<<20 {
		return 0, fmt.Errorf("APE ID3 metadata too large")
	}
	if header[5]&0x10 != 0 {
		length += 10
	}
	if length > size {
		return 0, fmt.Errorf("truncated APE ID3 tag")
	}
	return length, nil
}

func readAPEHeader(reader io.ReaderAt, size int64) (apeHeader, error) {
	var result apeHeader
	start, err := apeStreamStart(reader, size)
	if err != nil {
		return result, err
	}
	var prefix [6]byte
	if _, err := reader.ReadAt(prefix[:], start); err != nil || string(prefix[:4]) != "MAC " {
		return result, fmt.Errorf("invalid APE stream")
	}
	version := binary.LittleEndian.Uint16(prefix[4:])
	if version < 3800 || version > 9999 {
		return result, fmt.Errorf("unsupported APE version")
	}
	var blocksPerFrame, finalFrameBlocks, totalFrames uint32
	if version >= 3980 {
		var descriptor [52]byte
		if _, err := reader.ReadAt(descriptor[:], start); err != nil {
			return result, fmt.Errorf("read APE descriptor: %w", err)
		}
		descriptorBytes := int64(binary.LittleEndian.Uint32(descriptor[8:12]))
		headerBytes := int64(binary.LittleEndian.Uint32(descriptor[12:16]))
		seekBytes := int64(binary.LittleEndian.Uint32(descriptor[16:20]))
		wavBytes := int64(binary.LittleEndian.Uint32(descriptor[20:24]))
		frameBytes := uint64(binary.LittleEndian.Uint32(descriptor[24:28])) | uint64(binary.LittleEndian.Uint32(descriptor[28:32]))<<32
		terminatingBytes := int64(binary.LittleEndian.Uint32(descriptor[32:36]))
		if descriptorBytes < 52 || headerBytes < 24 || descriptorBytes > size-start || headerBytes > size-start-descriptorBytes {
			return result, fmt.Errorf("invalid APE header size")
		}
		var header [24]byte
		if _, err := reader.ReadAt(header[:], start+descriptorBytes); err != nil {
			return result, fmt.Errorf("read APE header: %w", err)
		}
		blocksPerFrame = binary.LittleEndian.Uint32(header[4:8])
		finalFrameBlocks = binary.LittleEndian.Uint32(header[8:12])
		totalFrames = binary.LittleEndian.Uint32(header[12:16])
		result.bitDepth = int(binary.LittleEndian.Uint16(header[16:18]))
		result.channels = int(binary.LittleEndian.Uint16(header[18:20]))
		result.sampleRate = int(binary.LittleEndian.Uint32(header[20:24]))
		result.frameStart = start + descriptorBytes + headerBytes + seekBytes + wavBytes
		if result.frameStart > size || frameBytes > uint64(size-result.frameStart) || terminatingBytes > size-result.frameStart-int64(frameBytes) {
			return result, fmt.Errorf("truncated APE frame data")
		}
		result.frameEnd = result.frameStart + int64(frameBytes)
		copy(result.md5[:], descriptor[36:52])
	} else {
		var header [32]byte
		if _, err := reader.ReadAt(header[:], start); err != nil {
			return result, fmt.Errorf("read legacy APE header: %w", err)
		}
		compression := binary.LittleEndian.Uint16(header[6:8])
		flags := binary.LittleEndian.Uint16(header[8:10])
		result.channels = int(binary.LittleEndian.Uint16(header[10:12]))
		result.sampleRate = int(binary.LittleEndian.Uint32(header[12:16]))
		wavBytes := int64(binary.LittleEndian.Uint32(header[16:20]))
		totalFrames = binary.LittleEndian.Uint32(header[24:28])
		finalFrameBlocks = binary.LittleEndian.Uint32(header[28:32])
		result.bitDepth = 16
		if flags&1 != 0 {
			result.bitDepth = 8
		} else if flags&8 != 0 {
			result.bitDepth = 24
		}
		blocksPerFrame = 9216
		if version >= 3950 {
			blocksPerFrame = 294912
		} else if version >= 3900 || compression == 4000 {
			blocksPerFrame = 73728
		}
		position := start + 32
		if flags&4 != 0 {
			position += 4
		}
		seekBytes := int64(totalFrames) * 4
		if flags&0x10 != 0 {
			var count [4]byte
			if _, err := reader.ReadAt(count[:], position); err != nil {
				return result, fmt.Errorf("read APE seek count: %w", err)
			}
			seekBytes = int64(binary.LittleEndian.Uint32(count[:])) * 4
			position += 4
		}
		if flags&0x20 == 0 {
			position += wavBytes
		}
		if seekBytes < int64(totalFrames)*4 || seekBytes < 4 || position > size || seekBytes > size-position {
			return result, fmt.Errorf("invalid APE seek table")
		}
		var firstSeek [4]byte
		if _, err := reader.ReadAt(firstSeek[:], position); err != nil {
			return result, fmt.Errorf("read APE first seek: %w", err)
		}
		result.frameStart = start + int64(binary.LittleEndian.Uint32(firstSeek[:]))
		_, result.frameEnd, _, err = locateAPEv2(reader, size)
		if err != nil {
			return result, err
		}
		if result.frameStart < position+seekBytes || result.frameStart > result.frameEnd {
			return result, fmt.Errorf("invalid APE frame range")
		}
	}
	if result.channels == 0 || result.sampleRate == 0 || totalFrames == 0 || blocksPerFrame == 0 || finalFrameBlocks == 0 || finalFrameBlocks > blocksPerFrame {
		return result, fmt.Errorf("invalid APE audio facts")
	}
	if result.bitDepth != 8 && result.bitDepth != 16 && result.bitDepth != 24 && result.bitDepth != 32 {
		return result, fmt.Errorf("invalid APE bit depth")
	}
	result.totalSamples = uint64(totalFrames-1)*uint64(blocksPerFrame) + uint64(finalFrameBlocks)
	return result, nil
}
