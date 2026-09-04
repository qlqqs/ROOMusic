package main

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"unicode/utf16"
)

func TestParseM4AReaderParsesLateMoovAndRawITunesAtoms(t *testing.T) {
	temporaryDirectory := t.TempDir()
	path := filepath.Join(temporaryDirectory, "late-moov.m4a")
	moov := repairM4AMoov("mp4a", 2, 24, 44100, 88200, "标题", "艺术家", "专辑", "专辑艺术家")
	// mdat 特意超过旧实现的 16 MiB 整文件上限；定位尾部 metadata 时不会读取该 payload。
	mdatPayload := bytes.Repeat([]byte{0x5a}, 17<<20)
	data := repairM4AAtom("ftyp", append([]byte("M4A "), []byte{0, 0, 0, 0}...))
	data = append(data, repairM4AAtom("mdat", mdatPayload)...)
	data = append(data, moov...)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write M4A fixture: %v", err)
	}

	observation, err := parseM4A(path)
	if err != nil {
		t.Fatalf("parse late moov M4A: %v", err)
	}
	if observation.Title != "标题" || observation.Artist != "艺术家" || observation.Album != "专辑" || observation.AlbumArtist != "专辑艺术家" {
		t.Fatalf("unexpected iTunes metadata: %+v", observation)
	}
	if observation.Codec != "aac" || observation.Channels != 2 || observation.SampleRate != 44100 {
		t.Fatalf("unexpected AAC facts: %+v", observation)
	}
	if observation.TrackNumber != 2 || observation.DiscNumber != 1 || observation.ArtworkMIME != "image/jpeg" || !bytes.Equal(observation.Artwork, []byte{0xff, 0xd8, 0xff, 0xd9}) {
		t.Fatalf("unexpected numeric tags or artwork: %+v", observation)
	}
	if observation.DurationSeconds != 2 || observation.Bitrate != 256 {
		t.Fatalf("unexpected duration/bitrate: %+v", observation)
	}
	if observation.RawAtoms[string([]byte{0xa9, 'n', 'a', 'm'})] != "标题" {
		t.Fatalf("raw atom key was not retained: %#v", observation.RawAtoms)
	}
}

func TestParseM4AReaderParsesALACConfigAndLargesize(t *testing.T) {
	temporaryDirectory := t.TempDir()
	path := filepath.Join(temporaryDirectory, "alac.m4a")
	moov := repairM4AMoov("alac", 2, 24, 96000, 192000, "", "", "", "")
	data := repairM4AAtom("ftyp", append([]byte("isom"), []byte{0, 0, 0, 0}...))
	data = append(data, repairM4AAtom64("free", []byte("padding"))...)
	data = append(data, moov...)
	data = append(data, repairM4AAtomToEOF("free", []byte("tail"))...)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write ALAC fixture: %v", err)
	}

	observation, err := parseM4A(path)
	if err != nil {
		t.Fatalf("parse ALAC M4A: %v", err)
	}
	if observation.Codec != "alac" || observation.Channels != 2 || observation.SampleRate != 96000 || observation.BitDepth != 24 {
		t.Fatalf("unexpected ALAC facts: %+v", observation)
	}
}

func TestParseM4AReaderRejectsTruncatedAtomWithoutPanic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "truncated.m4a")
	fixture := repairM4AAtom("ftyp", append([]byte("M4A "), []byte{0, 0, 0, 0}...))
	fixture = append(fixture, []byte{0, 0, 0, 32, 'm', 'o', 'o', 'v', 0, 0, 0}...)
	if err := os.WriteFile(path, fixture, 0o644); err != nil {
		t.Fatalf("write truncated fixture: %v", err)
	}
	if _, err := parseM4A(path); err == nil {
		t.Fatal("truncated M4A atom was accepted")
	}
}

func TestParseM4AReaderRejectsTooManyTopLevelAtoms(t *testing.T) {
	path := filepath.Join(t.TempDir(), "too-many-atoms.m4a")
	fixture := repairM4AAtom("ftyp", append([]byte("M4A "), []byte{0, 0, 0, 0}...))
	for index := 0; index < maxM4AAtoms; index++ {
		fixture = append(fixture, repairM4AAtom("free", nil)...)
	}
	if err := os.WriteFile(path, fixture, 0o644); err != nil {
		t.Fatalf("write excessive-atom fixture: %v", err)
	}
	if _, err := parseM4A(path); err == nil {
		t.Fatal("M4A with excessive top-level atoms was accepted")
	}
}

func TestParseM4AReaderRejectsContainerWithoutAudioSampleEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.m4a")
	fixture := repairM4AAtom("ftyp", append([]byte("M4A "), []byte{0, 0, 0, 0}...))
	if err := os.WriteFile(path, fixture, 0o644); err != nil {
		t.Fatalf("write empty M4A fixture: %v", err)
	}
	if _, err := parseM4A(path); err == nil {
		t.Fatal("M4A container without an audio sample entry was accepted")
	}
}

func TestParseM4ATextDecodingIsStrictAndBounded(t *testing.T) {
	units := utf16.Encode([]rune("标题"))
	utf16BigEndian := make([]byte, len(units)*2)
	for index, unit := range units {
		binary.BigEndian.PutUint16(utf16BigEndian[index*2:], unit)
	}
	if decoded := decodeM4AText(utf16BigEndian, 2); decoded != "标题" {
		t.Fatalf("unexpected UTF-16 M4A text: %q", decoded)
	}
	if decoded := decodeM4AText([]byte{0xff, 0xfe, 0xfd}, 1); decoded != "" {
		t.Fatalf("invalid M4A text was accepted: %q", decoded)
	}
	if decoded := decodeM4AText([]byte(strings.Repeat("x", maxAudioTextBytes+100)), 1); len(decoded) != maxAudioTextBytes {
		t.Fatalf("M4A text bound was not applied: %d", len(decoded))
	}
}

