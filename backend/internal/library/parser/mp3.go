package parser

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
)

func parseMP3(path string) (AudioObservation, error) {
	file, err := os.Open(path)
	if err != nil {
		return AudioObservation{}, err
	}
	defer file.Close()
	observation := AudioObservation{SourceKind: "mp3_id3v2", InferredFields: make(map[string]bool), Codec: "mp3"}
	header := make([]byte, 10)
	if _, err = io.ReadFull(file, header); err != nil {
		return observation, fmt.Errorf("invalid MP3 stream")
	}
	audioStart := int64(0)
	if string(header[:3]) == "ID3" {
		size := 10 + int(header[6]&0x7f)<<21 + int(header[7]&0x7f)<<14 + int(header[8]&0x7f)<<7 + int(header[9]&0x7f)
		if size > 16<<20 {
			return observation, fmt.Errorf("ID3 metadata too large")
		}
		data := make([]byte, size-10)
		if _, err = io.ReadFull(file, data); err != nil {
			return observation, err
		}
		if err := parseID3Frames(data, &observation, header[3], header[5]); err != nil {
			return observation, err
		}
		audioStart = int64(size)
		if header[5]&0x10 != 0 {
			audioStart += 10
		}
	} else if header[0] != 0xff || header[1]&0xe0 != 0xe0 {
		return observation, fmt.Errorf("invalid MP3 stream")
	}
	if info, statErr := file.Stat(); statErr == nil {
		applyMP3AudioFacts(file, info.Size(), audioStart, &observation)
	}
	finalizeTags(&observation)
	return applyFilenameFallback(path, observation), nil
}

// parseID3Frames 解析标签体里的帧；major 是 ID3v2 主版本号，tagFlags 是标签头的标志字节。
func parseID3Frames(data []byte, observation *AudioObservation, major, tagFlags byte) error {
	if major == 3 && tagFlags&0x80 != 0 {
		// v2.3 的同步消除作用于整个标签体（含帧头）；v2.4 改为逐帧标志，在 id3FramePayload 里处理。
		data = removeUnsynchronisation(data)
	}
	lyricsHasEmptyDescriptor := false
	for offset := 0; offset+10 <= len(data); {
		frameID := string(data[offset : offset+4])
		frameSize := id3FrameSize(data, offset, major)
		formatFlags := data[offset+9]
		offset += 10
		if frameSize <= 0 || offset+frameSize > len(data) {
			return nil
		}
		frame := data[offset : offset+frameSize]
		offset += frameSize
		if frameID == "APIC" {
			if artwork, mimeType, ok := parseID3AttachedPicture(frame); ok {
				observation.Artwork = artwork
				observation.ArtworkMIME = mimeType
			}
			continue
		}
		if frameID == "USLT" {
			// 内容描述为空的帧优先；其余情况取第一个有效帧。
			if payload, ok := id3FramePayload(frame, major, formatFlags); ok {
				if emptyDescriptor, lyrics, valid := parseID3USLT(payload); valid && (observation.Lyrics == "" || emptyDescriptor && !lyricsHasEmptyDescriptor) {
					observation.Lyrics, observation.LyricsSource = lyrics, LyricsSourceID3USLT
					lyricsHasEmptyDescriptor = emptyDescriptor
				}
			}
			continue
		}
		if frameID != "TIT2" && frameID != "TPE1" && frameID != "TPE2" && frameID != "TPE3" && frameID != "TALB" && frameID != "TRCK" && frameID != "TPOS" && frameID != "TCON" && frameID != "TDRC" && frameID != "TYER" && frameID != "TSRC" && frameID != "TCOM" && frameID != "TPUB" && frameID != "TXXX" && frameID != "TCMP" && frameID != "TDOR" && frameID != "TORY" {
			continue
		}
		if len(frame) < 2 {
			continue
		}
		if frameID == "TXXX" || frameID == "TCMP" || frameID == "TDOR" || frameID == "TORY" {
			payload, ok := id3FramePayload(frame, major, formatFlags)
			if !ok {
				continue
			}
			key := frameID
			var values []string
			if frameID == "TXXX" {
				key, values = parseID3TXXX(payload)
			} else {
				values = decodeID3ArtistValues(payload)
			}
			for _, raw := range values {
				if err := setAudioTag(observation, key, raw); err != nil {
					return err
				}
			}
			continue
		}
		if role := artistTagRole(frameID); role != "" {
			payload, ok := id3FramePayload(frame, major, formatFlags)
			if !ok {
				continue
			}
			for _, raw := range decodeID3ArtistValues(payload) {
				if err := captureArtistTag(observation, frameID, raw); err != nil {
					return err
				}
				if role != "track_artist" && role != "album_artist" {
					addObservationCredits(observation, role, raw)
				}
			}
			continue
		}
		if frameID == "TCON" {
			if payload, ok := id3FramePayload(frame, major, formatFlags); ok {
				observation.Genres = metadata.SplitGenres(append(observation.Genres, decodeID3ArtistValues(payload)...)...)
			}
			continue
		}
		value := decodeID3Text(frame)
		switch frameID {
		case "TIT2":
			observation.Title = value
		case "TALB":
			observation.Album = value
		case "TRCK":
			observation.TrackNumber = parseNumber(value)
		case "TPOS":
			observation.DiscNumber = parseNumber(value)
		case "TDRC", "TYER":
			observation.Year = ParseYear(value)
		case "TSRC":
			observation.ISRC = value
		case "TPUB":
			observation.Label = value
		}
	}
	return nil
}

