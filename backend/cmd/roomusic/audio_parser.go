package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/transform"
)

type audioObservation struct {
	Title, Artist, Album    string
	AlbumArtist             string
	TrackNumber, DiscNumber int
	SourceKind              string
	Inferred                bool
	InferredFields          map[string]bool
	Artwork                 []byte
	ArtworkMIME             string
	DurationSeconds         float64
	Codec                   string
	BitDepth                int
	SampleRate              int
	Channels                int
	Bitrate                 int
	Year                    int
	Genre                   string
	Catalog                 string
	ISRC                    string
	SourceType              string
	MediaType               string
	Edition                 string
	Label                   string
	Barcode                 string
	Credits                 []creditObservation
	RawAtoms                map[string]string
}

type cueTrack struct {
	Number           int
	Title, Artist    string
	PerformerPresent bool
	IndexFrames      int
	IndexPresent     bool
	ReferencedFile   string
	FileType         string
	Indexes          map[int]int
	EndFrames        int
	EndPresent       bool
	DurationFrames   int
	DurationSeconds  float64
	ISRC             string
	SheetTitle       string
	SheetArtist      string
	Genre            string
	Date             string
	Catalog          string
	ReferenceStatus  string
}

// cueFileReference 保留每个 FILE 声明。兼容入口 parseCue 会把引用平铺到
// cueTrack.ReferencedFile，需要逐文件诊断的扫描流程则直接使用 parseCueDocument。
type cueFileReference struct {
	Path         string
	Type         string
	ResolvedPath string
	Status       string
	TrackNumbers []int
}

type cueDocument struct {
	Tracks      []cueTrack
	Files       []cueFileReference
	Title       string
	Artist      string
	Genre       string
	Date        string
	Catalog     string
	Encoding    string
	Diagnostics []string
}

const (
	maxM4AMetadataAtom    = int64(16 << 20)
	maxM4AMetadataTotal   = int64(32 << 20)
	maxM4ARecursion       = 32
	maxM4AChildren        = 10000
	maxM4AAtoms           = 10000
	maxCueBytes           = int64(8 << 20)
	maxCueDiagnostics     = 32
	maxCueDiagnosticBytes = 512
	maxCueLineBytes       = 64 << 10
	maxCueTokenBytes      = 4096
	maxCueFiles           = 4096
	maxCueTracks          = 10000
	maxM4ARawAtoms        = 64
	maxAudioTextBytes     = 4096
)

func parseM4A(path string) (audioObservation, error) {
	file, err := os.Open(path)
	if err != nil {
		return audioObservation{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return audioObservation{}, err
	}
	if info.Size() < 8 {
		return audioObservation{}, fmt.Errorf("invalid M4A stream")
	}

	o := audioObservation{
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
			return audioObservation{}, fmt.Errorf("invalid M4A atom at %d: %w", offset, headerErr)
		}
		if budgetErr := consumeM4AAtomBudget(&budget); budgetErr != nil {
			return audioObservation{}, budgetErr
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
				return audioObservation{}, err
			}
			if !supportedM4ABrand(brand) {
				return audioObservation{}, fmt.Errorf("unsupported M4A brand")
			}
			foundFTYP = true
		case "moov":
			if err := walkM4AAtoms(file, atom.PayloadStart, atom.End, &o, &budget, 0, "moov"); err != nil {
				return audioObservation{}, fmt.Errorf("invalid M4A metadata: %w", err)
			}
		}
		if atom.End <= offset {
			return audioObservation{}, fmt.Errorf("invalid M4A atom size")
		}
		offset = atom.End
	}
	if !foundFTYP {
		return audioObservation{}, fmt.Errorf("invalid M4A stream: missing ftyp")
	}
	if !supportedM4ABrand(brand) {
		return audioObservation{}, fmt.Errorf("unsupported M4A brand")
	}
	if o.Codec != "aac" && o.Codec != "alac" {
		return audioObservation{}, fmt.Errorf("invalid M4A stream: missing supported audio sample entry")
	}
	if o.Bitrate == 0 && o.DurationSeconds > 0 {
		// 这里只提供容器级粗略估算，不据此推断发行或来源语义。
		o.Bitrate = estimatedBitrateKbps(info.Size(), o.DurationSeconds)
	}
	return applyFilenameFallback(path, o), nil
}