func TestParseM4AWriterAndAllowlistedFreeformMetadata(t *testing.T) {
	items := repairM4AAtom(string([]byte{0xa9, 'w', 'r', 't'}), repairM4AData(1, []byte("Composer One; Composer Two")))
	items = append(items, repairM4AFreeform("LABEL", "Example Label")...)
	items = append(items, repairM4AFreeform("UPC", "012345678901")...)
	items = append(items, repairM4AFreeform("PRIVATE_NOTE", "must stay unmapped")...)
	observation := audioObservation{InferredFields: map[string]bool{}, RawAtoms: map[string]string{}}
	parseM4AAtoms(repairM4AAtom("ilst", items), &observation)

	if observation.Label != "Example Label" || observation.Barcode != "012345678901" {
		t.Fatalf("M4A freeform metadata 未映射：%+v", observation)
	}
	if len(observation.Credits) != 2 || observation.Credits[0] != (creditObservation{Role: "composer", Name: "Composer One"}) || observation.Credits[1] != (creditObservation{Role: "composer", Name: "Composer Two"}) {
		t.Fatalf("M4A composer 未拆分：%+v", observation.Credits)
	}
	if observation.RawAtoms["----:PRIVATE_NOTE"] != "" {
		t.Fatal("未在 allowlist 中的 M4A freeform 字段进入了业务映射")
	}
}

func TestEstimatedBitrateRejectsUnrepresentableFacts(t *testing.T) {
	if bitrate := estimatedBitrateKbps(32_000, 1); bitrate != 256 {
		t.Fatalf("estimated bitrate = %d, want 256", bitrate)
	}
	for _, duration := range []float64{0, -1, math.NaN(), math.Inf(1), math.SmallestNonzeroFloat64} {
		if bitrate := estimatedBitrateKbps(math.MaxInt64, duration); bitrate != 0 {
			t.Fatalf("unrepresentable duration %v produced bitrate %d", duration, bitrate)
		}
	}
}

func TestParseExistingTagParsersRetainAlbumArtist(t *testing.T) {
	temporaryDirectory := t.TempDir()
	flacPath := filepath.Join(temporaryDirectory, "album-artist.flac")
	mp3Path := filepath.Join(temporaryDirectory, "album-artist.mp3")
	if err := os.WriteFile(flacPath, repairFLACComments("TITLE=Track", "ALBUMARTIST=Various Artists", "GENRE=Rock", "DATE=2024-01-01", "CATALOGNUMBER=CAT-001", "ISRC=USAAA2400001", "SOURCE=CD", "MEDIA=Compact Disc"), 0o644); err != nil {
		t.Fatalf("write FLAC fixture: %v", err)
	}
	if err := os.WriteFile(mp3Path, repairMP3Frames(map[string]string{"TIT2": "Track", "TPE2": "Various Artists", "TCON": "Rock", "TDRC": "2024", "TSRC": "USAAA2400001"}), 0o644); err != nil {
		t.Fatalf("write MP3 fixture: %v", err)
	}
	flac, err := parseFLAC(flacPath)
	if err != nil {
		t.Fatalf("parse FLAC album artist: %v", err)
	}
	mp3, err := parseMP3(mp3Path)
	if err != nil {
		t.Fatalf("parse MP3 album artist: %v", err)
	}
	if flac.AlbumArtist != "Various Artists" || mp3.AlbumArtist != "Various Artists" || flac.Codec != "flac" || mp3.Codec != "mp3" {
		t.Fatalf("album artist was lost: flac=%+v mp3=%+v", flac, mp3)
	}
	if flac.Genre != "Rock" || flac.Year != 2024 || flac.Catalog != "CAT-001" || flac.ISRC != "USAAA2400001" || mp3.Genre != "Rock" || mp3.Year != 2024 || mp3.ISRC != "USAAA2400001" {
		t.Fatalf("explicit tag facts were lost: flac=%+v mp3=%+v", flac, mp3)
	}
	if flac.SourceType != "CD" || flac.MediaType != "Compact Disc" || mp3.SourceType != "" || mp3.MediaType != "" {
		t.Fatalf("explicit source/media tags were not isolated from audio facts: flac=%+v mp3=%+v", flac, mp3)
	}
}

func TestParseFLACRetainsReleaseMetadataAndTrackCredits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metadata.flac")
	data := repairFLACComments(
		"TITLE=Track",
		"LABEL=Example Label",
		"UPC=012345678901",
		"EDITION=Deluxe Edition",
		"COMPOSER=Composer One; Composer Two",
		"COMPOSER=Composer Three",
	)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("写入 FLAC fixture：%v", err)
	}
	observation, err := parseFLAC(path)
	if err != nil {
		t.Fatalf("解析 FLAC metadata：%v", err)
	}
	if observation.Label != "Example Label" || observation.Barcode != "012345678901" || observation.Edition != "Deluxe Edition" {
		t.Fatalf("FLAC 发行字段丢失：%+v", observation)
	}
	wantCredits := []creditObservation{
		{Role: "composer", Name: "Composer One"},
		{Role: "composer", Name: "Composer Two"},
		{Role: "composer", Name: "Composer Three"},
	}
	if len(observation.Credits) != len(wantCredits) {
		t.Fatalf("FLAC credits 数量 = %d，期望 %d：%+v", len(observation.Credits), len(wantCredits), observation.Credits)
	}
	for index := range wantCredits {
		if observation.Credits[index] != wantCredits[index] {
			t.Fatalf("FLAC credit[%d] = %+v，期望 %+v", index, observation.Credits[index], wantCredits[index])
		}
	}
}

