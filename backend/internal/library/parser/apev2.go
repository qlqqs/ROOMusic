package parser

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const (
	maxAPEv2Body  = 16 << 20
	maxAPEv2Items = 1024
)

type apev2Location struct {
	bodyStart, bodySize int64
	count               uint32
}

// locateAPEv2 同时返回标签之前的边界，供旧版 APE 指纹排除尾部标签。
// 不可信的长度不能用于裁剪音频；标签体是否合法由 readAPEv2 独立检查。
func locateAPEv2(reader io.ReaderAt, size int64) (apev2Location, int64, bool, error) {
	end := size
	if end >= 128 {
		var marker [3]byte
		if _, err := reader.ReadAt(marker[:], end-128); err != nil {
			return apev2Location{}, end, false, err
		}
		if string(marker[:]) == "TAG" {
			end -= 128
		}
	}
	if end < 32 {
		return apev2Location{}, end, false, nil
	}
	var footer [32]byte
	if _, err := reader.ReadAt(footer[:], end-32); err != nil {
		return apev2Location{}, end, false, err
	}
	if string(footer[:8]) != "APETAGEX" {
		return apev2Location{}, end, false, nil
	}
	version := binary.LittleEndian.Uint32(footer[8:12])
	length := int64(binary.LittleEndian.Uint32(footer[12:16]))
	flags := binary.LittleEndian.Uint32(footer[20:24])
	if (version != 1000 && version != 2000) || length < 32 || length > end || flags&(1<<29) != 0 {
		return apev2Location{}, end, false, nil
	}
	start := end - length
	location := apev2Location{bodyStart: start, bodySize: length - 32, count: binary.LittleEndian.Uint32(footer[16:20])}
	if flags&(1<<31) != 0 {
		if start < 32 {
			return apev2Location{}, end, false, nil
		}
		var header [32]byte
		if _, err := reader.ReadAt(header[:], start-32); err != nil {
			return apev2Location{}, end, false, err
		}
		if !bytes.Equal(header[:20], footer[:20]) || binary.LittleEndian.Uint32(header[20:24])&(1<<29) == 0 {
			return apev2Location{}, end, false, nil
		}
		start -= 32
	}
	return location, start, true, nil
}

type apev2Item struct {
	key   string
	kind  uint32
	value []byte
}

func readAPEv2(reader io.ReaderAt, size int64, observation *AudioObservation) error {
	location, _, found, err := locateAPEv2(reader, size)
	if err != nil {
		return fmt.Errorf("read APEv2 footer: %w", err)
	}
	if !found || location.bodySize > maxAPEv2Body || location.count > maxAPEv2Items {
		return nil
	}
	body := make([]byte, location.bodySize)
	if _, err := reader.ReadAt(body, location.bodyStart); err != nil {
		return fmt.Errorf("read APEv2 body: %w", err)
	}
	items, valid := decodeAPEv2Items(body, location.count)
	if !valid {
		return nil
	}
	// 完整检查之后才写 observation，末项损坏也不会留下前面的半份标签。
	for _, item := range items {
		switch item.kind {
		case 0:
			key := strings.ToUpper(item.key)
			switch key {
			case "TRACK":
				key = "TRACKNUMBER"
			case "DISC":
				key = "DISCNUMBER"
			}
			for value := range strings.SplitSeq(string(item.value), "\x00") {
				if err := setAudioTag(observation, key, value); err != nil {
					return err
				}
			}
		case 1:
			if strings.EqualFold(item.key, "Cover Art (Front)") {
				applyAPEv2Artwork(item.value, observation)
			}
		}
	}
	return nil
}

func decodeAPEv2Items(body []byte, count uint32) ([]apev2Item, bool) {
	items := make([]apev2Item, 0, count)
	for range count {
		if len(body) < 9 {
			return nil, false
		}
		length := uint64(binary.LittleEndian.Uint32(body[:4]))
		kind := (binary.LittleEndian.Uint32(body[4:8]) >> 1) & 3
		body = body[8:]
		keyEnd := bytes.IndexByte(body, 0)
		if keyEnd < 1 {
			return nil, false
		}
		for _, character := range body[:keyEnd] {
			if character < 0x20 || character > 0x7e {
				return nil, false
			}
		}
		key := string(body[:keyEnd])
		body = body[keyEnd+1:]
		if length > uint64(len(body)) {
			return nil, false
		}
		value := body[:int(length)]
		if kind == 0 && !utf8.Valid(value) {
			return nil, false
		}
		items = append(items, apev2Item{key: key, kind: kind, value: value})
		body = body[int(length):]
	}
	return items, len(body) == 0
}

func applyAPEv2Artwork(value []byte, observation *AudioObservation) {
	nameEnd := bytes.IndexByte(value, 0)
	if nameEnd < 0 {
		return
	}
	image := value[nameEnd+1:]
	if len(image) == 0 || len(image) > 16<<20 {
		return
	}
	mime := ""
	switch {
	case bytes.HasPrefix(image, []byte{0xff, 0xd8}):
		mime = "image/jpeg"
	case bytes.HasPrefix(image, []byte("\x89PNG\r\n\x1a\n")):
		mime = "image/png"
	default:
		return
	}
	observation.Artwork = append([]byte(nil), image...)
	observation.ArtworkMIME = mime
}
