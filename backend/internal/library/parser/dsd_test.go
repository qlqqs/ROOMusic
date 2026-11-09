package parser

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/playback/dsdio"
)

func TestDSFMetadataPointerPriorityAndRecovery(t *testing.T) {
	payload := bytes.Repeat([]byte{0x96}, 8192)
	fallback := dsdTestID3("块尾标题", "块尾艺人")
	pointed := dsdTestID3("指针标题", "指针艺人")
	dataEnd := uint64(92 + len(payload))
	// 有效指针与块尾同时存在不同标签，不能靠总是读块尾碰巧通过。
	twoTags := append(bytes.Clone(fallback), 0, 0, 0)
	pointedOffset := dataEnd + uint64(len(twoTags))
	twoTags = append(twoTags, pointed...)
	for _, tc := range []struct {
		name          string
		pointer       uint64
		tail          []byte
		title, artist string
	}{
		{"有效指针优先", pointedOffset, twoTags, "指针标题", "指针艺人"},
		{"正常块尾指针", dataEnd, fallback, "块尾标题", "块尾艺人"},
		{"零指针找回", 0, fallback, "块尾标题", "块尾艺人"},
		{"越界指针找回", dataEnd + uint64(len(fallback)) + 100, fallback, "块尾标题", "块尾艺人"},
		{"错位指针找回", dataEnd + 1, fallback, "块尾标题", "块尾艺人"},
		{"音频内指针找回", 92, fallback, "块尾标题", "块尾艺人"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := dsdTestDSF(payload, tc.tail, tc.pointer)
			obs, err := parseDSF(writeFingerprintFixture(t, "track.dsf", data))
			if err != nil {
				t.Fatal(err)
			}
			dsdTestObservation(t, obs, "dsf_id3", "dsf", 32768, tc.title, tc.artist)
		})
	}
}

func TestDFFID3TagsTakePriorityOverDIIN(t *testing.T) {
	payload := []byte{0x96, 0x69, 0x80, 1, 0x55, 0xaa, 0x81, 0x18}
	diin := dsdTestDIIN("原始标题", "原始艺人")
	id3 := dsdTestDFFChunk("ID3 ", dsdTestID3("ID3标题", "ID3艺人"))
	titleOnly := dsdTestDFFChunk("ID3 ", dsdTestID3("ID3标题", ""))
	artistOnly := dsdTestDFFChunk("ID3 ", dsdTestID3("", "ID3艺人"))
	for _, tc := range []struct {
		name          string
		chunks        [][]byte
		title, artist string
	}{
		{"仅DIIN", [][]byte{diin}, "原始标题", "原始艺人"},
		{"ID3在后", [][]byte{diin, id3}, "ID3标题", "ID3艺人"},
		{"ID3在前", [][]byte{id3, diin}, "ID3标题", "ID3艺人"},
		{"DIIN补艺人", [][]byte{titleOnly, diin}, "ID3标题", "原始艺人"},
		{"DIIN补标题", [][]byte{diin, artistOnly}, "原始标题", "ID3艺人"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := dsdTestDFF(payload, "DSD ", "DSD ", tc.chunks...)
			obs, err := parseDFF(writeFingerprintFixture(t, "track.dff", data))
			if err != nil {
				t.Fatal(err)
			}
			dsdTestObservation(t, obs, "dff_diin", "dff", 32, tc.title, tc.artist)
			for _, evidence := range obs.ArtistTagEvidence {
				if evidence.Role == "track_artist" && evidence.RawValue != tc.artist {
					t.Fatalf("被覆盖的艺人仍留在证据中：%+v", obs.ArtistTagEvidence)
				}
			}
		})
	}
}

func TestDSDFingerprintIgnoresTagsAndDetectsAudioChanges(t *testing.T) {
	for _, tc := range []struct {
		name, kind  string
		payload     []byte
		build       func([]byte, string) []byte
		fingerprint func(context.Context, io.ReaderAt, int64) (string, error)
	}{
		{
			name: "DSF", kind: "dsf-data-v1", payload: bytes.Repeat([]byte{0x96, 0x69}, 4096),
			build: func(payload []byte, label string) []byte {
				return dsdTestDSF(payload, dsdTestID3(label, label), uint64(92+len(payload)))
			},
			fingerprint: dsfFingerprint,
		},
		{
			name: "DFF", kind: "dff-dsd-v1", payload: []byte{0x96, 0x69, 0x80, 1, 0x55, 0xaa},
			build: func(payload []byte, label string) []byte {
				return dsdTestDFF(payload, "DSD ", "DSD ", dsdTestDIIN(label, label), dsdTestDFFChunk("ID3 ", dsdTestID3(label, label)))
			},
			fingerprint: dffFingerprint,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changedAudio := bytes.Clone(tc.payload)
			changedAudio[len(changedAudio)/2] ^= 1
			variants := []struct {
				name    string
				payload []byte
				label   string
			}{
				{"原始", tc.payload, "原始标签"},
				{"仅改标签及其长度", tc.payload, "不同且更长的标题与艺人标签"},
				{"仅改一个音频字节", changedAudio, "原始标签"},
			}
			fingerprints := make([]string, len(variants))
			for i, variant := range variants {
				data := tc.build(variant.payload, variant.label)
				got, err := tc.fingerprint(context.Background(), bytes.NewReader(data), int64(len(data)))
				if err != nil {
					t.Fatalf("%s：%v", variant.name, err)
				}
				want := tc.kind + ":" + sha256Hex(variant.payload)
				if got != want {
					t.Fatalf("%s：指纹 %q，期望仅哈希音频负载 %q", variant.name, got, want)
				}
				fingerprints[i] = got
			}
			if fingerprints[0] != fingerprints[1] {
				t.Fatal("标签改变了内容指纹")
			}
			if fingerprints[0] == fingerprints[2] {
				t.Fatal("一个音频字节的变化未改变内容指纹")
			}
		})
	}
}