func TestParseMP3DecodesUTF16AndReadsMPEGFacts(t *testing.T) {
	title := repairUTF16("UTF16 Title", true)
	fixture := repairMP3BinaryFrame("TIT2", append([]byte{1}, title...))
	fixture = append(fixture, []byte{0xff, 0xfb, 0x90, 0x00}...)
	fixture = append(fixture, make([]byte, 16_000)...)
	path := filepath.Join(t.TempDir(), "utf16.mp3")
	if err := os.WriteFile(path, fixture, 0o644); err != nil {
		t.Fatalf("写入 MP3 fixture：%v", err)
	}
	observation, err := parseMP3(path)
	if err != nil {
		t.Fatalf("解析 MP3：%v", err)
	}
	if observation.Title != "UTF16 Title" {
		t.Fatalf("UTF-16 标题 = %q", observation.Title)
	}
	if observation.Bitrate != 128 || observation.SampleRate != 44_100 || observation.Channels != 2 || observation.DurationSeconds < 1 {
		t.Fatalf("MPEG 音频事实不完整：%+v", observation)
	}
}

func TestDecodeID3TextSupportsDeclaredEncodings(t *testing.T) {
	if got := decodeID3Text(append([]byte{0}, []byte{'c', 'a', 'f', 0xe9}...)); got != "café" {
		t.Fatalf("Latin-1 ID3 文本 = %q", got)
	}
	if got := decodeID3Text(append([]byte{2}, repairUTF16("标题", false)[2:]...)); got != "标题" {
		t.Fatalf("UTF-16BE ID3 文本 = %q", got)
	}
	if got := decodeID3Text(append([]byte{3}, []byte("标题")...)); got != "标题" {
		t.Fatalf("UTF-8 ID3 文本 = %q", got)
	}
	if got := decodeID3Text([]byte{1, 0, 'x'}); got != "" {
		t.Fatalf("无 BOM 的 ID3v2 UTF-16 文本被接受：%q", got)
	}
}

func TestParseMP3AttachedPictureKeepsMIMESeparateFromDescription(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artwork.mp3")
	artwork := []byte{0xff, 0xd8, 0xff, 0xd9}
	frame := append([]byte{0}, []byte("image/jpeg")...)
	frame = append(frame, 0, 3)
	frame = append(frame, []byte("front cover")...)
	frame = append(frame, 0)
	frame = append(frame, artwork...)
	if err := os.WriteFile(path, repairMP3BinaryFrame("APIC", frame), 0o644); err != nil {
		t.Fatalf("write MP3 artwork fixture: %v", err)
	}

	observation, err := parseMP3(path)
	if err != nil {
		t.Fatalf("parse MP3 artwork: %v", err)
	}
	if observation.ArtworkMIME != "image/jpeg" || !bytes.Equal(observation.Artwork, artwork) {
		t.Fatalf("unexpected MP3 artwork: MIME=%q data=%x", observation.ArtworkMIME, observation.Artwork)
	}
}

func TestFilenameFallbackMarksOnlyMissingPositionsInferred(t *testing.T) {
	inferred := applyFilenameFallback(filepath.Join("Album", "track.flac"), audioObservation{
		Title:          "Track",
		Artist:         "Artist",
		Album:          "Album",
		InferredFields: map[string]bool{},
	})
	if inferred.TrackNumber != 1 || inferred.DiscNumber != 1 || !inferred.InferredFields["track_number"] || !inferred.InferredFields["disc_number"] || !inferred.Inferred {
		t.Fatalf("missing positions were not marked as inferred: %+v", inferred)
	}

	explicit := applyFilenameFallback(filepath.Join("Album", "track.flac"), audioObservation{
		Title:          "Track",
		Artist:         "Artist",
		Album:          "Album",
		TrackNumber:    1,
		DiscNumber:     1,
		InferredFields: map[string]bool{},
	})
	if explicit.InferredFields["track_number"] || explicit.InferredFields["disc_number"] || explicit.Inferred {
		t.Fatalf("explicit position 1 was downgraded to fallback evidence: %+v", explicit)
	}

	invalid := applyFilenameFallback(filepath.Join("Album", "track.flac"), audioObservation{
		TrackNumber:    -1,
		DiscNumber:     -2,
		InferredFields: map[string]bool{},
	})
	if invalid.TrackNumber != 1 || invalid.DiscNumber != 1 || !invalid.InferredFields["track_number"] || !invalid.InferredFields["disc_number"] {
		t.Fatalf("invalid negative positions were retained as tag facts: %+v", invalid)
	}
}

