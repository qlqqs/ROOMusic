package parser

import (
	"bytes"
	"encoding/binary"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"golang.org/x/text/encoding/simplifiedchinese"
)

// 歌词的来源键：写入 tracks.lyrics_source，区分内嵌标签与外置文件。
const (
	LyricsSourceVorbisLyrics   = "vorbis_lyrics"
	LyricsSourceVorbisUnsynced = "vorbis_unsyncedlyrics"
	LyricsSourceMP4            = "mp4_lyr"
	LyricsSourceID3USLT        = "id3_uslt"
	LyricsSourceLRCFile        = "lrc_file"
)

// MaxExternalLyricsBytes 是外置歌词解码前的原始字节上限。
const MaxExternalLyricsBytes = 256 << 10

// maxLyricsFrameTextBytes 是 USLT 帧里歌词文本解码前的上限：规范化后不超过 metadata.MaxLyricsBytes 的歌词，原文远小于它。
// 先按它拒绝，避免为一个异常大的帧分配数倍于其大小的临时内存。
const maxLyricsFrameTextBytes = 4 * metadata.MaxLyricsBytes

// DecodeExternalLyrics 按 BOM、合法 UTF-8、GB18030 的顺序解码并规范化外置歌词；无效或超限时返回 false。
func DecodeExternalLyrics(data []byte) (string, bool) {
	if len(data) == 0 || len(data) > MaxExternalLyricsBytes {
		return "", false
	}
	if bytes.HasPrefix(data, []byte{0x00, 0x00, 0xfe, 0xff}) || bytes.HasPrefix(data, []byte{0xff, 0xfe, 0x00, 0x00}) {
		return "", false
	}
	if bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) {
		return metadata.NormalizeLyrics(string(data[3:]))
	}
	if bytes.HasPrefix(data, []byte{0xff, 0xfe}) || bytes.HasPrefix(data, []byte{0xfe, 0xff}) {
		decoded, ok := decodeID3Lyrics(1, data)
		if !ok {
			return "", false
		}
		return metadata.NormalizeLyrics(decoded)
	}
	if utf8.Valid(data) {
		return metadata.NormalizeLyrics(string(data))
	}
	decoded, err := simplifiedchinese.GB18030.NewDecoder().Bytes(data)
	if err != nil || bytes.ContainsRune(decoded, utf8.RuneError) {
		return "", false
	}
	return metadata.NormalizeLyrics(string(decoded))
}

// ApplyExternalLyrics 将已规范化的外置歌词及其署名应用到观察副本，不修改共享的署名切片。
func ApplyExternalLyrics(observation *AudioObservation, text string) {
	credits := *observation
	credits.Credits = make([]metadata.Credit, 0, len(observation.Credits))
	for _, credit := range observation.Credits {
		if credit.Source != "lyrics" {
			credits.Credits = append(credits.Credits, credit)
		}
	}
	credits.Lyrics = text
	if credits.InferredFields["title"] {
		credits.Title = ""
	}
	addLyricsCredits(&credits)
	observation.Credits = metadata.CanonicalCredits(credits.Credits)
	observation.Lyrics, observation.LyricsSource = text, LyricsSourceLRCFile
}

// setAudioLyrics 处理 Vorbis 注释里的歌词键，返回键是否是歌词键（是则调用方不再按普通标签处理）。
// 键不区分大小写：`LYRICS` 优先于 `UNSYNCEDLYRICS`/`UNSYNCED LYRICS`；同一类键取第一个有效值，无效（空白、非法 UTF-8、超限）的值被跳过。
func setAudioLyrics(observation *AudioObservation, key, value string) bool {
	source := ""
	switch strings.ToUpper(key) {
	case "LYRICS":
		source = LyricsSourceVorbisLyrics
	case "UNSYNCEDLYRICS", "UNSYNCED LYRICS":
		source = LyricsSourceVorbisUnsynced
	default:
		return false
	}
	if observation.LyricsSource == LyricsSourceVorbisLyrics || (source == LyricsSourceVorbisUnsynced && observation.Lyrics != "") {
		return true
	}
	if lyrics, ok := metadata.NormalizeLyrics(value); ok {
		observation.Lyrics, observation.LyricsSource = lyrics, source
	}
	return true
}

