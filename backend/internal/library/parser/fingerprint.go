package parser

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// 内容指纹只取决于音频负载：改标签、封面或文件名不改变它，音频改变才会改变。格式为
// "<种类>-v<版本>:<值>"，种类与版本随值保存，比较时自然要求两者都一致；改变某个种类的算法必须升版本号。
const (
	fingerprintFLACMD5    = "flac-md5-v1"
	fingerprintFLACFrames = "flac-frames-v1"
	fingerprintM4AMedia   = "m4a-mdat-v1"
	fingerprintMP3Frames  = "mp3-frames-v1"
	fingerprintOggAudio   = "ogg-audio-v1"
	fingerprintWAVData    = "wav-data-v1"

	// fingerprintReadChunk 是流式哈希每次读取的字节数，也是检查 context 的粒度。
	fingerprintReadChunk = 1 << 20
	// maxMP3TagsPerSide 限制文件首尾各自可以剥离的标签个数，避免畸形文件造成长循环。
	maxMP3TagsPerSide = 8
)

// ContentFingerprint 计算 path 指向的音频文件与标签无关的内容指纹。读取按块检查 context，不把整个
// 文件读入内存（OGG 例外：解析器本来就整体读入该格式）。文件无法完整读取时返回错误。
func ContentFingerprint(ctx context.Context, path string) (string, error) {
	var compute func(context.Context, io.ReaderAt, int64) (string, error)
	switch strings.ToLower(filepath.Ext(path)) {
	case ".flac":
		compute = flacFingerprint
	case ".m4a":
		compute = m4aFingerprint
	case ".mp3":
		compute = mp3Fingerprint
	case ".ogg", ".opus":
		compute = oggFingerprint
	case ".wav":
		compute = wavFingerprint
	default:
		return "", fmt.Errorf("unsupported format")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	return compute(ctx, file, info.Size())
}

// contextReader 在每次读取前检查 context，使长时间的流式读取可以被取消。
type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

// hashSection 把 reader 的 [start, end) 写入 hash，每读一块检查一次 context。文件比声明的短
// （例如读取期间被截断）时返回 io.ErrUnexpectedEOF。
func hashSection(ctx context.Context, hash io.Writer, reader io.ReaderAt, start, end int64) error {
	if start < 0 || end < start {
		return errors.New("invalid audio range")
	}
	source := contextReader{ctx: ctx, reader: io.NewSectionReader(reader, start, end-start)}
	copied, err := io.CopyBuffer(hash, source, make([]byte, fingerprintReadChunk))
	if err != nil {
		return err
	}
	if copied != end-start {
		return io.ErrUnexpectedEOF
	}
	return nil
}

func formatFingerprint(kind string, sum []byte) string {
	return kind + ":" + hex.EncodeToString(sum)
}

// flacMD5Fingerprint 由 STREAMINFO 生成指纹：编码器写入的音频 MD5 加总样本数、采样率、声道数和位深。
// 编码器没有写入 MD5（全零）时返回空串，由调用方回退到帧数据哈希。
func flacMD5Fingerprint(block []byte) string {
	if len(block) != 34 || allZero(block[18:34]) {
		return ""
	}
	sampleRate, channels, bitDepth, totalSamples := decodeFLACStreamInfo(block)
	return fmt.Sprintf("%s:%x:%d:%d:%d:%d", fingerprintFLACMD5, block[18:34], totalSamples, sampleRate, channels, bitDepth)
}

func allZero(data []byte) bool {
	for _, value := range data {
		if value != 0 {
			return false
		}
	}
	return true
}

// flacFingerprint 优先使用 STREAMINFO 的音频 MD5（只读文件头）；MD5 全零时哈希最后一个元数据块之后的全部字节。
func flacFingerprint(ctx context.Context, reader io.ReaderAt, size int64) (string, error) {
	var magic [4]byte
	if _, err := reader.ReadAt(magic[:], 0); err != nil || string(magic[:]) != "fLaC" {
		return "", fmt.Errorf("invalid FLAC stream")
	}
	position := int64(4)
	for last := false; !last; {
		var header [4]byte
		if _, err := reader.ReadAt(header[:], position); err != nil {
			return "", fmt.Errorf("read FLAC metadata: %w", err)
		}
		last = header[0]&0x80 != 0
		length := int64(header[1])<<16 | int64(header[2])<<8 | int64(header[3])
		position += 4
		if header[0]&0x7f == 0 && length == 34 {
			block := make([]byte, length)
			if _, err := reader.ReadAt(block, position); err != nil {
				return "", fmt.Errorf("read FLAC STREAMINFO: %w", err)
			}
			if fingerprint := flacMD5Fingerprint(block); fingerprint != "" {
				return fingerprint, nil
			}
		}
		position += length
	}
	if position > size {
		return "", io.ErrUnexpectedEOF
	}
	hash := sha256.New()
	if err := hashSection(ctx, hash, reader, position, size); err != nil {
		return "", err
	}
	return formatFingerprint(fingerprintFLACFrames, hash.Sum(nil)), nil
}

// m4aFingerprint 按文件顺序哈希顶层全部 mdat 原子的负载（兼容 64 位长度和“到文件尾”的原子）。
// 没有 mdat 时哈希空内容，与其他没有音频负载的文件一致。
func m4aFingerprint(ctx context.Context, reader io.ReaderAt, size int64) (string, error) {
	if size < 8 {
		return "", fmt.Errorf("invalid M4A stream")
	}
	hash := sha256.New()
	for offset, atoms := int64(0), 0; offset < size; atoms++ {
		if atoms >= maxM4AAtoms {
			return "", errors.New("too many M4A atoms")
		}
		atom, err := readM4AAtomHeader(reader, offset, size)
		if err != nil {
			return "", fmt.Errorf("invalid M4A atom at %d: %w", offset, err)
		}
		if atom.Type == "mdat" {
			if err := hashSection(ctx, hash, reader, atom.PayloadStart, atom.End); err != nil {
				return "", err
			}
		}
		offset = atom.End
	}
	return formatFingerprint(fingerprintM4AMedia, hash.Sum(nil)), nil
}

// mp3Fingerprint 哈希去掉首尾标签之后的字节。
func mp3Fingerprint(ctx context.Context, reader io.ReaderAt, size int64) (string, error) {
	start, end, err := mp3AudioRange(reader, size)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	if err := hashSection(ctx, hash, reader, start, end); err != nil {
		return "", err
	}
	return formatFingerprint(fingerprintMP3Frames, hash.Sum(nil)), nil
}

// mp3AudioRange 返回去掉开头 ID3v2（含 footer）与结尾 ID3v1、APEv2、Lyrics3v2 之后的音频字节范围。
func mp3AudioRange(reader io.ReaderAt, size int64) (start, end int64, err error) {
	start, end = 0, size
	for range maxMP3TagsPerSide {
		if end-start < 10 {
			break
		}
		var header [10]byte
		if _, err := reader.ReadAt(header[:], start); err != nil {
			return 0, 0, err
		}
		if string(header[:3]) != "ID3" {
			break
		}
		tagSize := 10 + int64(header[6]&0x7f)<<21 + int64(header[7]&0x7f)<<14 + int64(header[8]&0x7f)<<7 + int64(header[9]&0x7f)
		if header[5]&0x10 != 0 {
			tagSize += 10
		}
		if tagSize > end-start {
			return 0, 0, fmt.Errorf("invalid MP3 stream: ID3 tag exceeds file")
		}
		start += tagSize
	}
	for range maxMP3TagsPerSide {
		trimmed, err := mp3TrailerLength(reader, start, end)
		if err != nil {
			return 0, 0, err
		}
		if trimmed == 0 {
			break
		}
		end -= trimmed
	}
	return start, end, nil
}

// mp3TrailerLength 返回 [start, end) 末尾处一个尾部标签的长度；没有可识别的标签时返回 0。
// 尾部标签可以按任意顺序叠加（音频后常见 APEv2、Lyrics3v2，最后是 ID3v1），调用方循环剥离。
func mp3TrailerLength(reader io.ReaderAt, start, end int64) (int64, error) {
	available := end - start
	if available >= 128 {
		var marker [3]byte
		if _, err := reader.ReadAt(marker[:], end-128); err != nil {
			return 0, err
		}
		if string(marker[:]) == "TAG" {
			// ID3v1 前面可能带 227 字节的扩展块 "TAG+"。
			if available >= 128+227 {
				var extended [4]byte
				if _, err := reader.ReadAt(extended[:], end-128-227); err != nil {
					return 0, err
				}
				if string(extended[:]) == "TAG+" {
					return 128 + 227, nil
				}
			}
			return 128, nil
		}
	}
	if available >= 32 {
		var footer [32]byte
		if _, err := reader.ReadAt(footer[:], end-32); err != nil {
			return 0, err
		}
		if string(footer[:8]) == "APETAGEX" {
			// 标签大小含 footer、不含 header；标志位 31 表示前面还有 32 字节的 header。
			tagSize := int64(binary.LittleEndian.Uint32(footer[12:16]))
			if binary.LittleEndian.Uint32(footer[20:24])&(1<<31) != 0 {
				tagSize += 32
			}
			if tagSize >= 32 && tagSize <= available {
				return tagSize, nil
			}
		}
	}
	if available >= 15 {
		var trailer [15]byte
		if _, err := reader.ReadAt(trailer[:], end-15); err != nil {
			return 0, err
		}
		if string(trailer[6:]) == "LYRICS200" {
			// 六位十进制长度含 "LYRICSBEGIN"，不含长度本身和结束标记。
			if length, err := strconv.Atoi(string(trailer[:6])); err == nil && int64(length)+15 <= available {
				return int64(length) + 15, nil
			}
		}
	}
	return 0, nil
}

// wavFingerprint 哈希 fmt 块（带长度前缀）与按顺序排列的全部 data 块负载，忽略 LIST 等其他块。
func wavFingerprint(ctx context.Context, reader io.ReaderAt, size int64) (string, error) {
	var header [12]byte
	if _, err := reader.ReadAt(header[:], 0); err != nil || string(header[:4]) != "RIFF" || string(header[8:12]) != "WAVE" {
		return "", fmt.Errorf("invalid WAV stream")
	}
	declaredEnd := int64(binary.LittleEndian.Uint32(header[4:8])) + 8
	if declaredEnd < 12 || declaredEnd > size {
		return "", fmt.Errorf("truncated WAV stream")
	}
	hash := sha256.New()
	var foundFormat bool
	var dataBytes int64
	for position := int64(12); position+8 <= declaredEnd; {
		var chunkHeader [8]byte
		if _, err := reader.ReadAt(chunkHeader[:], position); err != nil {
			return "", fmt.Errorf("truncated WAV chunk header")
		}
		position += 8
		id := string(chunkHeader[:4])
		chunkSize := int64(binary.LittleEndian.Uint32(chunkHeader[4:8]))
		paddedSize := chunkSize + chunkSize%2
		if position+paddedSize > declaredEnd {
			return "", fmt.Errorf("truncated WAV chunk")
		}
		switch id {
		case "fmt ":
			if chunkSize > 1<<20 {
				return "", fmt.Errorf("WAV format metadata too large")
			}
			payload := make([]byte, chunkSize)
			if _, err := reader.ReadAt(payload, position); err != nil {
				return "", fmt.Errorf("truncated WAV format chunk")
			}
			var length [4]byte
			binary.LittleEndian.PutUint32(length[:], uint32(chunkSize))
			_, _ = hash.Write(length[:])
			_, _ = hash.Write(payload)
			foundFormat = true
		case "data":
			if err := hashSection(ctx, hash, reader, position, position+chunkSize); err != nil {
				return "", err
			}
			dataBytes += chunkSize
		}
		position += paddedSize
	}
	if !foundFormat || dataBytes <= 0 {
		return "", fmt.Errorf("invalid WAV stream: missing audio chunks")
	}
	return formatFingerprint(fingerprintWAVData, hash.Sum(nil)), nil
}

// oggFingerprint 整体读入文件（与解析器一致）后计算音频包哈希。
func oggFingerprint(ctx context.Context, reader io.ReaderAt, size int64) (string, error) {
	data := make([]byte, size)
	if _, err := io.ReadFull(contextReader{ctx: ctx, reader: io.NewSectionReader(reader, 0, size)}, data); err != nil {
		return "", err
	}
	return oggAudioFingerprint(data)
}

// oggAudioFingerprint 哈希头部包（Vorbis 三个、Opus 两个）之后全部音频包的字节。按包而不是按页计算：
// 注释包变长只改变头部分页与之后各页的序号和校验和，音频包本身不变，页头也不参与哈希，所以改标签不会改变指纹。
func oggAudioFingerprint(data []byte) (string, error) {
	if len(data) < 27 || string(data[:4]) != "OggS" {
		return "", fmt.Errorf("invalid OGG stream")
	}
	firstPayload := 27 + int(data[26])
	if firstPayload > len(data) {
		return "", fmt.Errorf("truncated OGG page")
	}
	firstPacket := data[firstPayload:]
	headerPackets := 0
	switch {
	case bytes.HasPrefix(firstPacket, []byte("\x01vorbis")):
		headerPackets = 3
	case bytes.HasPrefix(firstPacket, []byte("OpusHead")):
		headerPackets = 2
	default:
		return "", fmt.Errorf("unsupported OGG codec")
	}
	hash := sha256.New()
	completed := 0
	pos := 0
	for pos+27 <= len(data) {
		if string(data[pos:pos+4]) != "OggS" {
			return "", fmt.Errorf("invalid OGG page")
		}
		table := pos + 27
		cursor := table + int(data[pos+26])
		if cursor > len(data) {
			return "", fmt.Errorf("truncated OGG page")
		}
		for _, segmentLength := range data[table : table+int(data[pos+26])] {
			end := cursor + int(segmentLength)
			if end > len(data) {
				return "", fmt.Errorf("truncated OGG packet")
			}
			if completed >= headerPackets {
				_, _ = hash.Write(data[cursor:end])
			}
			cursor = end
			// 小于 255 的段结束当前包；255 表示包在下一段（可能在下一页）继续。
			if segmentLength < 255 {
				completed++
			}
		}
		pos = cursor
	}
	if pos != len(data) {
		return "", fmt.Errorf("invalid OGG trailing data")
	}
	return formatFingerprint(fingerprintOggAudio, hash.Sum(nil)), nil
}
