package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

type audioObservation struct {
	Title, Artist, Album    string
	TrackNumber, DiscNumber int
	SourceKind              string
	Inferred                bool
	InferredFields          map[string]bool
	Artwork                 []byte
	ArtworkMIME             string
	DurationSeconds         float64
	Codec                   string
	SampleRate              int
	Channels                int
	Bitrate                 int
}

type cueTrack struct {
	Number         int
	Title, Artist  string
	IndexFrames    int
	IndexPresent   bool
	ReferencedFile string
}

func parseM4A(path string) (audioObservation, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return audioObservation{}, err
	}
	if len(b) > 16<<20 {
		return audioObservation{}, fmt.Errorf("M4A metadata too large")
	}
	if len(b) < 12 || string(b[4:8]) != "ftyp" {
		return audioObservation{}, fmt.Errorf("invalid M4A stream")
	}
	brand := string(b[8:12])
	if brand != "M4A " && brand != "isom" && brand != "mp42" && brand != "mp41" {
		return audioObservation{}, fmt.Errorf("unsupported M4A brand")
	}
	o := audioObservation{SourceKind: "m4a", InferredFields: map[string]bool{}}
	parseM4AAtoms(b, &o)
	return applyFilenameFallback(path, o), nil
}

func parseM4AAtoms(data []byte, o *audioObservation) {
	for _, m := range []struct {
		atom string
		dst  *string
	}{{"©nam", &o.Title}, {"©ART", &o.Artist}, {"©alb", &o.Album}} {
		if i := bytes.Index(data, []byte(m.atom)); i >= 0 {
			if j := bytes.Index(data[i+8:], []byte("data")); j >= 0 {
				start := i + 8 + j + 16
				end := start
				for end < len(data) && data[end] >= 32 {
					end++
				}
				if end > start {
					*m.dst = strings.TrimSpace(string(data[start:end]))
				}
			}
		}
	}
	var walk func([]byte)
	walk = func(buf []byte) {
		for pos := 0; pos+8 <= len(buf); {
			sz := int(binary.BigEndian.Uint32(buf[pos : pos+4]))
			typ := string(buf[pos+4 : pos+8])
			start := pos + 8
			if sz == 1 {
				if pos+16 > len(buf) {
					return
				}
				v := binary.BigEndian.Uint64(buf[pos+8 : pos+16])
				if v > uint64(len(buf)-pos) {
					return
				}
				sz = int(v)
				start = pos + 16
			}
			if sz < start-pos || pos+sz > len(buf) {
				return
			}
			payload := buf[start : pos+sz]
			switch typ {
			case "moov", "trak", "mdia", "minf", "stbl", "udta", "meta", "ilst":
				walk(payload)
			case "mvhd":
				if len(payload) >= 20 {
					version := payload[0]
					off := 12 // version 0: creation, modification, timescale
					if version == 1 {
						off = 20 // version 1 uses 64-bit creation/modification
					}
					if version == 0 && off+8 <= len(payload) {
						timescale := binary.BigEndian.Uint32(payload[off : off+4])
						dur := binary.BigEndian.Uint32(payload[off+4 : off+8])
						if timescale > 0 {
							o.DurationSeconds = float64(dur) / float64(timescale)
						}
					} else if version == 1 && off+12 <= len(payload) {
						timescale := binary.BigEndian.Uint32(payload[off : off+4])
						dur := binary.BigEndian.Uint64(payload[off+4 : off+12])
						if timescale > 0 {
							o.DurationSeconds = float64(dur) / float64(timescale)
						}
					}
				}
			case "mdhd":
				if len(payload) >= 20 {
					version := payload[0]
					off := 12
					if version == 1 {
						off = 20
					}
					if version == 0 && off+8 <= len(payload) {
						ts := binary.BigEndian.Uint32(payload[off : off+4])
						dur := binary.BigEndian.Uint32(payload[off+4 : off+8])
						if ts > 0 && o.DurationSeconds == 0 {
							o.DurationSeconds = float64(dur) / float64(ts)
						}
					} else if version == 1 && off+12 <= len(payload) {
						ts := binary.BigEndian.Uint32(payload[off : off+4])
						dur := binary.BigEndian.Uint64(payload[off+4 : off+12])
						if ts > 0 && o.DurationSeconds == 0 {
							o.DurationSeconds = float64(dur) / float64(ts)
						}
					}
				}
			case "stsd":
				if len(payload) >= 16 {
					o.Codec = string(payload[12:16])
					if o.Codec == "mp4a" {
						o.Codec = "aac"
					}
					if len(payload) >= 48 {
						// stsd full-box header is 8 bytes; channels/sample rate are
						// at offsets 24/32 within the sample entry.
						o.Channels = int(binary.BigEndian.Uint16(payload[32:34]))
						o.SampleRate = int(binary.BigEndian.Uint32(payload[40:44]) >> 16)
					}
				}
			case "©nam", "©ART", "©alb":
				if len(payload) >= 16 {
					text := strings.TrimSpace(string(payload[16:]))
					switch typ {
					case "©nam":
						o.Title = text
					case "©ART":
						o.Artist = text
					case "©alb":
						o.Album = text
					}
				}
			}
			pos += sz
		}
	}
	if len(data) > 8 {
		walk(data[8:])
	}
}