func TestParseWAVReaderSkipsLargePayloadAndParsesFacts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.wav")
	const dataSize = 17 << 20
	header := make([]byte, 44)
	copy(header[:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], uint32(len(header)-8+dataSize))
	copy(header[8:12], "WAVE")
	copy(header[12:16], "fmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16)
	binary.LittleEndian.PutUint16(header[20:22], 1)
	binary.LittleEndian.PutUint16(header[22:24], 2)
	binary.LittleEndian.PutUint32(header[24:28], 48000)
	binary.LittleEndian.PutUint32(header[28:32], 192000)
	binary.LittleEndian.PutUint16(header[32:34], 4)
	binary.LittleEndian.PutUint16(header[34:36], 16)
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], dataSize)
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create WAV fixture: %v", err)
	}
	if _, err := file.Write(header); err != nil {
		_ = file.Close()
		t.Fatalf("write WAV header: %v", err)
	}
	if err := file.Truncate(int64(len(header) + dataSize)); err != nil {
		_ = file.Close()
		t.Fatalf("truncate WAV fixture: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close WAV fixture: %v", err)
	}

	observation, err := parseWAV(path)
	if err != nil {
		t.Fatalf("parse WAV fixture: %v", err)
	}
	wantDuration := float64(dataSize) / 192000
	if observation.Codec != "pcm" || observation.Channels != 2 || observation.SampleRate != 48000 || observation.BitDepth != 16 || observation.Bitrate != 1536 || math.Abs(observation.DurationSeconds-wantDuration) > 0.0001 {
		t.Fatalf("unexpected WAV facts: %+v", observation)
	}
}

func TestParseWAVReaderRejectsContainerWithoutAudioChunks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.wav")
	fixture := make([]byte, 12)
	copy(fixture[:4], "RIFF")
	binary.LittleEndian.PutUint32(fixture[4:8], 4)
	copy(fixture[8:12], "WAVE")
	if err := os.WriteFile(path, fixture, 0o644); err != nil {
		t.Fatalf("write empty WAV fixture: %v", err)
	}
	if _, err := parseWAV(path); err == nil {
		t.Fatal("WAV container without format and data chunks was accepted")
	}
}

func TestParseOggAndOpusIdentificationFacts(t *testing.T) {
	temporaryDirectory := t.TempDir()
	vorbisPath := filepath.Join(temporaryDirectory, "track.ogg")
	opusPath := filepath.Join(temporaryDirectory, "track.opus")
	vorbis := make([]byte, 30)
	copy(vorbis, []byte("\x01vorbis"))
	vorbis[11] = 2
	binary.LittleEndian.PutUint32(vorbis[12:16], 44100)
	binary.LittleEndian.PutUint32(vorbis[20:24], 192000)
	vorbis[29] = 1
	opus := make([]byte, 19)
	copy(opus, []byte("OpusHead"))
	opus[8] = 1
	opus[9] = 2
	if err := os.WriteFile(vorbisPath, repairOggPage(vorbis), 0o644); err != nil {
		t.Fatalf("write Vorbis fixture: %v", err)
	}
	if err := os.WriteFile(opusPath, repairOggPage(opus), 0o644); err != nil {
		t.Fatalf("write Opus fixture: %v", err)
	}
	vorbisObservation, err := parseOgg(vorbisPath)
	if err != nil {
		t.Fatalf("parse Vorbis fixture: %v", err)
	}
	opusObservation, err := parseOpus(opusPath)
	if err != nil {
		t.Fatalf("parse Opus fixture: %v", err)
	}
	if vorbisObservation.Codec != "vorbis" || vorbisObservation.Channels != 2 || vorbisObservation.SampleRate != 44100 || vorbisObservation.Bitrate != 192 {
		t.Fatalf("unexpected Vorbis facts: %+v", vorbisObservation)
	}
	if opusObservation.Codec != "opus" || opusObservation.Channels != 2 || opusObservation.SampleRate != 48000 {
		t.Fatalf("unexpected Opus facts: %+v", opusObservation)
	}
}

