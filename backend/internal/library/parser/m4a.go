package parser

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
)

func parseM4A(path string) (AudioObservation, error) {
	file, err := os.Open(path)
	if err != nil {
		return AudioObservation{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return AudioObservation{}, err
	}
	if info.Size() < 8 {
		return AudioObservation{}, fmt.Errorf("invalid M4A stream")
	}

	o := AudioObservation{
		SourceKind:     "m4a",
		InferredFields: map[string]bool{},
		RawAtoms:       make(map[string]string),
	}
	budget := m4aParseBudget{remaining: maxM4AMetadataTotal, atomsRemaining: maxM4AAtoms}
	var foundFTYP bool
	var brand string
	for offset := int64(0); offset < info.Size(); {
		atom, headerErr := readM4AAtomHeader(file, offset, info.Size())
		if headerErr != nil {
			return AudioObservation{}, fmt.Errorf("invalid M4A atom at %d: %w", offset, headerErr)
		}
		if budgetErr := consumeM4AAtomBudget(&budget); budgetErr != nil {
			return AudioObservation{}, budgetErr
		}
		switch atom.Type {
		case "ftyp":
			if foundFTYP {
				// 多个 ftyp 不符合规范；保留第一个可兼容带有无害尾部元数据的旧文件。
				offset = atom.End
				continue
			}
			brand, err = readM4ABrand(file, atom)
			if err != nil {
				return AudioObservation{}, err
			}
			if !supportedM4ABrand(brand) {
				return AudioObservation{}, fmt.Errorf("unsupported M4A brand")
			}
			foundFTYP = true
		case "moov":
			if err := walkM4AAtoms(file, atom.PayloadStart, atom.End, &o, &budget, 0, "moov"); err != nil {
				return AudioObservation{}, fmt.Errorf("invalid M4A metadata: %w", err)
			}
		}
		if atom.End <= offset {
			return AudioObservation{}, fmt.Errorf("invalid M4A atom size")
		}
		offset = atom.End
	}
	if !foundFTYP {
		return AudioObservation{}, fmt.Errorf("invalid M4A stream: missing ftyp")
	}
	if !supportedM4ABrand(brand) {
		return AudioObservation{}, fmt.Errorf("unsupported M4A brand")
	}
	if o.Codec != "aac" && o.Codec != "alac" {
		return AudioObservation{}, fmt.Errorf("invalid M4A stream: missing supported audio sample entry")
	}
	if o.Bitrate == 0 && o.DurationSeconds > 0 {
		// 这里只提供容器级粗略估算，不据此推断发行或来源语义。
		o.Bitrate = estimatedBitrateKbps(info.Size(), o.DurationSeconds)
	}
	finalizeTags(&o)
	return applyFilenameFallback(path, o), nil
}

func parseM4AAtoms(data []byte, o *AudioObservation) {
	if o == nil {
		return
	}
	if o.InferredFields == nil {
		o.InferredFields = make(map[string]bool)
	}
	if o.RawAtoms == nil {
		o.RawAtoms = make(map[string]string)
	}
	reader := bytes.NewReader(data)
	budget := m4aParseBudget{remaining: maxM4AMetadataTotal, atomsRemaining: maxM4AAtoms}
	_ = walkM4AAtoms(reader, 0, int64(len(data)), o, &budget, 0, "")
}

type m4aParseBudget struct {
	remaining      int64
	atomsRemaining int
}

type m4aAtom struct {
	Offset       int64
	Size         int64
	PayloadStart int64
	End          int64
	Type         string
}

func readM4AAtomHeader(reader io.ReaderAt, offset, limit int64) (m4aAtom, error) {
	if offset < 0 || limit < offset || limit-offset < 8 {
		return m4aAtom{}, io.ErrUnexpectedEOF
	}
	var header [16]byte
	if _, err := reader.ReadAt(header[:8], offset); err != nil {
		return m4aAtom{}, err
	}
	size32 := binary.BigEndian.Uint32(header[:4])
	headerSize := int64(8)
	size := uint64(size32)
	if size32 == 1 {
		if limit-offset < 16 {
			return m4aAtom{}, io.ErrUnexpectedEOF
		}
		if _, err := reader.ReadAt(header[8:16], offset+8); err != nil {
			return m4aAtom{}, err
		}
		size = binary.BigEndian.Uint64(header[8:16])
		headerSize = 16
	} else if size32 == 0 {
		size = uint64(limit - offset)
	}
	if size < uint64(headerSize) {
		return m4aAtom{}, errors.New("atom size smaller than header")
	}
	if size > uint64(limit-offset) || size > uint64(math.MaxInt64) {
		return m4aAtom{}, errors.New("truncated atom")
	}
	typ := string(header[4:8])
	return m4aAtom{Offset: offset, Size: int64(size), PayloadStart: offset + headerSize, End: offset + int64(size), Type: typ}, nil
}

func readM4ABrand(reader io.ReaderAt, atom m4aAtom) (string, error) {
	if atom.Size < atom.PayloadStart-atom.Offset+8 {
		return "", errors.New("truncated ftyp atom")
	}
	var major [4]byte
	if _, err := reader.ReadAt(major[:], atom.PayloadStart); err != nil {
		return "", fmt.Errorf("read ftyp: %w", err)
	}
	return string(major[:]), nil
}

func supportedM4ABrand(brand string) bool {
	switch brand {
	case "M4A ", "M4B ", "M4P ", "isom", "iso2", "iso3", "iso4", "iso5", "iso6", "mp41", "mp42", "qt  ":
		return true
	default:
		return false
	}
}

func walkM4AAtoms(reader io.ReaderAt, start, limit int64, observation *AudioObservation, budget *m4aParseBudget, depth int, parent string) error {
	if depth > maxM4ARecursion {
		return errors.New("atom nesting too deep")
	}
	children := 0
	for offset := start; offset < limit; {
		children++
		if children > maxM4AChildren {
			return errors.New("too many atoms")
		}
		atom, err := readM4AAtomHeader(reader, offset, limit)
		if err != nil {
			return fmt.Errorf("atom %q at %d: %w", parent, offset, err)
		}
		if err := consumeM4AAtomBudget(budget); err != nil {
			return err
		}
		if atom.End <= offset {
			return errors.New("atom does not advance")
		}
		switch atom.Type {
		case "moov", "trak", "mdia", "minf", "stbl", "udta", "edts", "dinf", "sinf", "schi", "ilst":
			if err := walkM4AAtoms(reader, atom.PayloadStart, atom.End, observation, budget, depth+1, atom.Type); err != nil {
				return err
			}
		case "meta":
			// meta 是 full box，前四字节为 version/flags。
			if atom.End-atom.PayloadStart < 4 {
				return errors.New("truncated meta atom")
			}
			if err := walkM4AAtoms(reader, atom.PayloadStart+4, atom.End, observation, budget, depth+1, atom.Type); err != nil {
				return err
			}
		case "mvhd", "mdhd":
			if err := parseM4ADuration(reader, atom, observation, budget); err != nil {
				return err
			}
		case "stsd":
			if err := parseM4ASampleDescription(reader, atom, observation, budget); err != nil {
				return err
			}
		case "btrt":
			if err := parseM4ABitrate(reader, atom, observation, budget); err != nil {
				return err
			}
		default:
			if parent == "ilst" {
				if err := parseM4AItem(reader, atom, observation, budget); err != nil {
					return err
				}
			}
		}
		offset = atom.End
	}
	if start > limit {
		return errors.New("atom range outside parent")
	}
	return nil
}

func readM4AAtomPayload(reader io.ReaderAt, atom m4aAtom, budget int64) ([]byte, error) {
	payloadSize := atom.End - atom.PayloadStart
	if payloadSize < 0 {
		return nil, errors.New("negative atom payload")
	}
	if payloadSize > budget {
		return nil, errors.New("M4A metadata too large")
	}
	payload := make([]byte, int(payloadSize))
	if payloadSize == 0 {
		return payload, nil
	}
	if _, err := reader.ReadAt(payload, atom.PayloadStart); err != nil {
		return nil, fmt.Errorf("read atom payload: %w", err)
	}
	return payload, nil
}

func consumeM4AMetadataBudget(budget *m4aParseBudget, size int64) error {
	if size < 0 {
		return errors.New("negative metadata size")
	}
	if budget == nil || budget.remaining < size {
		return errors.New("M4A metadata budget exceeded")
	}
	budget.remaining -= size
	return nil
}

func consumeM4AAtomBudget(budget *m4aParseBudget) error {
	if budget == nil || budget.atomsRemaining <= 0 {
		return errors.New("too many M4A atoms")
	}
	budget.atomsRemaining--
	return nil
}

func parseM4ADuration(reader io.ReaderAt, atom m4aAtom, observation *AudioObservation, budget *m4aParseBudget) error {
	if err := consumeM4AMetadataBudget(budget, atom.End-atom.PayloadStart); err != nil {
		return err
	}
	payload, err := readM4AAtomPayload(reader, atom, 128)
	if err != nil {
		return err
	}
	if len(payload) < 4 {
		return errors.New("truncated duration atom")
	}
	version := payload[0]
	var timescale uint32
	var duration uint64
	switch version {
	case 0:
		if len(payload) < 20 {
			return errors.New("truncated duration atom")
		}
		timescale = binary.BigEndian.Uint32(payload[12:16])
		duration = uint64(binary.BigEndian.Uint32(payload[16:20]))
		if duration == math.MaxUint32 {
			duration = 0
		}
	case 1:
		if len(payload) < 32 {
			return errors.New("truncated duration atom")
		}
		timescale = binary.BigEndian.Uint32(payload[20:24])
		duration = binary.BigEndian.Uint64(payload[24:32])
		if duration == math.MaxUint64 {
			duration = 0
		}
	default:
		return errors.New("unsupported duration atom version")
	}
	if timescale > 0 && duration > 0 && observation.DurationSeconds == 0 {
		observation.DurationSeconds = float64(duration) / float64(timescale)
	}
	return nil
}

func parseM4ASampleDescription(reader io.ReaderAt, atom m4aAtom, observation *AudioObservation, budget *m4aParseBudget) error {
	if err := consumeM4AMetadataBudget(budget, atom.End-atom.PayloadStart); err != nil {
		return err
	}
	payload, err := readM4AAtomPayload(reader, atom, maxM4AMetadataAtom)
	if err != nil {
		return err
	}
	if len(payload) < 8 {
		return errors.New("truncated stsd atom")
	}
	entryCount := binary.BigEndian.Uint32(payload[4:8])
	entryOffset := 8
	for entryIndex := uint32(0); entryIndex < entryCount; entryIndex++ {
		if len(payload) < entryOffset+8 {
			return errors.New("truncated stsd sample entry")
		}
		entrySize := int(binary.BigEndian.Uint32(payload[entryOffset : entryOffset+4]))
		if entrySize < 8 || entryOffset > len(payload)-entrySize {
			return errors.New("truncated stsd sample entry")
		}
		entry := payload[entryOffset : entryOffset+entrySize]
		codec := string(entry[4:8])
		supported := true
		switch codec {
		case "mp4a":
			observation.Codec = "aac"
		case "alac":
			observation.Codec = "alac"
		case "enca":
			// 加密 sample entry 通常在 frma 子 atom 中记录原 codec；没有该证据时保守标为 AAC。
			observation.Codec = "aac"
		default:
			supported = false
		}
		if supported && len(entry) >= 36 {
			observation.Channels = int(binary.BigEndian.Uint16(entry[24:26]))
			observation.BitDepth = int(binary.BigEndian.Uint16(entry[26:28]))
			observation.SampleRate = int(binary.BigEndian.Uint32(entry[32:36]) >> 16)
			childrenOffset := 36
			version := binary.BigEndian.Uint16(entry[16:18])
			switch version {
			case 1:
				childrenOffset = 52
			case 2:
				childrenOffset = 72
			}
			if childrenOffset < len(entry) {
				if err := parseM4ASampleEntryChildren(entry[childrenOffset:], observation); err != nil {
					return err
				}
			}
		}
		entryOffset += entrySize
	}
	return nil
}

func parseM4ASampleEntryChildren(data []byte, observation *AudioObservation) error {
	return parseM4ASampleEntryChildrenDepth(data, observation, 0)
}

func parseM4ASampleEntryChildrenDepth(data []byte, observation *AudioObservation, depth int) error {
	if depth > 8 {
		return errors.New("M4A sample entry nesting too deep")
	}
	reader := bytes.NewReader(data)
	children := 0
	for offset := int64(0); offset < int64(len(data)); {
		children++
		if children > maxM4AChildren {
			return errors.New("too many M4A sample entry children")
		}
		atom, err := readM4AAtomHeader(reader, offset, int64(len(data)))
		if err != nil {
			return err
		}
		switch atom.Type {
		case "alac":
			payload, payloadErr := readM4AAtomPayload(reader, atom, 256)
			if payloadErr != nil {
				return payloadErr
			}
			if len(payload) >= 28 {
				if payload[9] > 0 {
					observation.BitDepth = int(payload[9])
				}
				if payload[13] > 0 {
					observation.Channels = int(payload[13])
				}
				if rate := binary.BigEndian.Uint32(payload[24:28]); rate > 0 {
					observation.SampleRate = int(rate)
				}
				if average := binary.BigEndian.Uint32(payload[20:24]); average > 0 {
					observation.Bitrate = int(average / 1000)
				}
			}
		case "btrt":
			payload, payloadErr := readM4AAtomPayload(reader, atom, 32)
			if payloadErr != nil {
				return payloadErr
			}
			if len(payload) >= 12 {
				if average := binary.BigEndian.Uint32(payload[8:12]); average > 0 {
					observation.Bitrate = int(average / 1000)
				}
			}
		case "frma":
			payload, payloadErr := readM4AAtomPayload(reader, atom, 16)
			if payloadErr != nil {
				return payloadErr
			}
			if len(payload) >= 4 {
				switch string(payload[:4]) {
				case "mp4a":
					observation.Codec = "aac"
				case "alac":
					observation.Codec = "alac"
				}
			}
		case "sinf", "schi", "wave":
			payload, payloadErr := readM4AAtomPayload(reader, atom, maxM4AMetadataAtom)
			if payloadErr != nil {
				return payloadErr
			}
			if err := parseM4ASampleEntryChildrenDepth(payload, observation, depth+1); err != nil {
				return err
			}
		}
		offset = atom.End
	}
	return nil
}

func parseM4ABitrate(reader io.ReaderAt, atom m4aAtom, observation *AudioObservation, budget *m4aParseBudget) error {
	if err := consumeM4AMetadataBudget(budget, atom.End-atom.PayloadStart); err != nil {
		return err
	}
	payload, err := readM4AAtomPayload(reader, atom, 32)
	if err != nil {
		return err
	}
	if len(payload) >= 12 {
		average := binary.BigEndian.Uint32(payload[8:12])
		if average > 0 {
			observation.Bitrate = int(average / 1000)
		}
	}
	return nil
}

func parseM4AItem(reader io.ReaderAt, atom m4aAtom, observation *AudioObservation, budget *m4aParseBudget) error {
	itemType := atom.Type
	// iTunes item 包含一个或多个 data 子 atom；只读取有界区域，封面也受同一预算限制。
	children := 0
	freeformMean := ""
	freeformName := ""
	type itemData struct {
		value    []byte
		typeCode [4]byte
	}
	values := make([]itemData, 0, 1)
	for offset := atom.PayloadStart; offset < atom.End; {
		children++
		if children > 64 {
			return errors.New("too many M4A item children")
		}
		child, err := readM4AAtomHeader(reader, offset, atom.End)
		if err != nil {
			return err
		}
		switch child.Type {
		case "data":
			if budgetErr := consumeM4AMetadataBudget(budget, child.End-child.PayloadStart); budgetErr != nil {
				return budgetErr
			}
			payload, payloadErr := readM4AAtomPayload(reader, child, maxM4AMetadataAtom)
			if payloadErr != nil {
				return payloadErr
			}
			if len(payload) >= 8 {
				var code [4]byte
				copy(code[:], payload[:4])
				values = append(values, itemData{value: append([]byte(nil), payload[8:]...), typeCode: code})
			}
		case "mean", "name":
			payloadSize := child.End - child.PayloadStart
			if budgetErr := consumeM4AMetadataBudget(budget, payloadSize); budgetErr != nil {
				return budgetErr
			}
			payload, payloadErr := readM4AAtomPayload(reader, child, metadata.MaxTextBytes+4)
			if payloadErr != nil {
				return payloadErr
			}
			if len(payload) < 4 {
				return errors.New("truncated M4A freeform metadata")
			}
			text := decodeM4AText(payload[4:], 1)
			if child.Type == "mean" {
				freeformMean = text
			} else {
				freeformName = text
			}
		}
		offset = child.End
	}
	for _, value := range values {
		if itemType == "----" {
			if err := parseM4AFreeformValue(freeformMean, freeformName, value.value, value.typeCode[:], observation); err != nil {
				return err
			}
			continue
		}
		if err := parseM4AItemValue(itemType, value.value, value.typeCode[:], observation); err != nil {
			return err
		}
	}
	return nil
}

func parseM4AItemValue(itemType string, value, typeCode []byte, observation *AudioObservation) error {
	if observation.RawAtoms == nil {
		observation.RawAtoms = make(map[string]string)
	}
	if itemType == "trkn" || itemType == "disk" {
		if len(value) >= 6 {
			number := int(binary.BigEndian.Uint16(value[2:4]))
			if itemType == "trkn" {
				if number > 0 {
					observation.TrackNumber = number
				}
			} else if number > 0 {
				observation.DiscNumber = number
			}
		}
		return nil
	}
	if itemType == "\xa9mvi" || itemType == "\xa9mvc" {
		if itemType == "\xa9mvi" && len(value) == 2 {
			collectWorkTag(observation, "©mvi", strconv.Itoa(int(binary.BigEndian.Uint16(value))))
		}
		return nil
	}
	if itemType == "cpil" {
		if len(value) == 1 && value[0] != 0 {
			observation.Compilation = true
		}
		return nil
	}
	if itemType == "covr" {
		if len(value) > 0 && len(value) <= int(maxM4AMetadataAtom) {
			observation.Artwork = append([]byte(nil), value...)
			switch binary.BigEndian.Uint32(typeCode) {
			case 13:
				observation.ArtworkMIME = "image/jpeg"
			case 14:
				observation.ArtworkMIME = "image/png"
			default:
				observation.ArtworkMIME = "application/octet-stream"
			}
		}
		return nil
	}
	if itemType == "\xa9lyr" {
		// 歌词保留换行且可达 64 KiB，不能走有界的单行文本解码，也不放进 RawAtoms 证据。
		setM4ALyrics(observation, value, typeCode)
		return nil
	}
	key := strings.ReplaceAll(itemType, "\xa9", "©")
	if role := artistTagRole(key); role != "" {
		raw := decodeM4ARawText(value, binary.BigEndian.Uint32(typeCode))
		if err := captureArtistTag(observation, key, raw); err != nil {
			return err
		}
	}
	text := decodeM4AText(value, binary.BigEndian.Uint32(typeCode))
	if text == "" {
		return nil
	}
	if _, present := observation.RawAtoms[itemType]; present || len(observation.RawAtoms) < maxM4ARawAtoms {
		observation.RawAtoms[itemType] = text
	}
	switch itemType {
	case "\xa9wrk", "\xa9mvn":
		collectWorkTag(observation, key, text)
	case "\xa9nam":
		observation.Title = text
	case "\xa9alb":
		observation.Album = text
	case "\xa9gen":
		observation.Genres = metadata.SplitGenres(append(observation.Genres, decodeM4ARawText(value, binary.BigEndian.Uint32(typeCode)))...)
	case "\xa9day":
		observation.Year = ParseYear(text)
	case "\xa9wrt", "\xa9con", "\xa9prf":
		addTagCredits(observation, key, decodeM4ARawText(value, binary.BigEndian.Uint32(typeCode)))
	case "\xa9cmt":
		// 评论仅作为 RawAtoms 证据保留，不映射到当前观察合同未拥有的业务字段。
	}
	return nil
}

func parseM4AFreeformValue(mean, name string, value, typeCode []byte, observation *AudioObservation) error {
	if mean != "com.apple.iTunes" || observation == nil {
		return nil
	}
	key := "----:" + name
	name = strings.ToUpper(strings.TrimSpace(name))
	if kind := personnelTagKind(name); kind == "PERSONNEL" || kind == "INVOLVEDPEOPLE" {
		return setAudioTag(observation, key, decodeM4ARawText(value, binary.BigEndian.Uint32(typeCode)))
	}
	if role := artistTagRole(name); role != "" {
		raw := decodeM4ARawText(value, binary.BigEndian.Uint32(typeCode))
		if err := captureArtistTagRole(observation, role, key, raw); err != nil {
			return err
		}
		if role != "track_artist" && role != "album_artist" {
			addTagCredits(observation, name, raw)
		}
		return nil
	}
	if collectWorkTag(observation, name, decodeM4AText(value, binary.BigEndian.Uint32(typeCode))) {
		return nil
	}
	if name == "COMPILATION" || name == "ORIGINALDATE" || name == "ORIGINALYEAR" {
		return setAudioTag(observation, key, decodeM4ARawText(value, binary.BigEndian.Uint32(typeCode)))
	}
	text := decodeM4AText(value, binary.BigEndian.Uint32(typeCode))
	if text == "" {
		return nil
	}
	switch name {
	case "LABEL":
		if observation.Label == "" {
			observation.Label = text
		}
	case "UPC":
		if observation.Barcode == "" {
			observation.Barcode = text
		}
	default:
		return nil
	}
	if observation.RawAtoms == nil {
		observation.RawAtoms = make(map[string]string)
	}
	key = "----:" + name
	if _, present := observation.RawAtoms[key]; present || len(observation.RawAtoms) < maxM4ARawAtoms {
		observation.RawAtoms[key] = text
	}
	return nil
}

func decodeM4AText(value []byte, dataType uint32) string {
	return metadata.BoundedText(decodeM4ARawText(bytes.Trim(value, " \t\r\n"), dataType))
}

func decodeM4ARawText(value []byte, dataType uint32) string {
	if len(value) == 0 {
		return ""
	}
	if dataType == 2 || (len(value) >= 2 && ((value[0] == 0xff && value[1] == 0xfe) || (value[0] == 0xfe && value[1] == 0xff))) {
		littleEndian := len(value) >= 2 && value[0] == 0xff && value[1] == 0xfe
		if len(value) >= 2 && ((value[0] == 0xff && value[1] == 0xfe) || (value[0] == 0xfe && value[1] == 0xff)) {
			value = value[2:]
		}
		if len(value)%2 != 0 {
			return ""
		}
		units := make([]uint16, len(value)/2)
		for i := range units {
			if littleEndian {
				units[i] = binary.LittleEndian.Uint16(value[i*2:])
			} else {
				units[i] = binary.BigEndian.Uint16(value[i*2:])
			}
		}
		if !validUTF16Units(units) {
			return ""
		}
		return strings.TrimRight(string(utf16.Decode(units)), "\x00")
	}
	value = bytes.TrimRight(value, "\x00")
	if utf8.Valid(value) {
		return string(value)
	}
	return ""
}