// id3FrameSize 读取 offset 处帧头里的 4 字节大小。v2.3 是普通大端整数；v2.4 规定为 synchsafe 整数，
// 但有些写入器在 v2.4 里仍写普通整数。两种读法都可能成立时，取下一个帧头位置有效的那种；都无效时按规范取 synchsafe。
func id3FrameSize(data []byte, offset int, major byte) int {
	raw := data[offset+4 : offset+8]
	plain := int(binary.BigEndian.Uint32(raw))
	if major < 4 || raw[0]|raw[1]|raw[2]|raw[3] >= 0x80 {
		return plain
	}
	synchsafe := int(raw[0])<<21 | int(raw[1])<<14 | int(raw[2])<<7 | int(raw[3])
	if id3FrameBoundaryValid(data, offset+10+synchsafe) || !id3FrameBoundaryValid(data, offset+10+plain) {
		return synchsafe
	}
	return plain
}

// id3FrameBoundaryValid 判断 next 是否像一个帧边界：恰好是标签体末尾、填充的开头，或一个由大写字母与数字组成的帧标识。
func id3FrameBoundaryValid(data []byte, next int) bool {
	if next < 0 || next > len(data) {
		return false
	}
	if next == len(data) || data[next] == 0 {
		return true
	}
	if next+10 > len(data) {
		return false
	}
	for _, character := range data[next : next+4] {
		if (character < 'A' || character > 'Z') && (character < '0' || character > '9') {
			return false
		}
	}
	return true
}

// id3FramePayload 按帧格式标志取出帧的有效载荷：跳过分组标识与数据长度指示，还原 v2.4 的逐帧同步消除。
// 压缩或加密的帧无法解读，返回 false。
func id3FramePayload(frame []byte, major, formatFlags byte) ([]byte, bool) {
	var grouped, unsynchronised, hasDataLength bool
	if major >= 4 {
		if formatFlags&0x0c != 0 {
			return nil, false
		}
		grouped, unsynchronised, hasDataLength = formatFlags&0x40 != 0, formatFlags&0x02 != 0, formatFlags&0x01 != 0
	} else {
		if formatFlags&0xc0 != 0 {
			return nil, false
		}
		grouped = formatFlags&0x20 != 0
	}
	if grouped {
		if len(frame) < 1 {
			return nil, false
		}
		frame = frame[1:]
	}
	if hasDataLength {
		if len(frame) < 4 {
			return nil, false
		}
		frame = frame[4:]
	}
	if unsynchronised {
		frame = removeUnsynchronisation(frame)
	}
	return frame, true
}