func TestParseCueDocumentSupportsMultiFileMetadataAndIndexRanges(t *testing.T) {
	temporaryDirectory := t.TempDir()
	for _, name := range []string{"disc one.flac", "disc two.flac"} {
		if err := os.WriteFile(filepath.Join(temporaryDirectory, name), repairFLACFixture(), 0o644); err != nil {
			t.Fatalf("write referenced fixture: %v", err)
		}
	}
	path := filepath.Join(temporaryDirectory, "album.cue")
	content := strings.Join([]string{
		"TITLE \"测试专辑\"",
		"PERFORMER \"测试艺术家\"",
		"REM GENRE \"摇滚\"",
		"REM DATE 2024",
		"CATALOG 0123456789012",
		"FILE \"disc one.flac\" FLAC",
		"  TRACK 01 AUDIO",
		"    TITLE \"第一首\"",
		"    ISRC CN-A01-24-00001",
		"    INDEX 00 00:00:00",
		"    INDEX 01 00:02:00",
		"  TRACK 02 AUDIO",
		"    TITLE \"第二首\"",
		"    PERFORMER \"客座艺术家\"",
		"    INDEX 01 03:00:00",
		"FILE \"disc two.flac\" FLAC",
		"  TRACK 01 AUDIO",
		"    TITLE \"第三首\"",
		"    INDEX 01 00:00:00",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write CUE fixture: %v", err)
	}

	document, err := parseCueDocument(path)
	if err != nil {
		t.Fatalf("parse multi-file CUE: %v", err)
	}
	if document.Title != "测试专辑" || document.Artist != "测试艺术家" || document.Genre != "摇滚" || document.Date != "2024" || document.Catalog != "0123456789012" {
		t.Fatalf("unexpected sheet metadata: %+v", document)
	}
	if document.Encoding != "utf-8" || len(document.Files) != 2 || len(document.Tracks) != 3 {
		t.Fatalf("unexpected CUE shape: %+v", document)
	}
	first := document.Tracks[0]
	if first.ReferencedFile != "disc one.flac" || first.ReferenceStatus != "present" || first.IndexFrames != 150 || first.Indexes[0] != 0 || first.ISRC == "" || first.SheetArtist != "测试艺术家" || first.Artist != "测试艺术家" {
		t.Fatalf("unexpected first CUE track: %+v", first)
	}
	if first.PerformerPresent || !document.Tracks[1].PerformerPresent || document.Tracks[1].Artist != "客座艺术家" {
		t.Fatalf("sheet/track performer presence was not preserved: %+v", document.Tracks)
	}
	if first.EndFrames != document.Tracks[1].IndexFrames || first.DurationFrames != 13350 {
		t.Fatalf("adjacent INDEX range was not derived: first=%+v second=%+v", first, document.Tracks[1])
	}
	if !document.Tracks[1].EndPresent || document.Tracks[1].DurationSeconds != 60 {
		t.Fatalf("last-track duration was not derived from the parent audio: %+v", document.Tracks[1])
	}
	if document.Tracks[2].ReferencedFile != "disc two.flac" || document.Tracks[2].FileType != "FLAC" {
		t.Fatalf("second FILE was lost: %+v", document.Tracks[2])
	}
	legacyTracks, legacyRef, err := parseCue(path)
	if err != nil || len(legacyTracks) != 3 || legacyRef != "disc one.flac" {
		t.Fatalf("legacy parseCue compatibility failed: tracks=%d ref=%q err=%v", len(legacyTracks), legacyRef, err)
	}
}

func TestParseCueDocumentDecodesSupportedEncodingsAndReportsUnsafeReference(t *testing.T) {
	temporaryDirectory := t.TempDir()
	if err := os.WriteFile(filepath.Join(temporaryDirectory, "track.flac"), repairFLACFixture(), 0o644); err != nil {
		t.Fatalf("write referenced fixture: %v", err)
	}
	base := "TITLE \"专辑\"\nFILE \"track.flac\" FLAC\nTRACK 01 AUDIO\nTITLE \"曲目\"\nINDEX 01 00:00:00\n"
	encodings := []struct {
		name string
		data []byte
		want string
	}{
		{name: "utf8 bom", data: append([]byte{0xef, 0xbb, 0xbf}, []byte(base)...), want: "utf-8-bom"},
		{name: "utf16le", data: repairUTF16(base, true), want: "utf-16"},
		{name: "utf16be", data: repairUTF16(base, false), want: "utf-16"},
	}
	for _, encodingCase := range encodings {
		t.Run(encodingCase.name, func(t *testing.T) {
			path := filepath.Join(temporaryDirectory, encodingCase.name+".cue")
			if err := os.WriteFile(path, encodingCase.data, 0o644); err != nil {
				t.Fatalf("write encoded CUE: %v", err)
			}
			document, err := parseCueDocument(path)
			if err != nil {
				t.Fatalf("parse %s CUE: %v", encodingCase.name, err)
			}
			if document.Encoding != encodingCase.want || document.Title != "专辑" || document.Tracks[0].Title != "曲目" {
				t.Fatalf("unexpected %s decode: %+v", encodingCase.name, document)
			}
		})
	}

	unsafePath := filepath.Join(temporaryDirectory, "unsafe.cue")
	unsafe := "FILE \"../outside.flac\" FLAC\nTRACK 01 AUDIO\nINDEX 01 00:00:00\n"
	if err := os.WriteFile(unsafePath, []byte(unsafe), 0o644); err != nil {
		t.Fatalf("write unsafe CUE: %v", err)
	}
	document, err := parseCueDocument(unsafePath)
	if err != nil {
		t.Fatalf("unsafe FILE should be a diagnostic, not syntax failure: %v", err)
	}
	if len(document.Diagnostics) == 0 || document.Tracks[0].ReferenceStatus != "unsafe" {
		t.Fatalf("unsafe FILE diagnostic was lost: %+v", document)
	}
}

func TestParseCueDecoderSupportsGBKShiftJISAndBig5(t *testing.T) {
	temporaryDirectory := t.TempDir()
	if err := os.WriteFile(filepath.Join(temporaryDirectory, "track.flac"), repairFLACFixture(), 0o644); err != nil {
		t.Fatalf("write referenced FLAC: %v", err)
	}
	testCases := []struct {
		name         string
		encoded      []byte
		want         string
		wantEncoding string
	}{
		{name: "gbk", encoded: []byte{0xD7, 0xA8, 0xBC, 0xAD}, want: "专辑", wantEncoding: "gbk"},
		{name: "shift jis", encoded: []byte{0x83, 0x41, 0x83, 0x8B, 0x83, 0x6F, 0x83, 0x80}, want: "アルバム", wantEncoding: "shift-jis"},
		{name: "big5", encoded: []byte{0xB1, 0x4D, 0xBF, 0xE8}, want: "專輯", wantEncoding: "big5"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			prefix := []byte("TITLE \"")
			data := append(prefix, testCase.encoded...)
			data = append(data, []byte("\"\nFILE \"track.flac\" FLAC\nTRACK 01 AUDIO\nINDEX 01 00:00:00\n")...)
			path := filepath.Join(temporaryDirectory, testCase.name+".cue")
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatalf("write %s CUE: %v", testCase.name, err)
			}
			document, err := parseCueDocument(path)
			if err != nil {
				t.Fatalf("parse %s CUE: %v", testCase.name, err)
			}
			if document.Title != testCase.want || document.Encoding != testCase.wantEncoding {
				t.Fatalf("unexpected %s decode: %+v", testCase.name, document)
			}
		})
	}
}

