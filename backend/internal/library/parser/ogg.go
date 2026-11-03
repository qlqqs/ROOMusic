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
			parseVorbisComments(packet[7:], &o)
		}
		if bytes.HasPrefix(packet, []byte("OpusTags")) {
			parseVorbisComments(packet[8:], &o)
			o.SourceKind = "opus_tags"
		}
		if bytes.HasPrefix(packet, []byte("OpusHead")) {
			opusHead = true
			codecHeader = true
			o.Codec = "opus"
			if len(packet) >= 19 {
				o.Channels = int(packet[9])
				o.SampleRate = 48000
			}
		}
		pos = end + total
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
	return applyFilenameFallback(path, o), nil
}

func parseOpus(path string) (AudioObservation, error) { return parseOggStream(path, true) }