// removeUnsynchronisation 把同步消除时插入的零字节还原：每个 0xFF 0x00 变回 0xFF。
func removeUnsynchronisation(data []byte) []byte {
	if !bytes.Contains(data, []byte{0xff, 0x00}) {
		return data
	}
	restored := make([]byte, 0, len(data))
	for index := 0; index < len(data); index++ {
		restored = append(restored, data[index])
		if data[index] == 0xff && index+1 < len(data) && data[index+1] == 0x00 {
			index++
		}
	}
	return restored
}

var (
	mp3MPEG1Layer3Bitrates = [16]int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 0}
	mp3MPEG2Layer3Bitrates = [16]int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0}
	mp3MPEG1SampleRates    = [4]int{44100, 48000, 32000, 0}
)

// applyMP3AudioFacts 只在 ID3 后的有界窗口中寻找 MPEG Layer III 首帧，
// 不把整首音频读入内存。找不到有效帧时保留已解析标签，不伪造音频事实。
func applyMP3AudioFacts(reader io.ReaderAt, fileSize, audioStart int64, observation *AudioObservation) {
	if reader == nil || observation == nil || fileSize <= audioStart || audioStart < 0 {
		return
	}
	searchSize := fileSize - audioStart
	if searchSize > maxMP3FrameSearchBytes {
		searchSize = maxMP3FrameSearchBytes
	}
	data := make([]byte, int(searchSize))
	read, err := reader.ReadAt(data, audioStart)
	if err != nil && !errors.Is(err, io.EOF) {
		return
	}
	data = data[:read]
	for offset := 0; offset+4 <= len(data); offset++ {
		bitrate, sampleRate, channels, valid := decodeMP3FrameHeader(data[offset : offset+4])
		if !valid {
			continue
		}
		observation.Bitrate = bitrate
		observation.SampleRate = sampleRate
		observation.Channels = channels
		if fileSize <= math.MaxInt64/8 {
			seconds := fileSize * 8 / int64(bitrate*1000)
			if seconds > 0 {
				observation.DurationSeconds = float64(seconds)
			}
		}
		return
	}
}

func decodeMP3FrameHeader(header []byte) (bitrate, sampleRate, channels int, valid bool) {
	if len(header) < 4 || header[0] != 0xff || header[1]&0xe0 != 0xe0 {
		return 0, 0, 0, false
	}
	version := (header[1] >> 3) & 0x03
	layer := (header[1] >> 1) & 0x03
	bitrateIndex := (header[2] >> 4) & 0x0f
	sampleRateIndex := (header[2] >> 2) & 0x03
	if version == 1 || layer != 1 || bitrateIndex == 0 || bitrateIndex == 15 || sampleRateIndex == 3 {
		return 0, 0, 0, false
	}
	sampleRate = mp3MPEG1SampleRates[sampleRateIndex]
	if version == 3 {
		bitrate = mp3MPEG1Layer3Bitrates[bitrateIndex]
	} else {
		bitrate = mp3MPEG2Layer3Bitrates[bitrateIndex]
		if version == 2 {
			sampleRate /= 2
		} else {
			sampleRate /= 4
		}
	}
	channels = 2
	if header[3]>>6 == 3 {
		channels = 1
	}
	return bitrate, sampleRate, channels, bitrate > 0 && sampleRate > 0
}

func parseID3AttachedPicture(frame []byte) ([]byte, string, bool) {
	if len(frame) < 4 {
		return nil, "", false
	}
	mimeLength := bytes.IndexByte(frame[1:], 0)
	if mimeLength < 1 || mimeLength > 255 {
		return nil, "", false
	}
	mimeEnd := 1 + mimeLength
	descriptionStart := mimeEnd + 2
	if descriptionStart > len(frame) {
		return nil, "", false
	}

	dataStart := -1
	switch frame[0] {
	case 0, 3:
		if descriptionLength := bytes.IndexByte(frame[descriptionStart:], 0); descriptionLength >= 0 {
			dataStart = descriptionStart + descriptionLength + 1
		}
	case 1, 2:
		for offset := descriptionStart; offset+1 < len(frame); offset += 2 {
			if frame[offset] == 0 && frame[offset+1] == 0 {
				dataStart = offset + 2
				break
			}
		}
	default:
		return nil, "", false
	}
	if dataStart < 0 || dataStart >= len(frame) {
		return nil, "", false
	}
	mimeType := strings.ToLower(strings.TrimSpace(string(frame[1:mimeEnd])))
	if mimeType == "" {
		return nil, "", false
	}
	return append([]byte(nil), frame[dataStart:]...), mimeType, true
}