func TestParseCueDocumentBoundsInputAndSymlinkContainment(t *testing.T) {
	temporaryDirectory := t.TempDir()
	oversizedPath := filepath.Join(temporaryDirectory, "oversized.cue")
	oversized, err := os.Create(oversizedPath)
	if err != nil {
		t.Fatalf("create oversized CUE: %v", err)
	}
	if err := oversized.Truncate(maxCueBytes + 1); err != nil {
		_ = oversized.Close()
		t.Fatalf("truncate oversized CUE: %v", err)
	}
	if err := oversized.Close(); err != nil {
		t.Fatalf("close oversized CUE: %v", err)
	}
	if _, err := parseCueDocument(oversizedPath); err == nil {
		t.Fatal("oversized CUE was accepted")
	}
	if _, _, err := decodeCueText([]byte{0xff, 0xfe, 0x00, 0xd8}); err == nil {
		t.Fatal("unpaired UTF-16 surrogate was accepted")
	}
	if _, _, err := decodeCueText([]byte("TITLE \"bad\x00value\"")); err == nil {
		t.Fatal("CUE control character was accepted")
	}

	cueDirectory := filepath.Join(temporaryDirectory, "cue")
	outsideDirectory := filepath.Join(temporaryDirectory, "outside")
	if err := os.MkdirAll(filepath.Join(cueDirectory, "sub"), 0o755); err != nil {
		t.Fatalf("create CUE directory: %v", err)
	}
	if err := os.MkdirAll(outsideDirectory, 0o755); err != nil {
		t.Fatalf("create outside directory: %v", err)
	}
	insidePath := filepath.Join(cueDirectory, "sub", "track.flac")
	outsidePath := filepath.Join(outsideDirectory, "outside.flac")
	for _, path := range []string{insidePath, outsidePath} {
		if err := os.WriteFile(path, repairFLACFixture(), 0o644); err != nil {
			t.Fatalf("write referenced FLAC: %v", err)
		}
	}
	if err := os.Symlink(outsidePath, filepath.Join(cueDirectory, "escaped.flac")); err != nil {
		t.Fatalf("create escaping symlink: %v", err)
	}
	path := filepath.Join(cueDirectory, "paths.cue")
	content := "FILE \"sub\\track.flac\" FLAC\nTRACK 01 AUDIO\nINDEX 01 00:00:00\nFILE \"escaped.flac\" FLAC\nTRACK 02 AUDIO\nINDEX 01 00:00:00\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write path CUE: %v", err)
	}
	document, err := parseCueDocument(path)
	if err != nil {
		t.Fatalf("parse path CUE: %v", err)
	}
	if document.Files[0].Path != "sub/track.flac" || document.Files[0].Status != "present" {
		t.Fatalf("Windows separator was not safely normalized: %+v", document.Files[0])
	}
	if document.Files[1].Status != "unsafe" || len(document.Diagnostics) == 0 {
		t.Fatalf("escaping symlink was not diagnosed: %+v", document)
	}
}

func TestParseCueDocumentAllowsParentReferenceWithinExplicitRoot(t *testing.T) {
	libraryRoot := t.TempDir()
	cueDirectory := filepath.Join(libraryRoot, "sheets")
	if err := os.MkdirAll(cueDirectory, 0o755); err != nil {
		t.Fatalf("create sheet directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(libraryRoot, "track.flac"), repairFLACFixture(), 0o644); err != nil {
		t.Fatalf("write parent audio: %v", err)
	}
	path := filepath.Join(cueDirectory, "album.cue")
	content := "FILE \"../track.flac\" FLAC\nTRACK 01 AUDIO\nINDEX 01 00:00:00\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write parent-reference CUE: %v", err)
	}
	if document, err := parseCueDocument(path, libraryRoot); err != nil || document.Files[0].Status != "present" {
		t.Fatalf("contained parent reference was rejected: doc=%+v err=%v", document, err)
	}
	if document, err := parseCueDocument(path); err != nil || document.Files[0].Status != "unsafe" {
		t.Fatalf("default CUE-directory boundary was not conservative: doc=%+v err=%v", document, err)
	}
}

func TestParseCueDocumentRejectsNonRegularReference(t *testing.T) {
	temporaryDirectory := t.TempDir()
	pipePath := filepath.Join(temporaryDirectory, "audio.flac")
	if err := syscall.Mkfifo(pipePath, 0o600); err != nil {
		t.Fatalf("create FIFO reference: %v", err)
	}
	path := filepath.Join(temporaryDirectory, "album.cue")
	content := "FILE \"audio.flac\" FLAC\nTRACK 01 AUDIO\nINDEX 01 00:00:00\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write FIFO-reference CUE: %v", err)
	}

	document, err := parseCueDocument(path)
	if err != nil {
		t.Fatalf("non-regular reference should be diagnosed, not fail syntax parsing: %v", err)
	}
	if len(document.Files) != 1 || document.Files[0].Status != "unsafe" || len(document.Tracks) != 1 || document.Tracks[0].ReferenceStatus != "unsafe" {
		t.Fatalf("non-regular FILE reference was accepted: %+v", document)
	}
	if len(document.Diagnostics) == 0 || !strings.Contains(document.Diagnostics[0], "not a regular file") {
		t.Fatalf("non-regular FILE diagnostic was lost: %+v", document.Diagnostics)
	}
}