func parseM4AAtoms(data []byte, o *audioObservation) {
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

func walkM4AAtoms(reader io.ReaderAt, start, limit int64, observation *audioObservation, budget *m4aParseBudget, depth int, parent string) error {
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

func parseM4ADuration(reader io.ReaderAt, atom m4aAtom, observation *audioObservation, budget *m4aParseBudget) error {
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

func parseM4ASampleDescription(reader io.ReaderAt, atom m4aAtom, observation *audioObservation, budget *m4aParseBudget) error {
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

func parseM4ASampleEntryChildren(data []byte, observation *audioObservation) error {
	return parseM4ASampleEntryChildrenDepth(data, observation, 0)
}

func parseM4ASampleEntryChildrenDepth(data []byte, observation *audioObservation, depth int) error {
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

func parseM4ABitrate(reader io.ReaderAt, atom m4aAtom, observation *audioObservation, budget *m4aParseBudget) error {
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

func parseM4AItem(reader io.ReaderAt, atom m4aAtom, observation *audioObservation, budget *m4aParseBudget) error {
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
			payload, payloadErr := readM4AAtomPayload(reader, child, maxAudioTextBytes+4)
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
			parseM4AFreeformValue(freeformMean, freeformName, value.value, value.typeCode[:], observation)
			continue
		}
		parseM4AItemValue(itemType, value.value, value.typeCode[:], observation)
	}
	return nil
}

func parseM4AItemValue(itemType string, value, typeCode []byte, observation *audioObservation) {
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
		return
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
		return
	}
	text := decodeM4AText(value, binary.BigEndian.Uint32(typeCode))
	if text == "" {
		return
	}
	if _, present := observation.RawAtoms[itemType]; present || len(observation.RawAtoms) < maxM4ARawAtoms {
		observation.RawAtoms[itemType] = text
	}
	switch itemType {
	case "\xa9nam":
		observation.Title = text
	case "\xa9ART":
		observation.Artist = text
	case "aART":
		observation.AlbumArtist = text
	case "\xa9alb":
		observation.Album = text
	case "\xa9gen":
		observation.Genre = text
	case "\xa9day":
		observation.Year = parseYear(text)
	case "\xa9wrt":
		addObservationCredits(observation, "composer", text)
	case "\xa9cmt":
		// 评论仅作为 RawAtoms 证据保留，不映射到当前观察合同未拥有的业务字段。
	}
}

func parseM4AFreeformValue(mean, name string, value, typeCode []byte, observation *audioObservation) {
	if mean != "com.apple.iTunes" || observation == nil {
		return
	}
	text := decodeM4AText(value, binary.BigEndian.Uint32(typeCode))
	if text == "" {
		return
	}
	name = strings.ToUpper(strings.TrimSpace(name))
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
		return
	}
	if observation.RawAtoms == nil {
		observation.RawAtoms = make(map[string]string)
	}
	key := "----:" + name
	if _, present := observation.RawAtoms[key]; present || len(observation.RawAtoms) < maxM4ARawAtoms {
		observation.RawAtoms[key] = text
	}
}

func decodeM4AText(value []byte, dataType uint32) string {
	value = bytes.Trim(value, " \t\r\n")
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
		return boundedAudioText(string(utf16.Decode(units)))
	}
	value = bytes.Trim(value, "\x00 \t\r\n")
	if utf8.Valid(value) {
		return boundedAudioText(string(value))
	}
	return ""
}

func boundedAudioText(value string) string {
	value = strings.TrimSpace(value)
	var result strings.Builder
	for _, character := range value {
		if unicode.IsControl(character) {
			continue
		}
		width := utf8.RuneLen(character)
		if width < 0 || result.Len()+width > maxAudioTextBytes {
			break
		}
		result.WriteRune(character)
	}
	return strings.TrimSpace(result.String())
}

func parseYear(value string) int {
	value = strings.TrimSpace(value)
	if len(value) < 4 {
		return 0
	}
	year, err := strconv.Atoi(value[:4])
	if err != nil || year < 1 {
		return 0
	}
	return year
}

func parseCue(path string) ([]cueTrack, string, error) {
	document, err := parseCueDocument(path)
	if err != nil {
		return nil, "", err
	}
	if len(document.Files) == 0 || len(document.Tracks) == 0 {
		return nil, "", fmt.Errorf("incomplete CUE")
	}
	for _, track := range document.Tracks {
		if !track.IndexPresent {
			return nil, "", fmt.Errorf("missing INDEX 01")
		}
	}
	return document.Tracks, document.Files[0].Path, nil
}

// parseCueDocument 保留全部 FILE、sheet 元数据和有界逐引用诊断。可选根目录用于
// 扫描级 containment，省略时保守限定在 CUE 目录；缺失或不安全的引用仍携带状态。
func parseCueDocument(path string, containmentRoot ...string) (cueDocument, error) {
	file, err := os.Open(path)
	if err != nil {
		return cueDocument{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return cueDocument{}, err
	}
	if info.Size() > maxCueBytes {
		return cueDocument{}, fmt.Errorf("CUE metadata too large")
	}
	data := make([]byte, int(info.Size()))
	if _, err := io.ReadFull(file, data); err != nil {
		return cueDocument{}, err
	}
	content, encoding, err := decodeCueText(data)
	if err != nil {
		return cueDocument{}, err
	}
	document := cueDocument{Encoding: encoding}
	boundary := filepath.Dir(path)
	if len(containmentRoot) > 0 && strings.TrimSpace(containmentRoot[0]) != "" {
		boundary = containmentRoot[0]
	}
	var currentFile *cueFileReference
	var currentTrack *cueTrack
	var currentFileID int
	var nextFileID int
	var trackCount int
	trackFileIDs := make([]int, 0)
	flushTrack := func() {
		if currentTrack == nil || currentFile == nil {
			return
		}
		currentTrack.ReferencedFile = currentFile.Path
		currentTrack.FileType = currentFile.Type
		currentTrack.ReferenceStatus = currentFile.Status
		currentTrack.SheetTitle = document.Title
		currentTrack.SheetArtist = document.Artist
		currentTrack.Genre = document.Genre
		currentTrack.Date = document.Date
		currentTrack.Catalog = document.Catalog
		currentFile.TrackNumbers = append(currentFile.TrackNumbers, currentTrack.Number)
		document.Tracks = append(document.Tracks, *currentTrack)
		trackFileIDs = append(trackFileIDs, currentFileID)
		currentTrack = nil
	}
	flushFile := func() {
		flushTrack()
		if currentFile != nil {
			document.Files = append(document.Files, *currentFile)
			currentFile = nil
		}
	}
	for lineNumber, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, ";") {
			continue
		}
		if len(line) > maxCueLineBytes {
			return cueDocument{}, fmt.Errorf("CUE line %d exceeds size limit", lineNumber+1)
		}
		tokens, tokenErr := cueTokens(line)
		if tokenErr != nil {
			return cueDocument{}, fmt.Errorf("CUE line %d: %w", lineNumber+1, tokenErr)
		}
		if len(tokens) == 0 {
			continue
		}
		for _, token := range tokens {
			if len(token) > maxCueTokenBytes {
				return cueDocument{}, fmt.Errorf("CUE line %d contains an overlong value", lineNumber+1)
			}
		}
		key := strings.ToUpper(tokens[0])
		switch key {
		case "FILE":
			if len(tokens) < 3 || !supportedCueFileType(tokens[2]) {
				return cueDocument{}, fmt.Errorf("CUE line %d: missing FILE type", lineNumber+1)
			}
			flushFile()
			if nextFileID >= maxCueFiles {
				return cueDocument{}, fmt.Errorf("CUE contains too many FILE declarations")
			}
			nextFileID++
			currentFileID = nextFileID
			ref := strings.TrimSpace(tokens[1])
			status, resolved, diagnostic := validateCueReference(path, boundary, ref)
			currentFile = &cueFileReference{Path: normalizeCueReference(ref), Type: strings.ToUpper(tokens[2]), ResolvedPath: resolved, Status: status}
			if diagnostic != "" {
				appendCueDiagnostic(&document, diagnostic)
			}
		case "TRACK":
			if currentFile == nil {
				return cueDocument{}, fmt.Errorf("CUE line %d: TRACK before FILE", lineNumber+1)
			}
			if len(tokens) < 3 || strings.ToUpper(tokens[2]) != "AUDIO" {
				return cueDocument{}, fmt.Errorf("CUE line %d: unsupported CUE track", lineNumber+1)
			}
			if trackCount >= maxCueTracks {
				return cueDocument{}, fmt.Errorf("CUE contains too many TRACK declarations")
			}
			number, numberErr := strconv.Atoi(tokens[1])
			if numberErr != nil || number < 1 || number > 999 {
				return cueDocument{}, fmt.Errorf("CUE line %d: invalid TRACK number", lineNumber+1)
			}
			flushTrack()
			currentTrack = &cueTrack{Number: number, Indexes: make(map[int]int)}
			trackCount++
		case "TITLE":
			value := cueValue(tokens[1:])
			if currentTrack != nil {
				currentTrack.Title = value
			} else {
				document.Title = value
			}
		case "PERFORMER":
			value := cueValue(tokens[1:])
			if currentTrack != nil {
				currentTrack.Artist = value
				currentTrack.PerformerPresent = true
			} else {
				document.Artist = value
			}
		case "REM":
			if len(tokens) < 3 {
				continue
			}
			value := cueValue(tokens[2:])
			switch strings.ToUpper(tokens[1]) {
			case "GENRE":
				document.Genre = value
			case "DATE", "YEAR":
				document.Date = value
			}
		case "CATALOG":
			document.Catalog = cueValue(tokens[1:])
		case "ISRC":
			if currentTrack != nil {
				currentTrack.ISRC = cueValue(tokens[1:])
			}
		case "INDEX":
			if currentTrack == nil || len(tokens) < 3 {
				continue
			}
			indexNumber, indexErr := strconv.Atoi(tokens[1])
			if indexErr != nil || indexNumber < 0 || indexNumber > 99 {
				return cueDocument{}, fmt.Errorf("CUE line %d: invalid INDEX number", lineNumber+1)
			}
			frames, frameErr := parseCueTimecode(tokens[2])
			if frameErr != nil {
				return cueDocument{}, fmt.Errorf("CUE line %d: %w", lineNumber+1, frameErr)
			}
			if currentTrack.Indexes == nil {
				currentTrack.Indexes = make(map[int]int)
			}
			if _, duplicate := currentTrack.Indexes[indexNumber]; duplicate {
				return cueDocument{}, fmt.Errorf("CUE line %d: duplicate INDEX number", lineNumber+1)
			}
			currentTrack.Indexes[indexNumber] = frames
			if indexNumber == 1 {
				currentTrack.IndexFrames = frames
				currentTrack.IndexPresent = true
			}
		}
	}
	flushFile()
	if len(document.Files) == 0 || len(document.Tracks) == 0 {
		return cueDocument{}, fmt.Errorf("incomplete CUE")
	}
	for fileIndex := range document.Files {
		fileRef := &document.Files[fileIndex]
		if setCueTrackRanges(document.Tracks, trackFileIDs, fileIndex+1, cueDurationFrames(fileRef)) {
			appendCueDiagnostic(&document, "non-monotonic INDEX 01 in FILE reference: "+fileRef.Path)
		}
	}
	for index := range document.Tracks {
		document.Tracks[index].SheetTitle = document.Title
		document.Tracks[index].SheetArtist = document.Artist
		document.Tracks[index].Genre = document.Genre
		document.Tracks[index].Date = document.Date
		document.Tracks[index].Catalog = document.Catalog
		if document.Tracks[index].Artist == "" {
			document.Tracks[index].Artist = document.Artist
		}
		if !document.Tracks[index].IndexPresent {
			appendCueDiagnostic(&document, fmt.Sprintf("missing INDEX 01 for track %d", document.Tracks[index].Number))
		}
	}
	return document, nil
}