func parseCue(path string) ([]cueTrack, string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	if bytes.HasPrefix(b, []byte{0xff, 0xfe}) || bytes.HasPrefix(b, []byte{0xfe, 0xff}) {
		le := bytes.HasPrefix(b, []byte{0xff, 0xfe})
		b = b[2:]
		u := make([]uint16, len(b)/2)
		for i := range u {
			if le {
				u[i] = binary.LittleEndian.Uint16(b[i*2:])
			} else {
				u[i] = binary.BigEndian.Uint16(b[i*2:])
			}
		}
		b = []byte(string(utf16.Decode(u)))
	} else if !utf8.Valid(b) {
		decoded := b
		for _, enc := range []transform.Transformer{simplifiedchinese.GBK.NewDecoder(), japanese.ShiftJIS.NewDecoder()} {
			if out, _, e := transform.Bytes(enc, b); e == nil && utf8.Valid(out) {
				decoded = out
				break
			}
		}
		if !utf8.Valid(decoded) {
			return nil, "", fmt.Errorf("unsupported CUE encoding")
		}
		b = decoded
	}
	var fileRef string
	var currentFile string
	var tracks []cueTrack
	var cur *cueTrack
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		key := strings.ToUpper(fields[0])
		rest := strings.TrimSpace(strings.TrimPrefix(line, fields[0]))
		switch key {
		case "FILE":
			if len(rest) == 0 {
				return nil, "", fmt.Errorf("missing FILE")
			}
			if strings.HasPrefix(rest, "\"") {
				if end := strings.Index(rest[1:], "\""); end >= 0 {
					currentFile = rest[1 : end+1]
					fileType := strings.Fields(strings.TrimSpace(rest[end+2:]))
					if len(fileType) == 0 || !supportedCueFileType(fileType[0]) {
						return nil, "", fmt.Errorf("missing FILE type")
					}
				} else {
					return nil, "", fmt.Errorf("unterminated FILE reference")
				}
			} else {
				parts := strings.Fields(rest)
				if len(parts) < 2 || !supportedCueFileType(parts[1]) {
					return nil, "", fmt.Errorf("missing FILE type")
				}
				currentFile = parts[0]
			}
			if fileRef == "" {
				fileRef = currentFile
			}
		case "TRACK":
			if len(fields) < 3 || strings.ToUpper(fields[2]) != "AUDIO" {
				return nil, "", fmt.Errorf("unsupported CUE track")
			}
			n, err := strconv.Atoi(fields[1])
			if err != nil || n < 1 {
				return nil, "", fmt.Errorf("invalid TRACK number")
			}
			tracks = append(tracks, cueTrack{Number: n})
			tracks[len(tracks)-1].ReferencedFile = currentFile
			cur = &tracks[len(tracks)-1]
		case "TITLE":
			if cur != nil {
				cur.Title = strings.Trim(rest, "\"")
			}
		case "PERFORMER":
			if cur != nil {
				cur.Artist = strings.Trim(rest, "\"")
			}
		case "INDEX":
			if cur != nil && len(fields) >= 3 && fields[1] == "01" {
				p := strings.Split(fields[2], ":")
				if len(p) != 3 {
					return nil, "", fmt.Errorf("invalid INDEX")
				}
				m, errM := strconv.Atoi(p[0])
				e, errE := strconv.Atoi(p[1])
				f, errF := strconv.Atoi(p[2])
				if errM != nil || errE != nil || errF != nil || m < 0 || e < 0 || f < 0 {
					return nil, "", fmt.Errorf("invalid INDEX")
				}
				if e >= 60 || f >= 75 {
					return nil, "", fmt.Errorf("invalid INDEX")
				}
				cur.IndexFrames = (m*60+e)*75 + f
				cur.IndexPresent = true
			}
		}
	}
	if fileRef == "" || len(tracks) == 0 {
		return nil, "", fmt.Errorf("incomplete CUE")
	}
	for _, t := range tracks {
		if !t.IndexPresent {
			return nil, "", fmt.Errorf("missing INDEX 01")
		}
	}
	return tracks, fileRef, nil
}

