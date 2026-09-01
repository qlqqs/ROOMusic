package main

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type audioObservation struct {
	Title, Artist, Album    string
	TrackNumber, DiscNumber int
	SourceKind              string
	Inferred                bool
	InferredFields          map[string]bool
}

func parseAudioFile(path string) (audioObservation, error) {
	extension := strings.ToLower(filepath.Ext(path))
	switch extension {
	case ".flac":
		return parseFLAC(path)
	case ".mp3":
		return parseMP3(path)
	default:
		return audioObservation{}, fmt.Errorf("unsupported format")
	}
}

func parseFLAC(path string) (audioObservation, error) {
	file, err := os.Open(path)
	if err != nil {
		return audioObservation{}, err
	}
	defer file.Close()
	magic := make([]byte, 4)
	if _, err = io.ReadFull(file, magic); err != nil || string(magic) != "fLaC" {
		return audioObservation{}, fmt.Errorf("invalid FLAC stream")
	}
	observation := audioObservation{SourceKind: "flac_vorbis_comment", InferredFields: make(map[string]bool)}
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
		if blockType == 4 {
			parseVorbisComments(block, &observation)
		}
	}
	return applyFilenameFallback(path, observation), nil
}

func parseVorbisComments(block []byte, observation *audioObservation) {
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

func parseMP3(path string) (audioObservation, error) {
	file, err := os.Open(path)
	if err != nil {
		return audioObservation{}, err
	}
	defer file.Close()
	observation := audioObservation{SourceKind: "mp3_id3v2", InferredFields: make(map[string]bool)}
	header := make([]byte, 10)
	if _, err = io.ReadFull(file, header); err != nil {
		return observation, fmt.Errorf("invalid MP3 stream")
	}
	if string(header[:3]) == "ID3" {
		size := 10 + int(header[6]&0x7f)<<21 + int(header[7]&0x7f)<<14 + int(header[8]&0x7f)<<7 + int(header[9]&0x7f)
		if size > 16<<20 {
			return observation, fmt.Errorf("ID3 metadata too large")
		}
		data := make([]byte, size-10)
		if _, err = io.ReadFull(file, data); err != nil {
			return observation, err
		}
		parseID3Frames(data, &observation)
	} else if header[0] != 0xff || header[1]&0xe0 != 0xe0 {
		return observation, fmt.Errorf("invalid MP3 stream")
	}
	return applyFilenameFallback(path, observation), nil
}

func parseID3Frames(data []byte, observation *audioObservation) {
	for offset := 0; offset+10 <= len(data); {
		frameID := string(data[offset : offset+4])
		frameSize := int(binary.BigEndian.Uint32(data[offset+4 : offset+8]))
		offset += 10
		if frameSize <= 0 || offset+frameSize > len(data) {
			return
		}
		frame := data[offset : offset+frameSize]
		offset += frameSize
		if frameID != "TIT2" && frameID != "TPE1" && frameID != "TALB" && frameID != "TRCK" && frameID != "TPOS" {
			continue
		}
		if len(frame) < 2 {
			continue
		}
		value := decodeID3Text(frame)
		switch frameID {
		case "TIT2":
			observation.Title = value
		case "TPE1":
			observation.Artist = value
		case "TALB":
			observation.Album = value
		case "TRCK":
			observation.TrackNumber = parseNumber(value)
		case "TPOS":
			observation.DiscNumber = parseNumber(value)
		}
	}
}

func decodeID3Text(frame []byte) string {
	encoding := frame[0]
	text := frame[1:]
	if encoding == 0 {
		return strings.TrimSpace(string(text))
	}
	return strings.TrimSpace(string(text))
}
func parseNumber(value string) int {
	returnValue, _ := strconv.Atoi(strings.SplitN(value, "/", 2)[0])
	return returnValue
}
func setAudioTag(observation *audioObservation, key, value string) {
	switch strings.ToUpper(key) {
	case "TITLE", "TRACKTITLE":
		observation.Title = value
	case "ARTIST":
		observation.Artist = value
	case "ALBUM", "ALBUMTITLE":
		observation.Album = value
	case "TRACKNUMBER":
		observation.TrackNumber = parseNumber(value)
	case "DISCNUMBER":
		observation.DiscNumber = parseNumber(value)
	}
}
func applyFilenameFallback(path string, observation audioObservation) audioObservation {
	if observation.InferredFields == nil {
		observation.InferredFields = make(map[string]bool)
	}
	if observation.Title == "" {
		observation.Title = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		observation.Inferred = true
		observation.InferredFields["title"] = true
	}
	if observation.Artist == "" {
		observation.Artist = "未知艺术家"
		observation.Inferred = true
		observation.InferredFields["artist"] = true
	}
	if observation.Album == "" {
		observation.Album = filepath.Base(filepath.Dir(path))
		observation.Inferred = true
		observation.InferredFields["album"] = true
	}
	if observation.TrackNumber == 0 {
		observation.TrackNumber = 1
		observation.Inferred = true
		observation.InferredFields["track_number"] = true
	}
	if observation.DiscNumber == 0 {
		observation.DiscNumber = 1
		observation.InferredFields["disc_number"] = true
	}
	return observation
}

type fieldObservation struct {
	Name, Value, SourceKind string
	Inferred                bool
}

func (observation audioObservation) fieldObservations() []fieldObservation {
	fields := []fieldObservation{
		{Name: "title", Value: observation.Title},
		{Name: "artist", Value: observation.Artist},
		{Name: "album", Value: observation.Album},
		{Name: "track_number", Value: strconv.Itoa(observation.TrackNumber)},
		{Name: "disc_number", Value: strconv.Itoa(observation.DiscNumber)},
	}
	for index := range fields {
		fields[index].Inferred = observation.InferredFields[fields[index].Name]
		fields[index].SourceKind = observation.SourceKind
		if fields[index].Inferred {
			fields[index].SourceKind = "filename_fallback"
		}
	}
	return fields
}
