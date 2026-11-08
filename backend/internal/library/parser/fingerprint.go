package parser

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
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
// 文件读入内存。文件无法完整读取时返回错误；没有任何音频负载
// （例如没有帧的 FLAC、没有 mdat 的 M4A）的退化文件没有可识别的内容，返回空串，不与其他文件比较。
func ContentFingerprint(ctx context.Context, path string) (string, error) {
	compute := fingerprintFunc(filepath.Ext(path))
	if compute == nil {
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

// ContentFingerprintOf 与 ContentFingerprint 计算同一个指纹，对象是调用方已经打开的文件：extension 是带点的扩展名
// （如 ".m4a"，不区分大小写），size 是文件大小。播放用它对已打开的句柄重新取证，不再按路径访问。
func ContentFingerprintOf(ctx context.Context, extension string, reader io.ReaderAt, size int64) (string, error) {
	compute := fingerprintFunc(extension)
	if compute == nil {
		return "", fmt.Errorf("unsupported format")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return compute(ctx, reader, size)
}

// fingerprintFunc 返回扩展名对应的指纹算法；不支持的扩展名返回 nil。
func fingerprintFunc(extension string) func(context.Context, io.ReaderAt, int64) (string, error) {
	switch strings.ToLower(extension) {
	case ".flac":
		return flacFingerprint
	case ".m4a":
		return m4aFingerprint
	case ".mp3":
		return mp3Fingerprint
	case ".ogg", ".opus":
		return oggFingerprint
	case ".wav":
		return wavFingerprint
	}
	return nil
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
	if position == size {
		return "", nil
	}
	hash := sha256.New()
	if err := hashSection(ctx, hash, reader, position, size); err != nil {
		return "", err
	}
	return formatFingerprint(fingerprintFLACFrames, hash.Sum(nil)), nil
}

// m4aFingerprint 按文件顺序哈希顶层全部 mdat 原子的负载（兼容 64 位长度和“到文件尾”的原子）。
func m4aFingerprint(ctx context.Context, reader io.ReaderAt, size int64) (string, error) {
	if size < 8 {
		return "", fmt.Errorf("invalid M4A stream")
	}
	hash := sha256.New()
	var payloadBytes int64
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
			payloadBytes += atom.End - atom.PayloadStart
		}
		offset = atom.End
	}
	if payloadBytes == 0 {
		return "", nil
	}
	return formatFingerprint(fingerprintM4AMedia, hash.Sum(nil)), nil
}

// mp3Fingerprint 哈希去掉首尾标签之后的字节。
func mp3Fingerprint(ctx context.Context, reader io.ReaderAt, size int64) (string, error) {
	start, end, err := mp3AudioRange(reader, size)
	if err != nil {
		return "", err
	}
	if start == end {
		return "", nil
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

// oggFingerprint 按页流式读取，保留按块检查 context 的取消语义，不拼接音频包。
func oggFingerprint(ctx context.Context, reader io.ReaderAt, size int64) (string, error) {
	source := &io.LimitedReader{
		R: contextReader{ctx: ctx, reader: io.NewSectionReader(reader, 0, size)},
		N: size,
	}
	fingerprint, err := readOggAudioFingerprint(source)
	if err != nil {
		return "", err
	}
	// 文件在读取中被截断时，不能把恰好落在页边界的短读当成完整音频。
	if source.N != 0 {
		return "", io.ErrUnexpectedEOF
	}
	return fingerprint, nil
}

// oggAudioFingerprint 与文件入口共用流式算法；传入的字节只作为只读数据源。
func oggAudioFingerprint(data []byte) (string, error) {
	return readOggAudioFingerprint(bytes.NewReader(data))
}

func readOggAudioFingerprint(source io.Reader) (string, error) {
	reader := newOggPageReader(source)
	var fingerprint *oggAudioHasher
	for {
		firstPage := !reader.started
		segments, err := reader.nextPage()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		if firstPage {
			fingerprint, err = newOggAudioHasher(reader.reader)
			if err != nil {
				return "", err
			}
			if fingerprint == nil {
				return "", fmt.Errorf("unsupported OGG codec")
			}
		}
		for _, segmentLength := range segments {
			segment, err := reader.readSegment(segmentLength)
			if err != nil {
				return "", err
			}
			fingerprint.writeSegment(segment)
		}
	}
	if reader.trailing {
		return "", fmt.Errorf("invalid OGG trailing data")
	}
	// 独立指纹入口沿用原算法：不校验最后一个包是否结束，也不设置单包大小限制。
	return fingerprint.fingerprint(), nil
}

// oggAudioHasher 跳过 Vorbis 三个、Opus 两个头部包，只哈希后续段负载。
// 包边界只由小于 255 的段长确定，页头、段表和分页方式均不参与哈希。
type oggAudioHasher struct {
	hash             hash.Hash
	headersRemaining int
	hasAudio         bool
}

// newOggAudioHasher 在无法识别编码时返回 nil；读取失败仍返回原错误。
func newOggAudioHasher(reader *bufio.Reader) (*oggAudioHasher, error) {
	// 原算法在首页段表之后按原始字节识别前缀，不要求这些字节组成完整包。
	// Peek 保留这一规则；短于八字节时仍可能匹配七字节的 Vorbis 前缀。
	prefix, err := reader.Peek(8)
	if err != nil && err != io.EOF {
		return nil, err
	}
	headers := 0
	switch {
	case bytes.HasPrefix(prefix, []byte("\x01vorbis")):
		headers = 3
	case bytes.HasPrefix(prefix, []byte("OpusHead")):
		headers = 2
	default:
		return nil, nil
	}
	return &oggAudioHasher{hash: sha256.New(), headersRemaining: headers}, nil
}

func (h *oggAudioHasher) writeSegment(segment []byte) {
	if h.headersRemaining == 0 && len(segment) > 0 {
		_, _ = h.hash.Write(segment)
		h.hasAudio = true
	}
	if len(segment) < 255 && h.headersRemaining > 0 {
		h.headersRemaining--
	}
}

func (h *oggAudioHasher) fingerprint() string {
	if !h.hasAudio {
		return ""
	}
	return formatFingerprint(fingerprintOggAudio, h.hash.Sum(nil))
}
