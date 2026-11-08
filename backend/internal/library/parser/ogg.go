package parser

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
)

func parseOgg(path string) (AudioObservation, error) {
	return parseOggStream(path, false)
}

func parseOggStream(path string, requireOpus bool) (AudioObservation, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return AudioObservation{}, err
	}
	if len(data) < 27 || string(data[:4]) != "OggS" {
		return AudioObservation{}, fmt.Errorf("invalid OGG stream")
	}
	o := AudioObservation{SourceKind: "ogg_vorbis_comment", InferredFields: map[string]bool{}}
	opusHead := false
	codecHeader := false
	// 注释可跨多个页，一页也可包含多个包；只有完整包才能产生署名证据。
	var packet []byte
	pos := 0
	for pos+27 <= len(data) {
		if string(data[pos:pos+4]) != "OggS" {
			return o, fmt.Errorf("invalid OGG page")
		}
		table := pos + 27
		cursor := table + int(data[pos+26])
		if cursor > len(data) {
			return o, fmt.Errorf("truncated OGG page")
		}
		for _, segmentLength := range data[table:cursor] {
			end := cursor + int(segmentLength)
			if end > len(data) {
				return o, fmt.Errorf("truncated OGG packet")
			}
			if len(packet)+int(segmentLength) > 16<<20 {
				return o, fmt.Errorf("OGG packet too large")
			}
			packet = append(packet, data[cursor:end]...)
			cursor = end
			if segmentLength == 255 {
				continue
			}
			if err := parseOggPacket(packet, &o, &codecHeader, &opusHead); err != nil {
				return o, err
			}
			packet = packet[:0]
		}
		pos = cursor
	}
	if len(packet) != 0 {
		return o, fmt.Errorf("truncated OGG packet")
	}
	if pos != len(data) {
		return o, fmt.Errorf("invalid OGG trailing data")
	}
	if !codecHeader || (requireOpus && !opusHead) {
		return o, fmt.Errorf("invalid Opus stream")
	}
	// 指纹只是补充事实：头部不符合指纹规则的流仍按原规则解析，由按需计算路径报告失败。
	if fingerprint, fingerprintErr := oggAudioFingerprint(data); fingerprintErr == nil {
		o.ContentFingerprint = fingerprint
	}
	finalizeTags(&o)
	return applyFilenameFallback(path, o), nil
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
