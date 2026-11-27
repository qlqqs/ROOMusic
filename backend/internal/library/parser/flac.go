package parser

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

func parseFLAC(file *os.File) (AudioObservation, error) {
	magic := make([]byte, 4)
	if _, err := io.ReadFull(file, magic); err != nil || string(magic) != "fLaC" {
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
		if _, err := io.ReadFull(file, header); err != nil {
			return observation, fmt.Errorf("read FLAC metadata: %w", err)
		}
		lastBlock = header[0]&0x80 != 0
		blockType := header[0] & 0x7f
		blockLength := int(header[1])<<16 | int(header[2])<<8 | int(header[3])
		if blockLength > 16<<20 {
			return observation, fmt.Errorf("FLAC metadata too large")
		}
		block := make([]byte, blockLength)
		if _, err := io.ReadFull(file, block); err != nil {
			return observation, err
		}
		switch blockType {
		case 0:
			if err := parseFLACStreamInfo(block, fileSize, &observation); err != nil {
				return observation, err
			}
			observation.ContentFingerprint = flacMD5Fingerprint(block)
		case 4:
			if err := parseVorbisComments(block, &observation); err != nil {
				return observation, err
			}
		case 6:
			if pictureType, mime, data, ok := parseFLACPicture(block); ok {
				offerArtwork(&observation, data, mime, pictureType)
			}
		}
	}
	finalizeTags(&observation)
	return observation, nil
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

func parseVorbisComments(block []byte, observation *AudioObservation) error {
	if len(block) < 4 {
		return nil
	}
	vendorLength := binary.LittleEndian.Uint32(block[:4])
	block = block[4:]
	if uint64(vendorLength) > uint64(len(block)) {
		return nil
	}
	block = block[vendorLength:]
	if len(block) < 4 {
		return nil
	}
	commentCount := binary.LittleEndian.Uint32(block[:4])
	block = block[4:]
	for index := uint32(0); index < commentCount; index++ {
		if len(block) < 4 {
			return fmt.Errorf("invalid Vorbis comment")
		}
		commentLength := binary.LittleEndian.Uint32(block[:4])
		block = block[4:]
		if uint64(commentLength) > uint64(len(block)) {
			return fmt.Errorf("invalid Vorbis comment")
		}
		key, value, found := bytes.Cut(block[:commentLength], []byte("="))
		block = block[commentLength:]
		picture := found && bytes.EqualFold(key, []byte("METADATA_BLOCK_PICTURE"))
		if commentLength > 1<<20 && !picture {
			return fmt.Errorf("invalid Vorbis comment")
		}
		if !found {
			continue
		}
		if picture {
			decoded, err := base64.StdEncoding.DecodeString(string(value))
			if err == nil {
				if pictureType, mime, data, ok := parseFLACPicture(decoded); ok {
					offerArtwork(observation, data, mime, pictureType)
				}
			}
			continue
		}
		if err := setAudioTag(observation, string(key), string(value)); err != nil {
			return err
		}
	}
	return nil
}

func parseFLACPicture(b []byte) (pictureType int, mime string, data []byte, ok bool) {
	if len(b) < 8 {
		return 0, "", nil, false
	}
	pictureType = int(binary.BigEndian.Uint32(b[:4]))
	mimeLength := binary.BigEndian.Uint32(b[4:8])
	b = b[8:]
	if uint64(mimeLength) > uint64(len(b)) {
		return 0, "", nil, false
	}
	mime = string(b[:mimeLength])
	if mime == "-->" {
		return 0, "", nil, false
	}
	b = b[mimeLength:]
	if len(b) < 4 {
		return 0, "", nil, false
	}
	descriptionLength := binary.BigEndian.Uint32(b[:4])
	b = b[4:]
	if uint64(descriptionLength) > uint64(len(b)) {
		return 0, "", nil, false
	}
	b = b[descriptionLength:]
	if len(b) < 20 {
		return 0, "", nil, false
	}
	dataLength := binary.BigEndian.Uint32(b[16:20])
	b = b[20:]
	if uint64(dataLength) > uint64(len(b)) {
		return 0, "", nil, false
	}
	return pictureType, mime, b[:dataLength], true
}