func supportedCueFileType(value string) bool {
	switch strings.ToUpper(value) {
	case "WAVE", "BINARY", "AIFF", "MP3", "OGG", "OPUS", "FLAC":
		return true
	default:
		return false
	}
}

func parseAudioFile(path string) (audioObservation, error) {
	extension := strings.ToLower(filepath.Ext(path))
	switch extension {
	case ".flac":
		return parseFLAC(path)
	case ".mp3":
		return parseMP3(path)
	case ".ogg":
		return parseOgg(path)
	case ".opus":
		return parseOpus(path)
	case ".wav":
		return parseWAV(path)
	case ".m4a":
		return parseM4A(path)
	default:
		return audioObservation{}, fmt.Errorf("unsupported format")
	}
}

func parseOgg(path string) (audioObservation, error) {
	return parseOggStream(path, false)
}

func parseOggStream(path string, requireOpus bool) (audioObservation, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return audioObservation{}, err
	}
	if len(data) < 27 || string(data[:4]) != "OggS" {
		return audioObservation{}, fmt.Errorf("invalid OGG stream")
	}
	o := audioObservation{SourceKind: "ogg_vorbis_comment", InferredFields: map[string]bool{}}
	opusHead := false
	codecHeader := false
	pos := 0
	for pos+27 <= len(data) {
		if string(data[pos:pos+4]) != "OggS" {
			return o, fmt.Errorf("invalid OGG page")
		}
		segments := int(data[pos+26])
		end := pos + 27 + segments
		if end > len(data) {
			return o, fmt.Errorf("truncated OGG page")
		}
		total := 0
		for _, n := range data[pos+27 : end] {
			total += int(n)
		}
		if end+total > len(data) {
			return o, fmt.Errorf("truncated OGG packet")
		}
		packet := data[end : end+total]
		if bytes.HasPrefix(packet, []byte("\x01vorbis")) {
			codecHeader = true
		}
		if bytes.HasPrefix(packet, []byte("\x03vorbis")) {
			parseVorbisComments(packet[7:], &o)
		}
		if bytes.HasPrefix(packet, []byte("OpusTags")) {
			parseVorbisComments(packet[8:], &o)
			o.SourceKind = "opus_tags"
		}
		if bytes.HasPrefix(packet, []byte("OpusHead")) {
			opusHead = true
			codecHeader = true
		}
		pos = end + total
	}
	if pos != len(data) {
		return o, fmt.Errorf("invalid OGG trailing data")
	}
	if !codecHeader || (requireOpus && !opusHead) {
		return o, fmt.Errorf("invalid Opus stream")
	}
	return applyFilenameFallback(path, o), nil
}

func parseOpus(path string) (audioObservation, error) { return parseOggStream(path, true) }

func parseWAV(path string) (audioObservation, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return audioObservation{}, err
	}
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return audioObservation{}, fmt.Errorf("invalid WAV stream")
	}
	o := audioObservation{SourceKind: "wav_info", InferredFields: map[string]bool{}}
	for pos := 12; pos+8 <= len(data); {
		id := string(data[pos : pos+4])
		n := int(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
		pos += 8
		if n < 0 || pos+n > len(data) {
			return o, fmt.Errorf("truncated WAV chunk")
		}
		chunk := data[pos : pos+n]
		pos += n
		if n%2 == 1 {
			pos++
		}
		if id == "LIST" && len(chunk) >= 4 && string(chunk[:4]) == "INFO" {
			for p := 4; p+8 <= len(chunk); {
				k := string(chunk[p : p+4])
				l := int(binary.LittleEndian.Uint32(chunk[p+4 : p+8]))
				p += 8
				if l < 0 || p+l > len(chunk) {
					break
				}
				v := strings.TrimSpace(string(bytes.TrimRight(chunk[p:p+l], "\x00")))
				p += l
				if l%2 == 1 {
					p++
				}
				switch k {
				case "INAM":
					o.Title = v
				case "IART":
					o.Artist = v
				case "IPRD":
					o.Album = v
				}
			}
		}
	}
	return applyFilenameFallback(path, o), nil
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
		if frameID == "APIC" {
			if len(frame) > 4 {
				pos := 1
				for pos < len(frame) && frame[pos] != 0 {
					pos++
				}
				pos++
				if pos < len(frame) {
					pos++
					for pos < len(frame) && frame[pos] != 0 {
						pos++
					}
					pos++
					if pos < len(frame) {
						observation.Artwork = append([]byte(nil), frame[pos:]...)
						observation.ArtworkMIME = strings.Trim(string(frame[1:pos-1]), "\x00")
					}
				}
			}
			continue
		}
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
