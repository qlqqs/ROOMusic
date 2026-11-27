package parser

import (
	"bytes"
	"errors"
	"io"
)

var ErrUnknownFormat = errors.New("无法识别音频格式")

func detectFormat(reader io.ReaderAt, size int64) (string, error) {
	var head [16]byte
	n, err := reader.ReadAt(head[:], 0)
	if err != nil && err != io.EOF {
		return "", err
	}
	b := head[:n]
	if bytes.HasPrefix(b, []byte("ID3")) {
		end, err := apeStreamStart(reader, size)
		if err != nil {
			return "", err
		}
		if len(b) < 10 || b[3] < 2 || b[3] > 4 {
			return "", ErrUnknownFormat
		}
		if end == size {
			return ".mp3", nil
		}
		n, err = reader.ReadAt(head[:], end)
		if err != nil && err != io.EOF {
			return "", err
		}
		b = head[:n]
		if bytes.HasPrefix(b, []byte("MAC ")) {
			return ".ape", nil
		}
		if _, _, _, ok := decodeMP3FrameHeader(b); ok {
			return ".mp3", nil
		}
		return "", ErrUnknownFormat
	}
	if len(b) < 4 {
		return "", ErrUnknownFormat
	}
	switch string(b[:4]) {
	case "fLaC":
		return ".flac", nil
	case "MAC ":
		return ".ape", nil
	case "wvpk":
		return ".wv", nil
	case "DSD ":
		return ".dsf", nil
	case "FRM8":
		if len(b) >= 16 && string(b[12:16]) == "DSD " {
			return ".dff", nil
		}
	case "RIFF":
		if len(b) >= 12 && string(b[8:12]) == "WAVE" {
			return ".wav", nil
		}
	case "FORM":
		if len(b) >= 12 && (string(b[8:12]) == "AIFF" || string(b[8:12]) == "AIFC") {
			return ".aiff", nil
		}
	case "OggS":
		page := newOggPageReader(io.NewSectionReader(reader, 0, size))
		segments, err := page.nextPage()
		if err != nil {
			return "", err
		}
		if len(segments) == 0 {
			return "", ErrUnknownFormat
		}
		first, err := page.readSegment(segments[0])
		if err != nil {
			return "", err
		}
		if bytes.HasPrefix(first, []byte("OpusHead")) {
			return ".opus", nil
		}
		if bytes.HasPrefix(first, []byte("\x01vorbis")) {
			return ".ogg", nil
		}
		return "", ErrUnknownFormat
	}
	if _, _, _, ok := decodeMP3FrameHeader(b); ok {
		return ".mp3", nil
	}
	if len(b) >= 8 {
		switch string(b[4:8]) {
		case "ftyp", "free", "wide", "skip", "moov", "mdat":
			for offset, count := int64(0), 0; offset < size && count < maxM4AAtoms; count++ {
				atom, err := readM4AAtomHeader(reader, offset, size)
				if err != nil {
					return "", err
				}
				if atom.Type == "ftyp" {
					brand, err := readM4ABrand(reader, atom)
					if err != nil {
						return "", err
					}
					if supportedM4ABrand(brand) {
						return ".m4a", nil
					}
					break
				}
				offset = atom.End
			}
		}
	}
	return "", ErrUnknownFormat
}