func TestDSTDFFPreservesUnsupportedEncodingErrorChain(t *testing.T) {
	for _, tc := range []struct{ name, compression, audioID string }{
		{"CMPR声明DST", "DST ", "DSD "},
		{"DST音频块", "DSD ", "DST "},
		{"二者均为DST", "DST ", "DST "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := dsdTestDFF([]byte{1, 2, 3, 4}, tc.compression, tc.audioID)
			// 纯包阶段尚未放开扩展名分派，直接验证 parser 的容器入口与错误映射。
			_, err := parseDFF(writeFingerprintFixture(t, "compressed.dff", data))
			if !errors.Is(err, ErrUnsupportedEncoding) {
				t.Fatalf("未保留 parser 哨兵：%v", err)
			}
			var unsupported *dsdio.UnsupportedEncodingError
			if !errors.As(err, &unsupported) {
				t.Fatalf("未保留中性容器错误：%v", err)
			}
			if unsupported.Encoding != "DST" {
				t.Fatalf("编码为 %q，期望 DST", unsupported.Encoding)
			}
		})
	}
}

func dsdTestObservation(t *testing.T, obs AudioObservation, kind, codec string, samples uint64, title, artist string) {
	t.Helper()
	if obs.SourceKind != kind || obs.Codec != codec || obs.SampleRate != 2822400 || obs.Channels != 2 || obs.BitDepth != 1 || obs.Bitrate != 5644 || obs.DurationSeconds != float64(samples)/2822400 {
		t.Fatalf("音频观察值错误：%+v", obs)
	}
	if obs.Title != title || obs.Artist != artist || obs.InferredFields["title"] || obs.InferredFields["artist"] {
		t.Fatalf("标签错误：标题 %q，艺人 %q，推断字段 %v", obs.Title, obs.Artist, obs.InferredFields)
	}
}

func dsdTestID3(title, artist string) []byte {
	var frames [][]byte
	if title != "" {
		frames = append(frames, id3Frame(4, "TIT2", [2]byte{}, append([]byte{3}, title...)))
	}
	if artist != "" {
		frames = append(frames, id3Frame(4, "TPE1", [2]byte{}, append([]byte{3}, artist...)))
	}
	return id3Tag(4, 0, frames...)
}

// dsdTestDSF 使用完整的双声道 4096 字节块，标签不包含在 data 长度内。
func dsdTestDSF(payload, tags []byte, metadataOffset uint64) []byte {
	data := make([]byte, 92)
	le := binary.LittleEndian
	copy(data, "DSD ")
	le.PutUint64(data[4:12], 28)
	le.PutUint64(data[12:20], uint64(92+len(payload)+len(tags)))
	le.PutUint64(data[20:28], metadataOffset)
	copy(data[28:32], "fmt ")
	le.PutUint64(data[32:40], 52)
	le.PutUint32(data[40:44], 1)
	le.PutUint32(data[48:52], 2)
	le.PutUint32(data[52:56], 2)
	le.PutUint32(data[56:60], 2822400)
	le.PutUint32(data[60:64], 1)
	le.PutUint64(data[64:72], uint64(len(payload)/2*8))
	le.PutUint32(data[72:76], 4096)
	copy(data[80:84], "data")
	le.PutUint64(data[84:92], uint64(12+len(payload)))
	data = append(data, payload...)
	return append(data, tags...)
}

func dsdTestDIIN(title, artist string) []byte {
	text := func(value string) []byte {
		return append(binary.BigEndian.AppendUint32(nil, uint32(len(value))), value...)
	}
	body := dsdTestDFFChunk("DITI", text(title))
	body = append(body, dsdTestDFFChunk("DIAR", text(artist))...)
	return dsdTestDFFChunk("DIIN", body)
}

func dsdTestDFF(payload []byte, compression, audioID string, tags ...[]byte) []byte {
	props := []byte("SND ")
	props = append(props, dsdTestDFFChunk("FS  ", binary.BigEndian.AppendUint32(nil, 2822400))...)
	props = append(props, dsdTestDFFChunk("CHNL", append(binary.BigEndian.AppendUint16(nil, 2), "SLFTSRGT"...))...)
	props = append(props, dsdTestDFFChunk("CMPR", append([]byte(compression), 0))...)
	body := []byte("DSD ")
	body = append(body, dsdTestDFFChunk("FVER", []byte{1, 5, 0, 0})...)
	body = append(body, dsdTestDFFChunk("PROP", props)...)
	body = append(body, dsdTestDFFChunk(audioID, payload)...)
	for _, tag := range tags {
		body = append(body, tag...)
	}
	return dsdTestDFFChunk("FRM8", body)
}

func dsdTestDFFChunk(id string, payload []byte) []byte {
	data := binary.BigEndian.AppendUint64([]byte(id), uint64(len(payload)))
	data = append(data, payload...)
	if len(payload)%2 != 0 {
		data = append(data, 0)
	}
	return data
}