func decodeCueText(data []byte) (string, string, error) {
	if bytes.HasPrefix(data, []byte{0x00, 0x00, 0xfe, 0xff}) || bytes.HasPrefix(data, []byte{0xff, 0xfe, 0x00, 0x00}) {
		return "", "", fmt.Errorf("unsupported UTF-32 CUE encoding")
	}
	if bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) {
		if !utf8.Valid(data[3:]) {
			return "", "", fmt.Errorf("unsupported CUE encoding")
		}
		return validatedCueText(string(data[3:]), "utf-8-bom")
	}
	if bytes.HasPrefix(data, []byte{0xff, 0xfe}) || bytes.HasPrefix(data, []byte{0xfe, 0xff}) {
		if len(data[2:])%2 != 0 {
			return "", "", fmt.Errorf("invalid UTF-16 CUE encoding")
		}
		littleEndian := data[0] == 0xff
		units := make([]uint16, (len(data)-2)/2)
		for index := range units {
			if littleEndian {
				units[index] = binary.LittleEndian.Uint16(data[2+index*2:])
			} else {
				units[index] = binary.BigEndian.Uint16(data[2+index*2:])
			}
		}
		if !validUTF16Units(units) {
			return "", "", fmt.Errorf("invalid UTF-16 CUE encoding")
		}
		return validatedCueText(string(utf16.Decode(units)), "utf-16")
	}
	if utf8.Valid(data) {
		return validatedCueText(string(data), "utf-8")
	}
	// 同时尝试现有资料库常见的 GBK、Shift-JIS 和 Big5，并用文本质量稳定裁决；
	// 解码错误不会通过替换字符静默吞掉。
	bestContent := ""
	bestEncoding := ""
	bestScore := math.MinInt
	for candidateIndex, candidate := range []struct {
		name string
		enc  transform.Transformer
	}{
		{name: "gbk", enc: simplifiedchinese.GBK.NewDecoder()},
		{name: "shift-jis", enc: japanese.ShiftJIS.NewDecoder()},
		{name: "big5", enc: traditionalchinese.Big5.NewDecoder()},
	} {
		decoded, _, decodeErr := transform.Bytes(candidate.enc, data)
		if decodeErr != nil || !utf8.Valid(decoded) || strings.ContainsRune(string(decoded), utf8.RuneError) || !validCueText(string(decoded)) {
			continue
		}
		content := string(decoded)
		score := cueDecodedTextScore(content)*10 - candidateIndex
		if score > bestScore {
			bestContent = content
			bestEncoding = candidate.name
			bestScore = score
		}
	}
	if bestContent != "" && bestScore > -1000 {
		return bestContent, bestEncoding, nil
	}
	return "", "", fmt.Errorf("unsupported CUE encoding")
}