func decodeID3Text(frame []byte) string {
	if len(frame) < 2 {
		return ""
	}
	encoding := frame[0]
	text := frame[1:]
	switch encoding {
	case 0:
		if terminator := bytes.IndexByte(text, 0); terminator >= 0 {
			text = text[:terminator]
		}
		var decoded strings.Builder
		for _, value := range text {
			decoded.WriteRune(rune(value))
		}
		return metadata.BoundedText(decoded.String())
	case 1:
		if len(text) < 2 || (text[0] != 0xff || text[1] != 0xfe) && (text[0] != 0xfe || text[1] != 0xff) {
			return ""
		}
		littleEndian := text[0] == 0xff
		return decodeID3UTF16(text[2:], littleEndian)
	case 2:
		return decodeID3UTF16(text, false)
	case 3:
		if terminator := bytes.IndexByte(text, 0); terminator >= 0 {
			text = text[:terminator]
		}
		if !utf8.Valid(text) {
			return ""
		}
		return metadata.BoundedText(string(text))
	default:
		return ""
	}
}

func decodeID3UTF16(value []byte, littleEndian bool) string {
	if len(value)%2 != 0 {
		return ""
	}
	units := make([]uint16, 0, len(value)/2)
	for offset := 0; offset < len(value); offset += 2 {
		var unit uint16
		if littleEndian {
			unit = binary.LittleEndian.Uint16(value[offset:])
		} else {
			unit = binary.BigEndian.Uint16(value[offset:])
		}
		if unit == 0 {
			break
		}
		units = append(units, unit)
	}
	if !validUTF16Units(units) {
		return ""
	}
	return metadata.BoundedText(string(utf16.Decode(units)))
}

// decodeID3ArtistValues 保留每个文本值的原文；按编码识别 NUL 多值边界。
func decodeID3ArtistValues(frame []byte) []string {
	if len(frame) < 2 {
		return nil
	}
	text := frame[1:]
	var decoded string
	switch frame[0] {
	case 0:
		var value strings.Builder
		for _, b := range text {
			value.WriteRune(rune(b))
		}
		decoded = value.String()
	case 1, 2:
		littleEndian := false
		if frame[0] == 1 {
			if len(text) < 2 || (text[0] != 0xff || text[1] != 0xfe) && (text[0] != 0xfe || text[1] != 0xff) {
				return nil
			}
			littleEndian = text[0] == 0xff
			text = text[2:]
		}
		if len(text)%2 != 0 {
			return nil
		}
		units := make([]uint16, len(text)/2)
		for i := range units {
			if littleEndian {
				units[i] = binary.LittleEndian.Uint16(text[i*2:])
			} else {
				units[i] = binary.BigEndian.Uint16(text[i*2:])
			}
		}
		if !validUTF16Units(units) {
			return nil
		}
		decoded = string(utf16.Decode(units))
	case 3:
		if !utf8.Valid(text) {
			return nil
		}
		decoded = string(text)
	default:
		return nil
	}
	values := strings.Split(strings.TrimRight(decoded, "\x00"), "\x00")
	if frame[0] == 1 {
		// 部分写入器为后续值重复携带 BOM；它属于编码，不属于姓名原文。
		for i := range values {
			values[i] = strings.TrimPrefix(values[i], "\ufeff")
		}
	}
	return values
}

// parseID3TXXX 按帧编码解码描述和全部值；解码器按 UTF-16 码元识别 NUL 并保留字节序。
func parseID3TXXX(frame []byte) (string, []string) {
	parts := decodeID3ArtistValues(frame)
	if len(parts) < 2 {
		return "", nil
	}
	description := strings.ToUpper(strings.TrimSpace(parts[0]))
	switch description {
	case "ALBUMARTIST", "ALBUM_ARTIST", "ALBUM ARTIST", "COMPILATION", "ORIGINALDATE", "ORIGINALYEAR":
		return "TXXX:" + description, parts[1:]
	default:
		return "", nil
	}
}
