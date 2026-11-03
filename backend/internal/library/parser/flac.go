package parser

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strings"
)

func parseFLAC(path string) (AudioObservation, error) {
	file, err := os.Open(path)
	if err != nil {
		return AudioObservation{}, err
	}
	defer file.Close()
	magic := make([]byte, 4)
	if _, err = io.ReadFull(file, magic); err != nil || string(magic) != "fLaC" {
		return AudioObservation{}, fmt.Errorf("invalid FLAC stream")
	}
	observation := AudioObservation{SourceKind: "flac_vorbis_comment", InferredFields: make(map[string]bool), Codec: "flac"}
	var fileSize int64
	if info, statErr := file.Stat(); statErr == nil {
		fileSize = info.Size()
	}
	lastBlock := false
	for !lastBlock {
		header := make([]byte, 4)
		if _, err = io.ReadFull(file, header); err != nil {
			return observation, fmt.Errorf("read FLAC metadata: %w", err)
		}
		lastBlock = header[0]&0x80 != 0
		blockType := header[0] & 0x7f
		blockLength := int(header[1])<<16 | int(header[2])<<8 | int(header[3])
		if blockLength > 16<<20 {
			return observation, fmt.Errorf("FLAC metadata too large")
		}
		block := make([]byte, blockLength)
		if _, err = io.ReadFull(file, block); err != nil {
			return observation, err
		}
		switch blockType {
		case 0:
			if err := parseFLACStreamInfo(block, fileSize, &observation); err != nil {
				return observation, err
			}
			observation.ContentFingerprint = flacMD5Fingerprint(block)
		case 4:
			parseVorbisComments(block, &observation)
		}
	}
	return applyFilenameFallback(path, observation), nil
}

func parseFLACStreamInfo(block []byte, fileSize int64, observation *AudioObservation) error {
	if len(block) != 34 {
		return fmt.Errorf("invalid FLAC STREAMINFO length")
	}
	sampleRate, channels, bitDepth, totalSamples := decodeFLACStreamInfo(block)
	if sampleRate <= 0 || channels <= 0 || bitDepth <= 0 {
		return fmt.Errorf("invalid FLAC STREAMINFO facts")
	}
	observation.SampleRate = sampleRate
	observation.Channels = channels
	observation.BitDepth = bitDepth
	if totalSamples > 0 {
		observation.DurationSeconds = float64(totalSamples) / float64(sampleRate)
	}
	if fileSize > 0 && observation.DurationSeconds > 0 {
		observation.Bitrate = estimatedBitrateKbps(fileSize, observation.DurationSeconds)
	}
	return nil
}

// decodeFLACStreamInfo 解出 STREAMINFO 的采样率、声道数、位深和总样本数；block 必须是 34 字节。
func decodeFLACStreamInfo(block []byte) (sampleRate, channels, bitDepth int, totalSamples int64) {
	sampleRate = int(block[10])<<12 | int(block[11])<<4 | int(block[12]>>4)
	channels = int((block[12]>>1)&0x07) + 1
	bitDepth = (int(block[12]&0x01)<<4 | int(block[13]>>4)) + 1
	totalSamples = int64(block[13]&0x0f)<<32 | int64(binary.BigEndian.Uint32(block[14:18]))
	return sampleRate, channels, bitDepth, totalSamples
}

func parseVorbisComments(block []byte, observation *AudioObservation) {
	reader := bufio.NewReader(strings.NewReader(string(block)))
	var vendorLength uint32
	if binary.Read(reader, binary.LittleEndian, &vendorLength) != nil {
		return
	}
	if vendorLength > uint32(len(block)) {
		return
	}
	commentVendor := make([]byte, vendorLength)
	if _, err := io.ReadFull(reader, commentVendor); err != nil {
		return
	}
	var commentCount uint32
	if binary.Read(reader, binary.LittleEndian, &commentCount) != nil {
		return
	}
	for index := uint32(0); index < commentCount; index++ {
		var commentLength uint32
		if binary.Read(reader, binary.LittleEndian, &commentLength) != nil || commentLength > 1<<20 {
			return
		}
		value := make([]byte, commentLength)
		if _, err := io.ReadFull(reader, value); err != nil {
			return
		}
		keyValue := strings.SplitN(string(value), "=", 2)
		if len(keyValue) != 2 {
			continue
		}
		setAudioTag(observation, keyValue[0], keyValue[1])
	}
}