func validatedCueText(value, encoding string) (string, string, error) {
	if !validCueText(value) {
		return "", "", fmt.Errorf("CUE contains unsupported control characters")
	}
	return value, encoding, nil
}

func validCueText(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) && character != '\n' && character != '\r' && character != '\t' {
			return false
		}
	}
	return true
}

func validUTF16Units(units []uint16) bool {
	for index := 0; index < len(units); index++ {
		unit := units[index]
		switch {
		case unit >= 0xd800 && unit <= 0xdbff:
			if index+1 >= len(units) || units[index+1] < 0xdc00 || units[index+1] > 0xdfff {
				return false
			}
			index++
		case unit >= 0xdc00 && unit <= 0xdfff:
			return false
		}
	}
	return true
}

func cueDecodedTextScore(value string) int {
	score := 0
	for _, character := range value {
		switch {
		case character == utf8.RuneError:
			score -= 200
		case unicode.IsControl(character) && character != '\n' && character != '\r' && character != '\t':
			score -= 100
		case character >= 0xff61 && character <= 0xff9f:
			// 普通 CUE 中的半角片假名常见于把 GBK 或 Big5 误解成 Shift-JIS。
			score -= 20
		case unicode.In(character, unicode.Hiragana, unicode.Katakana):
			score += 24
		case unicode.In(character, unicode.Han):
			score += 8
			if strings.ContainsRune("專輯藝術樂體國語錄聲華萬與為來時對門風龍學會", character) {
				score += 6
			}
			if strings.ContainsRune("专辑艺术乐体国语录声华万与为来时对门风龙学会", character) {
				score += 6
			}
		case unicode.IsLetter(character) || unicode.IsDigit(character):
			score += 3
		case unicode.IsSpace(character) || unicode.IsPunct(character) || unicode.IsSymbol(character):
			score++
		}
	}
	return score
}

func cueTokens(line string) ([]string, error) {
	var tokens []string
	var value strings.Builder
	inQuotes := false
	flush := func() {
		if value.Len() > 0 {
			tokens = append(tokens, value.String())
			value.Reset()
		}
	}
	for _, character := range line {
		if character == '"' {
			inQuotes = !inQuotes
			continue
		}
		if !inQuotes && (character == ' ' || character == '\t') {
			flush()
			continue
		}
		value.WriteRune(character)
	}
	if inQuotes {
		return nil, errors.New("unterminated quote")
	}
	flush()
	return tokens, nil
}

func cueValue(tokens []string) string {
	return truncateUTF8Bytes(strings.TrimSpace(strings.Join(tokens, " ")), maxCueTokenBytes)
}

func parseCueTimecode(value string) (int, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 3 {
		return 0, errors.New("invalid INDEX timecode")
	}
	minutes, minutesErr := strconv.ParseInt(parts[0], 10, 32)
	seconds, secondsErr := strconv.ParseInt(parts[1], 10, 8)
	frames, framesErr := strconv.ParseInt(parts[2], 10, 8)
	if minutesErr != nil || secondsErr != nil || framesErr != nil || minutes < 0 || seconds < 0 || frames < 0 || seconds >= 60 || frames >= 75 {
		return 0, errors.New("invalid INDEX timecode")
	}
	totalFrames := (minutes*60+seconds)*75 + frames
	if totalFrames > math.MaxInt32 {
		return 0, errors.New("INDEX timecode exceeds supported range")
	}
	return int(totalFrames), nil
}