func TestParseCueDocumentClassifiesMalformedIndexSequences(t *testing.T) {
	temporaryDirectory := t.TempDir()
	audioPath := filepath.Join(temporaryDirectory, "track.flac")
	if err := os.WriteFile(audioPath, repairFLACFixture(), 0o644); err != nil {
		t.Fatalf("write referenced FLAC: %v", err)
	}
	duplicatePath := filepath.Join(temporaryDirectory, "duplicate.cue")
	duplicate := "FILE \"track.flac\" FLAC\nTRACK 01 AUDIO\nINDEX 01 00:00:00\nINDEX 01 00:01:00\n"
	if err := os.WriteFile(duplicatePath, []byte(duplicate), 0o644); err != nil {
		t.Fatalf("write duplicate-index CUE: %v", err)
	}
	if _, err := parseCueDocument(duplicatePath); err == nil {
		t.Fatal("duplicate INDEX number was accepted")
	}

	nonMonotonicPath := filepath.Join(temporaryDirectory, "non-monotonic.cue")
	nonMonotonic := "FILE \"track.flac\" FLAC\nTRACK 01 AUDIO\nINDEX 01 02:00:00\nTRACK 02 AUDIO\nINDEX 01 01:00:00\n"
	if err := os.WriteFile(nonMonotonicPath, []byte(nonMonotonic), 0o644); err != nil {
		t.Fatalf("write non-monotonic CUE: %v", err)
	}
	document, err := parseCueDocument(nonMonotonicPath)
	if err != nil {
		t.Fatalf("non-monotonic INDEX should produce a diagnostic: %v", err)
	}
	if len(document.Diagnostics) == 0 || document.Tracks[0].EndPresent {
		t.Fatalf("non-monotonic INDEX was not diagnosed: %+v", document)
	}

	missingPath := filepath.Join(temporaryDirectory, "missing-index.cue")
	missing := "FILE \"track.flac\" FLAC\nTRACK 01 AUDIO\nTITLE \"No index\"\n"
	if err := os.WriteFile(missingPath, []byte(missing), 0o644); err != nil {
		t.Fatalf("write missing-index CUE: %v", err)
	}
	if document, err := parseCueDocument(missingPath); err != nil || len(document.Diagnostics) == 0 {
		t.Fatalf("document parser did not retain missing-index diagnostic: doc=%+v err=%v", document, err)
	}
	if _, _, err := parseCue(missingPath); err == nil {
		t.Fatal("legacy parseCue accepted a track without INDEX 01")
	}
	if _, err := parseCueTimecode("999999999:59:74"); err == nil {
		t.Fatal("overflowing INDEX timecode was accepted")
	}
}

func repairM4AAtom(atomType string, payload []byte) []byte {
	result := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(result[:4], uint32(len(result)))
	copy(result[4:8], []byte(atomType))
	copy(result[8:], payload)
	return result
}

func repairOggPage(packet []byte) []byte {
	page := make([]byte, 28+len(packet))
	copy(page[:4], "OggS")
	page[26] = 1
	page[27] = byte(len(packet))
	copy(page[28:], packet)
	return page
}

func repairFLACFixture() []byte {
	streamInfo := make([]byte, 34)
	const sampleRate = 44100
	const channels = 2
	const bitDepth = 16
	const durationSeconds = 240
	totalSamples := uint64(sampleRate * durationSeconds)
	packed := uint64(sampleRate)<<44 | uint64(channels-1)<<41 | uint64(bitDepth-1)<<36 | totalSamples
	binary.BigEndian.PutUint64(streamInfo[10:18], packed)
	comments := make([]byte, 8)
	result := append([]byte{'f', 'L', 'a', 'C', 0, 0, 0, byte(len(streamInfo))}, streamInfo...)
	result = append(result, []byte{0x84, 0, 0, byte(len(comments))}...)
	return append(result, comments...)
}

func repairFLACComments(comments ...string) []byte {
	payload := &bytes.Buffer{}
	_ = binary.Write(payload, binary.LittleEndian, uint32(0))
	_ = binary.Write(payload, binary.LittleEndian, uint32(len(comments)))
	for _, comment := range comments {
		_ = binary.Write(payload, binary.LittleEndian, uint32(len(comment)))
		_, _ = payload.WriteString(comment)
	}
	length := payload.Len()
	return append([]byte{'f', 'L', 'a', 'C', 0x84, byte(length >> 16), byte(length >> 8), byte(length)}, payload.Bytes()...)
}

func repairMP3Frames(frames map[string]string) []byte {
	payload := &bytes.Buffer{}
	for _, frameID := range []string{"TIT2", "TPE1", "TPE2", "TALB", "TRCK", "TPOS", "TCON", "TDRC", "TYER", "TSRC"} {
		value, ok := frames[frameID]
		if !ok {
			continue
		}
		_, _ = payload.WriteString(frameID)
		_ = binary.Write(payload, binary.BigEndian, uint32(len(value)+1))
		_, _ = payload.Write([]byte{0, 0, 0})
		_, _ = payload.WriteString(value)
	}
	size := payload.Len()
	header := []byte{'I', 'D', '3', 3, 0, 0, byte(size >> 21), byte(size >> 14), byte(size >> 7), byte(size)}
	return append(header, payload.Bytes()...)
}

