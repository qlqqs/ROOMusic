package parser

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

func parseOgg(path string) (AudioObservation, error) {
	return parseOggStream(path, false)
}

func parseOggStream(path string, requireOpus bool) (AudioObservation, error) {
	file, err := os.Open(path)
	if err != nil {
		return AudioObservation{}, err
	}
	defer file.Close()
	reader := newOggPageReader(file)
	o := AudioObservation{SourceKind: "ogg_vorbis_comment", InferredFields: map[string]bool{}}
	opusHead := false
	codecHeader := false
	var fingerprint *oggAudioHasher
	// 注释可跨多个页，一页也可包含多个包；只保留当前包，不累计音频包。
	var packet []byte
	for {
		firstPage := !reader.started
		segments, err := reader.nextPage()
		if err == io.EOF {
			break
		}
		if err != nil {
			if !reader.started {
				return AudioObservation{}, err
			}
			return o, err
		}
		if firstPage {
			// 指纹只是补充事实：首部不符合指纹规则时仍按原规则解析标签。
			fingerprint, err = newOggAudioHasher(reader.reader)
			if err != nil {
				return o, err
			}
		}
		for _, segmentLength := range segments {
			segment, err := reader.readSegment(segmentLength)
			if err != nil {
				return o, err
			}
			if len(packet)+len(segment) > 16<<20 {
				return o, fmt.Errorf("OGG packet too large")
			}
			packet = append(packet, segment...)
			if fingerprint != nil {
				fingerprint.writeSegment(segment)
			}
			if segmentLength == 255 {
				continue
			}
			if err := parseOggPacket(packet, &o, &codecHeader, &opusHead); err != nil {
				return o, err
			}
			packet = packet[:0]
		}
	}
	if len(packet) != 0 {
		return o, fmt.Errorf("truncated OGG packet")
	}
	if reader.trailing {
		return o, fmt.Errorf("invalid OGG trailing data")
	}
	if !codecHeader || (requireOpus && !opusHead) {
		return o, fmt.Errorf("invalid Opus stream")
	}
	if fingerprint != nil {
		o.ContentFingerprint = fingerprint.fingerprint()
	}
	return applyFilenameFallback(path, o), nil
}

// oggPageReader 逐页读取段表，段负载在调用方按顺序消费；所有读取缓冲大小固定。
// 不额外校验版本、校验和、流序号或续包标志，保持原解析器接受的文件范围。
type oggPageReader struct {
	reader   *bufio.Reader
	header   [27]byte
	segments [255]byte
	segment  [255]byte
	started  bool
	trailing bool
}

func newOggPageReader(reader io.Reader) *oggPageReader {
	return &oggPageReader{reader: bufio.NewReader(reader)}
}

func (r *oggPageReader) nextPage() ([]byte, error) {
	n, err := io.ReadFull(r.reader, r.header[:])
	if err != nil {
		if err != io.EOF && err != io.ErrUnexpectedEOF {
			return nil, err
		}
		if !r.started {
			return nil, fmt.Errorf("invalid OGG stream")
		}
		// 短尾部由调用方收口：标签解析优先报告未结束的包，指纹仅检查尾部。
		r.trailing = n != 0
		return nil, io.EOF
	}
	if string(r.header[:4]) != "OggS" {
		if !r.started {
			return nil, fmt.Errorf("invalid OGG stream")
		}
		return nil, fmt.Errorf("invalid OGG page")
	}
	r.started = true
	segments := r.segments[:int(r.header[26])]
	if _, err := io.ReadFull(r.reader, segments); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil, fmt.Errorf("truncated OGG page")
		}
		return nil, err
	}
	return segments, nil
}

func (r *oggPageReader) readSegment(length byte) ([]byte, error) {
	segment := r.segment[:int(length)]
	if _, err := io.ReadFull(r.reader, segment); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil, fmt.Errorf("truncated OGG packet")
		}
		return nil, err
	}
	return segment, nil
}

func parseOpus(path string) (AudioObservation, error) { return parseOggStream(path, true) }

func parseOggPacket(packet []byte, o *AudioObservation, codecHeader, opusHead *bool) error {
	if bytes.HasPrefix(packet, []byte("\x01vorbis")) {
		*codecHeader = true
		o.Codec = "vorbis"
		if len(packet) >= 30 {
			o.Channels = int(packet[11])
			o.SampleRate = int(binary.LittleEndian.Uint32(packet[12:16]))
			if nominal := int32(binary.LittleEndian.Uint32(packet[20:24])); nominal > 0 {
				o.Bitrate = int(nominal / 1000)
			}
		}
	}
	if bytes.HasPrefix(packet, []byte("\x03vorbis")) {
		if err := parseVorbisComments(packet[7:], o); err != nil {
			return err
		}
	}
	if bytes.HasPrefix(packet, []byte("OpusTags")) {
		o.SourceKind = "opus_tags"
		if err := parseVorbisComments(packet[8:], o); err != nil {
			return err
		}
	}
	if bytes.HasPrefix(packet, []byte("OpusHead")) {
		*opusHead = true
		*codecHeader = true
		o.Codec = "opus"
		if len(packet) >= 19 {
			o.Channels = int(packet[9])
			o.SampleRate = 48000
		}
	}
	return nil
}