func normalizeCueReference(reference string) string {
	reference = strings.TrimSpace(strings.ReplaceAll(reference, "\\", "/"))
	return filepath.ToSlash(filepath.Clean(filepath.FromSlash(reference)))
}

func validateCueReference(cuePath, containmentRoot, reference string) (status, resolved, diagnostic string) {
	if reference == "" || strings.IndexByte(reference, 0) >= 0 {
		return "unsafe", "", "empty or NUL FILE reference"
	}
	if filepath.IsAbs(reference) || strings.HasPrefix(reference, `\`) || (len(reference) >= 2 && reference[1] == ':') {
		return "unsafe", "", "unsafe absolute FILE reference"
	}
	normalized := normalizeCueReference(reference)
	if normalized == "." {
		return "unsafe", "", "empty FILE reference"
	}
	cueDir, err := filepath.Abs(filepath.Dir(cuePath))
	if err != nil {
		return "unchecked", "", "unable to resolve CUE directory"
	}
	boundary, err := filepath.Abs(containmentRoot)
	if err != nil {
		return "unchecked", "", "unable to resolve containment root"
	}
	if relative, relErr := filepath.Rel(boundary, cueDir); relErr != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "unsafe", "", "CUE file is outside containment root"
	}
	target := filepath.Clean(filepath.Join(cueDir, filepath.FromSlash(normalized)))
	relative, err := filepath.Rel(boundary, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "unsafe", "", "unsafe FILE path traversal"
	}
	resolved = target
	info, statErr := os.Stat(target)
	if statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return "missing", resolved, "missing FILE reference: " + normalized
		}
		return "missing", resolved, "unable to stat FILE reference: " + normalized
	}
	if !info.Mode().IsRegular() {
		return "unsafe", resolved, "FILE reference is not a regular file: " + normalized
	}
	realBoundary, boundaryErr := filepath.EvalSymlinks(boundary)
	realTarget, targetErr := filepath.EvalSymlinks(target)
	if boundaryErr != nil || targetErr != nil {
		return "unchecked", resolved, "unable to resolve FILE symlink: " + normalized
	}
	if relative, relErr := filepath.Rel(realBoundary, realTarget); relErr != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "unsafe", resolved, "FILE symlink escapes containment root: " + normalized
	}
	return "present", resolved, ""
}

func appendCueDiagnostic(document *cueDocument, diagnostic string) {
	if document == nil || len(document.Diagnostics) >= maxCueDiagnostics || diagnostic == "" {
		return
	}
	if len(diagnostic) > maxCueDiagnosticBytes {
		diagnostic = truncateUTF8Bytes(diagnostic, maxCueDiagnosticBytes)
	}
	document.Diagnostics = append(document.Diagnostics, diagnostic)
}

func safeRuneLimit(value string, maxBytes int) int {
	bytesUsed := 0
	runesUsed := 0
	for _, character := range value {
		runeBytes := utf8.RuneLen(character)
		if runeBytes < 0 || bytesUsed+runeBytes > maxBytes {
			break
		}
		bytesUsed += runeBytes
		runesUsed++
	}
	return runesUsed
}

func truncateUTF8Bytes(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	return string([]rune(value)[:safeRuneLimit(value, maxBytes)])
}

func cueDurationFrames(fileRef *cueFileReference) int {
	if fileRef == nil || fileRef.Status != "present" || fileRef.ResolvedPath == "" {
		return 0
	}
	observation, err := parseAudioFile(fileRef.ResolvedPath)
	if err != nil || observation.DurationSeconds <= 0 {
		return 0
	}
	if observation.DurationSeconds > float64(math.MaxInt32)/75 {
		return 0
	}
	return int(math.Round(observation.DurationSeconds * 75))
}

func setCueTrackRanges(tracks []cueTrack, trackFileIDs []int, fileID, parentDuration int) bool {
	indices := make([]int, 0)
	for index := range tracks {
		if index < len(trackFileIDs) && trackFileIDs[index] == fileID {
			indices = append(indices, index)
		}
	}
	nonMonotonic := false
	for index, trackIndex := range indices {
		track := &tracks[trackIndex]
		if !track.IndexPresent {
			continue
		}
		if index+1 < len(indices) && tracks[indices[index+1]].IndexPresent {
			track.EndFrames = tracks[indices[index+1]].IndexFrames
			track.EndPresent = track.EndFrames >= track.IndexFrames
			if !track.EndPresent {
				nonMonotonic = true
			}
		} else if parentDuration > track.IndexFrames {
			track.EndFrames = parentDuration
			track.EndPresent = true
		} else if parentDuration > 0 && parentDuration <= track.IndexFrames {
			nonMonotonic = true
		}
		if track.EndPresent {
			track.DurationFrames = track.EndFrames - track.IndexFrames
			if track.DurationFrames >= 0 {
				track.DurationSeconds = float64(track.DurationFrames) / 75
			}
		}
	}
	return nonMonotonic
}

func supportedCueFileType(value string) bool {
	switch strings.ToUpper(value) {
	case "WAVE", "BINARY", "AIFF", "MP3", "OGG", "OPUS", "FLAC":
		return true
	default:
		return false
	}
}

func parseAudioFile(path string) (audioObservation, error) {
	extension := strings.ToLower(filepath.Ext(path))
	switch extension {
	case ".flac":
		return parseFLAC(path)
	case ".mp3":
		return parseMP3(path)
	case ".ogg":
		return parseOgg(path)
	case ".opus":
		return parseOpus(path)
	case ".wav":
		return parseWAV(path)
	case ".m4a":
		return parseM4A(path)
	default:
		return audioObservation{}, fmt.Errorf("unsupported format")
	}
}

func parseOgg(path string) (audioObservation, error) {
	return parseOggStream(path, false)
}

func parseOggStream(path string, requireOpus bool) (audioObservation, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return audioObservation{}, err
	}
	if len(data) < 27 || string(data[:4]) != "OggS" {
		return audioObservation{}, fmt.Errorf("invalid OGG stream")
	}
	o := audioObservation{SourceKind: "ogg_vorbis_comment", InferredFields: map[string]bool{}}
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
	return applyFilenameFallback(path, o), nil
}

func parseOpus(path string) (audioObservation, error) { return parseOggStream(path, true) }

func parseWAV(path string) (audioObservation, error) {
	file, err := os.Open(path)
	if err != nil {
		return audioObservation{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return audioObservation{}, err
	}
	var header [12]byte
	if _, err := io.ReadFull(file, header[:]); err != nil || string(header[:4]) != "RIFF" || string(header[8:12]) != "WAVE" {
		return audioObservation{}, fmt.Errorf("invalid WAV stream")
	}
	declaredEnd := int64(binary.LittleEndian.Uint32(header[4:8])) + 8
	if declaredEnd < 12 || declaredEnd > info.Size() {
		return audioObservation{}, fmt.Errorf("truncated WAV stream")
	}
	o := audioObservation{SourceKind: "wav_info", InferredFields: map[string]bool{}, Codec: "wav"}
	var byteRate int
	var foundFormat bool
	var dataBytes int64
	for position := int64(12); position+8 <= declaredEnd; {
		var chunkHeader [8]byte
		if _, err := io.ReadFull(file, chunkHeader[:]); err != nil {
			return o, fmt.Errorf("truncated WAV chunk header")
		}
		position += 8
		id := string(chunkHeader[:4])
		size := int64(binary.LittleEndian.Uint32(chunkHeader[4:8]))
		paddedSize := size + size%2
		if size < 0 || paddedSize < size || position+paddedSize > declaredEnd {
			return o, fmt.Errorf("truncated WAV chunk")
		}
		switch id {
		case "fmt ":
			if size > 1<<20 {
				return o, fmt.Errorf("WAV format metadata too large")
			}
			chunk := make([]byte, int(size))
			if _, err := io.ReadFull(file, chunk); err != nil {
				return o, fmt.Errorf("truncated WAV format chunk")
			}
			if err := parseWAVFormat(chunk, &o, &byteRate); err != nil {
				return o, err
			}
			foundFormat = true
		case "LIST":
			if size > 16<<20 {
				return o, fmt.Errorf("WAV metadata too large")
			}
			chunk := make([]byte, int(size))
			if _, err := io.ReadFull(file, chunk); err != nil {
				return o, fmt.Errorf("truncated WAV LIST chunk")
			}
			parseWAVInfo(chunk, &o)
		case "data":
			dataBytes += size
			if _, err := file.Seek(size, io.SeekCurrent); err != nil {
				return o, fmt.Errorf("seek WAV data: %w", err)
			}
		default:
			if _, err := file.Seek(size, io.SeekCurrent); err != nil {
				return o, fmt.Errorf("seek WAV chunk: %w", err)
			}
		}
		if size%2 == 1 {
			if _, err := file.Seek(1, io.SeekCurrent); err != nil {
				return o, fmt.Errorf("seek WAV padding: %w", err)
			}
		}
		position += paddedSize
	}
	if !foundFormat || dataBytes <= 0 {
		return audioObservation{}, fmt.Errorf("invalid WAV stream: missing audio chunks")
	}
	if byteRate > 0 && dataBytes > 0 {
		o.DurationSeconds = float64(dataBytes) / float64(byteRate)
		o.Bitrate = byteRate * 8 / 1000
	}
	return applyFilenameFallback(path, o), nil
}

func parseWAVFormat(chunk []byte, observation *audioObservation, byteRate *int) error {
	if len(chunk) < 16 {
		return fmt.Errorf("invalid WAV format chunk")
	}
	format := binary.LittleEndian.Uint16(chunk[:2])
	observation.Channels = int(binary.LittleEndian.Uint16(chunk[2:4]))
	observation.SampleRate = int(binary.LittleEndian.Uint32(chunk[4:8]))
	*byteRate = int(binary.LittleEndian.Uint32(chunk[8:12]))
	observation.BitDepth = int(binary.LittleEndian.Uint16(chunk[14:16]))
	switch format {
	case 1:
		observation.Codec = "pcm"
	case 3:
		observation.Codec = "ieee_float"
	case 0xfffe:
		observation.Codec = "wav_extensible"
	default:
		observation.Codec = fmt.Sprintf("wav_%d", format)
	}
	if observation.Channels < 1 || observation.SampleRate < 1 || *byteRate < 1 {
		return fmt.Errorf("invalid WAV audio facts")
	}
	return nil
}

func parseWAVInfo(chunk []byte, observation *audioObservation) {
	if len(chunk) < 4 || string(chunk[:4]) != "INFO" {
		return
	}
	for position := 4; position+8 <= len(chunk); {
		key := string(chunk[position : position+4])
		size := int(binary.LittleEndian.Uint32(chunk[position+4 : position+8]))
		position += 8
		if size < 0 || position+size > len(chunk) {
			return
		}
		value := strings.TrimSpace(string(bytes.TrimRight(chunk[position:position+size], "\x00")))
		position += size
		if size%2 == 1 {
			position++
		}
		switch key {
		case "INAM":
			observation.Title = value
		case "IART":
			observation.Artist = value
		case "IPRD":
			observation.Album = value
		}
	}
}

func parseFLAC(path string) (audioObservation, error) {
	file, err := os.Open(path)
	if err != nil {
		return audioObservation{}, err
	}
	defer file.Close()
	magic := make([]byte, 4)
	if _, err = io.ReadFull(file, magic); err != nil || string(magic) != "fLaC" {
		return audioObservation{}, fmt.Errorf("invalid FLAC stream")
	}
	observation := audioObservation{SourceKind: "flac_vorbis_comment", InferredFields: make(map[string]bool), Codec: "flac"}
	var fileSize int64
	if info, statErr := file.Stat(); statErr == nil {
		fileSize = info.Size()
	}
	lastBlock := false
	for !lastBlock {
		header := make([]byte, 4)
		if _, err = io.ReadFull(file, header); err != nil {
			return observation, fmt.Errorf("read FLAC metadata: %w", err)
		}
		lastBlock = header[0]&0x80 != 0
		blockType := header[0] & 0x7f
		blockLength := int(header[1])<<16 | int(header[2])<<8 | int(header[3])
		if blockLength > 16<<20 {
			return observation, fmt.Errorf("FLAC metadata too large")
		}
		block := make([]byte, blockLength)
		if _, err = io.ReadFull(file, block); err != nil {
			return observation, err
		}
		switch blockType {
		case 0:
			if err := parseFLACStreamInfo(block, fileSize, &observation); err != nil {
				return observation, err
			}
		case 4:
			parseVorbisComments(block, &observation)
		}
	}
	return applyFilenameFallback(path, observation), nil
}

func parseFLACStreamInfo(block []byte, fileSize int64, observation *audioObservation) error {
	if len(block) != 34 {
		return fmt.Errorf("invalid FLAC STREAMINFO length")
	}
	sampleRate := int(block[10])<<12 | int(block[11])<<4 | int(block[12]>>4)
	channels := int((block[12]>>1)&0x07) + 1
	bitDepth := (int(block[12]&0x01)<<4 | int(block[13]>>4)) + 1
	totalSamples := int64(block[13]&0x0f)<<32 | int64(binary.BigEndian.Uint32(block[14:18]))
	if sampleRate <= 0 || channels <= 0 || bitDepth <= 0 {
		return fmt.Errorf("invalid FLAC STREAMINFO facts")
	}
	observation.SampleRate = sampleRate
	observation.Channels = channels
	observation.BitDepth = bitDepth
	if totalSamples > 0 {
		observation.DurationSeconds = float64(totalSamples) / float64(sampleRate)
	}
	if fileSize > 0 && observation.DurationSeconds > 0 {
		observation.Bitrate = estimatedBitrateKbps(fileSize, observation.DurationSeconds)
	}
	return nil
}

func estimatedBitrateKbps(fileSize int64, durationSeconds float64) int {
	if fileSize <= 0 || durationSeconds <= 0 || math.IsNaN(durationSeconds) || math.IsInf(durationSeconds, 0) {
		return 0
	}
	bitrate := float64(fileSize) * 8 / durationSeconds / 1000
	if bitrate <= 0 || math.IsNaN(bitrate) || math.IsInf(bitrate, 0) || bitrate > float64(math.MaxInt32) {
		return 0
	}
	return int(bitrate)
}

func parseVorbisComments(block []byte, observation *audioObservation) {
	reader := bufio.NewReader(strings.NewReader(string(block)))
	var vendorLength uint32
	if binary.Read(reader, binary.LittleEndian, &vendorLength) != nil {
		return
	}
	if vendorLength > uint32(len(block)) {
		return
	}
	commentVendor := make([]byte, vendorLength)
	if _, err := io.ReadFull(reader, commentVendor); err != nil {
		return
	}
	var commentCount uint32
	if binary.Read(reader, binary.LittleEndian, &commentCount) != nil {
		return
	}
	for index := uint32(0); index < commentCount; index++ {
		var commentLength uint32
		if binary.Read(reader, binary.LittleEndian, &commentLength) != nil || commentLength > 1<<20 {
			return
		}
		value := make([]byte, commentLength)
		if _, err := io.ReadFull(reader, value); err != nil {
			return
		}
		keyValue := strings.SplitN(string(value), "=", 2)
		if len(keyValue) != 2 {
			continue
		}
		setAudioTag(observation, keyValue[0], keyValue[1])
	}
}

func parseMP3(path string) (audioObservation, error) {
	file, err := os.Open(path)
	if err != nil {
		return audioObservation{}, err
	}
	defer file.Close()
	observation := audioObservation{SourceKind: "mp3_id3v2", InferredFields: make(map[string]bool), Codec: "mp3"}
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
		parseID3Frames(data, &observation)
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
	return applyFilenameFallback(path, observation), nil
}

func parseID3Frames(data []byte, observation *audioObservation) {
	for offset := 0; offset+10 <= len(data); {
		frameID := string(data[offset : offset+4])
		frameSize := int(binary.BigEndian.Uint32(data[offset+4 : offset+8]))
		offset += 10
		if frameSize <= 0 || offset+frameSize > len(data) {
			return
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
		if frameID != "TIT2" && frameID != "TPE1" && frameID != "TPE2" && frameID != "TPE3" && frameID != "TALB" && frameID != "TRCK" && frameID != "TPOS" && frameID != "TCON" && frameID != "TDRC" && frameID != "TYER" && frameID != "TSRC" && frameID != "TCOM" && frameID != "TPUB" {
			continue
		}
		if len(frame) < 2 {
			continue
		}
		value := decodeID3Text(frame)
		switch frameID {
		case "TIT2":
			observation.Title = value
		case "TPE1":
			observation.Artist = value
		case "TPE2":
			observation.AlbumArtist = value
		case "TALB":
			observation.Album = value
		case "TRCK":
			observation.TrackNumber = parseNumber(value)
		case "TPOS":
			observation.DiscNumber = parseNumber(value)
		case "TCON":
			observation.Genre = value
		case "TDRC", "TYER":
			observation.Year = parseYear(value)
		case "TSRC":
			observation.ISRC = value
		case "TCOM":
			addObservationCredits(observation, "composer", value)
		case "TPE3":
			addObservationCredits(observation, "conductor", value)
		case "TPUB":
			observation.Label = value
		}
	}
}

const maxMP3FrameSearchBytes = 64 << 10

var (
	mp3MPEG1Layer3Bitrates = [16]int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 0}
	mp3MPEG2Layer3Bitrates = [16]int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0}
	mp3MPEG1SampleRates    = [4]int{44100, 48000, 32000, 0}
)

// applyMP3AudioFacts 只在 ID3 后的有界窗口中寻找 MPEG Layer III 首帧，
// 不把整首音频读入内存。找不到有效帧时保留已解析标签，不伪造音频事实。
func applyMP3AudioFacts(reader io.ReaderAt, fileSize, audioStart int64, observation *audioObservation) {
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
		return boundedAudioText(decoded.String())
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
		return boundedAudioText(string(text))
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
	return boundedAudioText(string(utf16.Decode(units)))
}
func parseNumber(value string) int {
	returnValue, _ := strconv.Atoi(strings.SplitN(value, "/", 2)[0])
	return returnValue
}
func setAudioTag(observation *audioObservation, key, value string) {
	value = boundedAudioText(value)
	switch strings.ToUpper(key) {
	case "TITLE", "TRACKTITLE":
		observation.Title = value
	case "ARTIST":
		observation.Artist = value
	case "ALBUMARTIST", "ALBUM ARTIST":
		observation.AlbumArtist = value
	case "ALBUM", "ALBUMTITLE":
		observation.Album = value
	case "TRACKNUMBER":
		observation.TrackNumber = parseNumber(value)
	case "DISCNUMBER":
		observation.DiscNumber = parseNumber(value)
	case "GENRE":
		observation.Genre = value
	case "DATE", "YEAR":
		observation.Year = parseYear(value)
	case "CATALOG", "CATALOGNUMBER", "CATALOGNO":
		observation.Catalog = value
	case "LABEL", "PUBLISHER", "ORGANIZATION":
		if observation.Label == "" {
			observation.Label = value
		}
	case "BARCODE", "UPC", "EAN":
		if observation.Barcode == "" {
			observation.Barcode = value
		}
	case "EDITION", "VERSION", "SUBTITLE", "RELEASEVERSION":
		if observation.Edition == "" {
			observation.Edition = value
		}
	case "COMPOSER", "TCOM":
		addObservationCredits(observation, "composer", value)
	case "CONDUCTOR", "TPE3":
		addObservationCredits(observation, "conductor", value)
	case "PERFORMER", "PERFORMERS":
		addObservationCredits(observation, "performer", value)
	case "PRODUCER":
		addObservationCredits(observation, "producer", value)
	case "ISRC":
		observation.ISRC = value
	case "SOURCE", "SOURCETYPE":
		observation.SourceType = value
	case "MEDIA", "MEDIATYPE":
		observation.MediaType = value
	}
}

func addObservationCredits(observation *audioObservation, role, value string) {
	if observation == nil {
		return
	}
	role = strings.ToLower(normalizeValue(role))
	if role == "" {
		return
	}
	for _, name := range splitCreditNames(value) {
		credit := creditObservation{Role: role, Name: name}
		duplicate := false
		for _, existing := range observation.Credits {
			if existing == credit {
				duplicate = true
				break
			}
		}
		if !duplicate {
			observation.Credits = append(observation.Credits, credit)
		}
	}
}

func splitCreditNames(value string) []string {
	return canonicalArtistNames(value)
}
func applyFilenameFallback(path string, observation audioObservation) audioObservation {
	if observation.InferredFields == nil {
		observation.InferredFields = make(map[string]bool)
	}
	if observation.Title == "" {
		observation.Title = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		observation.Inferred = true
		observation.InferredFields["title"] = true
	}
	if observation.Artist == "" {
		observation.Artist = "未知艺术家"
		observation.Inferred = true
		observation.InferredFields["artist"] = true
	}
	if observation.Album == "" {
		observation.Album = filepath.Base(filepath.Dir(path))
		observation.Inferred = true
		observation.InferredFields["album"] = true
	}
	if observation.TrackNumber <= 0 {
		observation.TrackNumber = 1
		observation.Inferred = true
		observation.InferredFields["track_number"] = true
	}
	if observation.DiscNumber <= 0 {
		observation.DiscNumber = 1
		observation.Inferred = true
		observation.InferredFields["disc_number"] = true
	}
	return observation
}

type fieldObservation struct {
	Name, Value, SourceKind string
	Inferred                bool
}

func (observation audioObservation) fieldObservations() []fieldObservation {
	fields := []fieldObservation{
		{Name: "title", Value: observation.Title},
		{Name: "artist", Value: observation.Artist},
		{Name: "album", Value: observation.Album},
		{Name: "album_artist", Value: observation.AlbumArtist},
		{Name: "track_number", Value: strconv.Itoa(observation.TrackNumber)},
		{Name: "disc_number", Value: strconv.Itoa(observation.DiscNumber)},
	}
	for index := range fields {
		fields[index].Inferred = observation.InferredFields[fields[index].Name]
		fields[index].SourceKind = observation.SourceKind
		if fields[index].Inferred {
			fields[index].SourceKind = "filename_fallback"
		}
	}
	return fields
}