func repairMP3BinaryFrame(frameID string, frame []byte) []byte {
	payload := &bytes.Buffer{}
	_, _ = payload.WriteString(frameID)
	_ = binary.Write(payload, binary.BigEndian, uint32(len(frame)))
	_, _ = payload.Write([]byte{0, 0})
	_, _ = payload.Write(frame)
	size := payload.Len()
	header := []byte{'I', 'D', '3', 3, 0, 0, byte(size >> 21), byte(size >> 14), byte(size >> 7), byte(size)}
	return append(header, payload.Bytes()...)
}

func repairM4AAtom64(atomType string, payload []byte) []byte {
	result := make([]byte, 16+len(payload))
	binary.BigEndian.PutUint32(result[:4], 1)
	copy(result[4:8], []byte(atomType))
	binary.BigEndian.PutUint64(result[8:16], uint64(len(result)))
	copy(result[16:], payload)
	return result
}

func repairM4AAtomToEOF(atomType string, payload []byte) []byte {
	result := make([]byte, 8+len(payload))
	copy(result[4:8], []byte(atomType))
	copy(result[8:], payload)
	return result
}

func repairM4AData(typeCode uint32, value []byte) []byte {
	payload := make([]byte, 8+len(value))
	binary.BigEndian.PutUint32(payload[:4], typeCode)
	copy(payload[8:], value)
	return repairM4AAtom("data", payload)
}

func repairM4AFreeform(name, value string) []byte {
	mean := repairM4AAtom("mean", append([]byte{0, 0, 0, 0}, []byte("com.apple.iTunes")...))
	nameAtom := repairM4AAtom("name", append([]byte{0, 0, 0, 0}, []byte(name)...))
	return repairM4AAtom("----", append(mean, append(nameAtom, repairM4AData(1, []byte(value))...)...))
}

func repairM4AMoov(codec string, channels, bitDepth, sampleRate, duration uint32, title, artist, album, albumArtist string) []byte {
	mvhdPayload := make([]byte, 20)
	binary.BigEndian.PutUint32(mvhdPayload[12:16], 44100)
	binary.BigEndian.PutUint32(mvhdPayload[16:20], duration)
	mvhd := repairM4AAtom("mvhd", mvhdPayload)
	entry := make([]byte, 36)
	binary.BigEndian.PutUint32(entry[:4], uint32(len(entry)))
	copy(entry[4:8], []byte(codec))
	binary.BigEndian.PutUint16(entry[24:26], uint16(channels))
	binary.BigEndian.PutUint16(entry[26:28], uint16(bitDepth))
	binary.BigEndian.PutUint32(entry[32:36], sampleRate<<16)
	if codec == "alac" {
		config := make([]byte, 28)
		config[9] = byte(bitDepth)
		config[13] = byte(channels)
		binary.BigEndian.PutUint32(config[24:28], sampleRate)
		entry = append(entry, repairM4AAtom("alac", config)...)
	}
	bitrate := make([]byte, 12)
	binary.BigEndian.PutUint32(bitrate[8:12], 256000)
	entry = append(entry, repairM4AAtom("btrt", bitrate)...)
	binary.BigEndian.PutUint32(entry[:4], uint32(len(entry)))
	stsdPayload := make([]byte, 8)
	binary.BigEndian.PutUint32(stsdPayload[4:8], 1)
	stsdPayload = append(stsdPayload, entry...)
	stsd := repairM4AAtom("stsd", stsdPayload)
	stbl := repairM4AAtom("stbl", stsd)
	minf := repairM4AAtom("minf", stbl)
	mdia := repairM4AAtom("mdia", append(repairM4AAtom("mdhd", mvhdPayload), minf...))
	trak := repairM4AAtom("trak", mdia)
	items := make([]byte, 0)
	for _, item := range []struct {
		name  string
		value string
	}{
		{name: string([]byte{0xa9, 'n', 'a', 'm'}), value: title},
		{name: string([]byte{0xa9, 'A', 'R', 'T'}), value: artist},
		{name: string([]byte{0xa9, 'a', 'l', 'b'}), value: album},
		{name: "aART", value: albumArtist},
	} {
		if item.value != "" {
			items = append(items, repairM4AAtom(item.name, repairM4AData(1, []byte(item.value)))...)
		}
	}
	trackValue := []byte{0, 0, 0, 2, 0, 10, 0, 0}
	discValue := []byte{0, 0, 0, 1, 0, 2, 0, 0}
	items = append(items, repairM4AAtom("trkn", repairM4AData(0, trackValue))...)
	items = append(items, repairM4AAtom("disk", repairM4AData(0, discValue))...)
	items = append(items, repairM4AAtom("covr", repairM4AData(13, []byte{0xff, 0xd8, 0xff, 0xd9}))...)
	meta := repairM4AAtom("meta", append([]byte{0, 0, 0, 0}, repairM4AAtom("ilst", items)...))
	udta := repairM4AAtom("udta", meta)
	return repairM4AAtom("moov", append(mvhd, append(trak, udta...)...))
}

func repairUTF16(value string, littleEndian bool) []byte {
	units := utf16.Encode([]rune(value))
	result := make([]byte, 2+len(units)*2)
	if littleEndian {
		result[0], result[1] = 0xff, 0xfe
	} else {
		result[0], result[1] = 0xfe, 0xff
	}
	for index, unit := range units {
		if littleEndian {
			binary.LittleEndian.PutUint16(result[2+index*2:], unit)
		} else {
			binary.BigEndian.PutUint16(result[2+index*2:], unit)
		}
	}
	return result
}