// setM4ALyrics 处理 MP4 的 `©lyr`：只接受类型为 1（UTF-8）的 data，取第一个有效值。
func setM4ALyrics(observation *AudioObservation, value, typeCode []byte) {
	if observation.Lyrics != "" || len(typeCode) < 4 || binary.BigEndian.Uint32(typeCode) != 1 {
		return
	}
	if lyrics, ok := metadata.NormalizeLyrics(string(value)); ok {
		observation.Lyrics, observation.LyricsSource = lyrics, LyricsSourceMP4
	}
}

// parseID3USLT 解析 USLT 帧（编码字节、3 字节语言、以编码对应的终止符结尾的内容描述、歌词文本）。
// 返回内容描述是否为空（仅有 BOM 也算空）与规范化后的歌词；编码不认识、没有终止符、解码失败或歌词无效时 ok 为 false。
func parseID3USLT(frame []byte) (emptyDescriptor bool, lyrics string, ok bool) {
	if len(frame) < 5 {
		return false, "", false
	}
	encoding, body := frame[0], frame[4:]
	var descriptor, text []byte
	switch encoding {
	case 0, 3:
		end := bytes.IndexByte(body, 0)
		if end < 0 {
			return false, "", false
		}
		descriptor, text = body[:end], body[end+1:]
	case 1, 2:
		// UTF-16 的终止符是按两字节对齐的 0x0000。
		end := -1
		for offset := 0; offset+1 < len(body); offset += 2 {
			if body[offset] == 0 && body[offset+1] == 0 {
				end = offset
				break
			}
		}
		if end < 0 {
			return false, "", false
		}
		descriptor, text = body[:end], body[end+2:]
	default:
		return false, "", false
	}
	if len(text) > maxLyricsFrameTextBytes {
		return false, "", false
	}
	decoded, decodedOK := decodeID3Lyrics(encoding, text)
	if !decodedOK {
		return false, "", false
	}
	lyrics, ok = metadata.NormalizeLyrics(decoded)
	emptyDescriptor = len(descriptor) == 0 || (len(descriptor) == 2 && (bytes.Equal(descriptor, []byte{0xff, 0xfe}) || bytes.Equal(descriptor, []byte{0xfe, 0xff})))
	return emptyDescriptor, lyrics, ok
}

// decodeID3Lyrics 按 ID3v2 的编码字节把歌词文本解成 UTF-8：0 是 ISO-8859-1，1 是带 BOM 的 UTF-16（缺 BOM 时按小端），
// 2 是不带 BOM 的 UTF-16BE，3 是 UTF-8。奇数长度、非法代理对或非法 UTF-8 解码失败。
func decodeID3Lyrics(encoding byte, text []byte) (string, bool) {
	switch encoding {
	case 0:
		runes := make([]rune, len(text))
		for index, character := range text {
			runes[index] = rune(character)
		}
		return string(runes), true
	case 3:
		if !utf8.Valid(text) {
			return "", false
		}
		return string(text), true
	}
	littleEndian := encoding == 1
	if encoding == 1 && len(text) >= 2 {
		switch {
		case text[0] == 0xff && text[1] == 0xfe:
			text = text[2:]
		case text[0] == 0xfe && text[1] == 0xff:
			text, littleEndian = text[2:], false
		}
	}
	if len(text)%2 != 0 {
		return "", false
	}
	units := make([]uint16, len(text)/2)
	for index := range units {
		if littleEndian {
			units[index] = binary.LittleEndian.Uint16(text[index*2:])
		} else {
			units[index] = binary.BigEndian.Uint16(text[index*2:])
		}
	}
	if !validUTF16Units(units) {
		return "", false
	}
	return string(utf16.Decode(units)), true
}
