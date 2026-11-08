package parser

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/debug"
	"testing"
)

// 基线由 ab46693 的独立 HEAD worktree 实际解析得到；包含全部观察字段及独立指纹入口。
const oggHeadBaseline = `{
"continued.ogg":{"Observation":{"Title":"流式兼容","Artist":"未知艺术家","Album":"001","AlbumArtist":"","TrackNumber":1,"DiscNumber":1,"SourceKind":"ogg_vorbis_comment","Inferred":true,"InferredFields":{"album":true,"artist":true,"disc_number":true,"track_number":true},"Artwork":null,"ArtworkMIME":"","DurationSeconds":0,"Codec":"vorbis","BitDepth":0,"SampleRate":44100,"Channels":2,"Bitrate":0,"Year":0,"Genres":null,"Catalog":"","ISRC":"","SourceType":"","MediaType":"","Edition":"","Label":"","Barcode":"","Credits":null,"ArtistTagEvidence":null,"RawAtoms":null,"ContentFingerprint":"ogg-audio-v1:a76e929d32e3f6e2dae480a9c4a55906164402403973ecc82eb0cc1f1c81e5f8","Lyrics":"","LyricsSource":""},"Fingerprint":"ogg-audio-v1:a76e929d32e3f6e2dae480a9c4a55906164402403973ecc82eb0cc1f1c81e5f8"},
"continued.opus":{"Observation":{"Title":"流式兼容","Artist":"未知艺术家","Album":"002","AlbumArtist":"","TrackNumber":1,"DiscNumber":1,"SourceKind":"opus_tags","Inferred":true,"InferredFields":{"album":true,"artist":true,"disc_number":true,"track_number":true},"Artwork":null,"ArtworkMIME":"","DurationSeconds":0,"Codec":"opus","BitDepth":0,"SampleRate":48000,"Channels":2,"Bitrate":0,"Year":0,"Genres":null,"Catalog":"","ISRC":"","SourceType":"","MediaType":"","Edition":"","Label":"","Barcode":"","Credits":null,"ArtistTagEvidence":null,"RawAtoms":null,"ContentFingerprint":"ogg-audio-v1:a76e929d32e3f6e2dae480a9c4a55906164402403973ecc82eb0cc1f1c81e5f8","Lyrics":"","LyricsSource":""},"Fingerprint":"ogg-audio-v1:a76e929d32e3f6e2dae480a9c4a55906164402403973ecc82eb0cc1f1c81e5f8"},
"opus-48000.opus":{"Observation":{"Title":"Opus","Artist":"Fixture Artist","Album":"Playback Fixtures","AlbumArtist":"Fixture Artist","TrackNumber":10,"DiscNumber":1,"SourceKind":"opus_tags","Inferred":true,"InferredFields":{"disc_number":true},"Artwork":null,"ArtworkMIME":"","DurationSeconds":0,"Codec":"opus","BitDepth":0,"SampleRate":48000,"Channels":2,"Bitrate":0,"Year":0,"Genres":null,"Catalog":"","ISRC":"","SourceType":"","MediaType":"","Edition":"","Label":"","Barcode":"","Credits":null,"ArtistTagEvidence":[{"role":"track_artist","tag_key":"artist","raw_value":"Fixture Artist","source_kind":"opus_tags","occurrence_index":0},{"role":"album_artist","tag_key":"ALBUMARTIST","raw_value":"Fixture Artist","source_kind":"opus_tags","occurrence_index":0}],"RawAtoms":null,"ContentFingerprint":"ogg-audio-v1:3c9e09cfc1721db26bf7a29ea23d93a5fc14084933835318d5615791fbe17207","Lyrics":"","LyricsSource":""},"Fingerprint":"ogg-audio-v1:3c9e09cfc1721db26bf7a29ea23d93a5fc14084933835318d5615791fbe17207"},
"vorbis-44100.ogg":{"Observation":{"Title":"Vorbis","Artist":"Fixture Artist","Album":"Playback Fixtures","AlbumArtist":"Fixture Artist","TrackNumber":9,"DiscNumber":1,"SourceKind":"ogg_vorbis_comment","Inferred":true,"InferredFields":{"disc_number":true},"Artwork":null,"ArtworkMIME":"","DurationSeconds":0,"Codec":"vorbis","BitDepth":0,"SampleRate":44100,"Channels":2,"Bitrate":96,"Year":0,"Genres":null,"Catalog":"","ISRC":"","SourceType":"","MediaType":"","Edition":"","Label":"","Barcode":"","Credits":null,"ArtistTagEvidence":[{"role":"track_artist","tag_key":"artist","raw_value":"Fixture Artist","source_kind":"ogg_vorbis_comment","occurrence_index":0},{"role":"album_artist","tag_key":"ALBUMARTIST","raw_value":"Fixture Artist","source_kind":"ogg_vorbis_comment","occurrence_index":0}],"RawAtoms":null,"ContentFingerprint":"ogg-audio-v1:816e4770895a67ccffb5147821560641359840ac9e0468622f8b4c4e05e619c9","Lyrics":"","LyricsSource":""},"Fingerprint":"ogg-audio-v1:816e4770895a67ccffb5147821560641359840ac9e0468622f8b4c4e05e619c9"}
}`

func TestOggOpusObservationsMatchHEADBaseline(t *testing.T) {
	var baseline map[string]struct {
		Observation AudioObservation
		Fingerprint string
	}
	if err := json.Unmarshal([]byte(oggHeadBaseline), &baseline); err != nil {
		t.Fatal(err)
	}
	packets := splitPackets(bytes.Repeat([]byte{0x31, 0x41, 0x59, 0x26}, 750), 500)
	for _, name := range []string{"vorbis-44100.ogg", "opus-48000.opus", "continued.ogg", "continued.opus"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(playbackFixtureDirectory, name)
			if name == "continued.ogg" || name == "continued.opus" {
				// 固定目录名，避免文件名兜底的专辑字段随 t.TempDir 的编号变化。
				directory := "001"
				data := testVorbisStream("流式兼容", 2, packets...)
				if name == "continued.opus" {
					directory = "002"
					data = testOpusStream("流式兼容", 2, packets...)
				}
				directory = filepath.Join(t.TempDir(), directory)
				if err := os.Mkdir(directory, 0700); err != nil {
					t.Fatal(err)
				}
				path = filepath.Join(directory, name)
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			observation, err := ParseAudioFile(path)
			if err != nil {
				t.Fatal(err)
			}
			got, want := reflect.ValueOf(observation), reflect.ValueOf(baseline[name].Observation)
			for i := 0; i < got.NumField(); i++ {
				if !reflect.DeepEqual(got.Field(i).Interface(), want.Field(i).Interface()) {
					t.Errorf("观察字段 %s：实际=%#v 基线=%#v", got.Type().Field(i).Name, got.Field(i).Interface(), want.Field(i).Interface())
				}
			}
			fingerprint, err := ContentFingerprint(context.Background(), path)
			if err != nil || fingerprint != baseline[name].Fingerprint {
				t.Fatalf("独立指纹=%q err=%v，基线=%q", fingerprint, err, baseline[name].Fingerprint)
			}
		})
	}
}

// writeMemoryOgg 只重复普通音频包，不重复注释包；生成过程只保留一页。
func writeMemoryOgg(t *testing.T, targetBytes int64) string {
	t.Helper()
	file, err := os.Create(filepath.Join(t.TempDir(), "memory.ogg"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	header := testVorbisStream("内存测试", 255)
	if _, err := file.Write(header); err != nil {
		t.Fatal(err)
	}
	audio := bytes.Repeat([]byte{0x21}, 4096)
	packets := make([][]byte, 12)
	for i := range packets {
		packets[i] = audio
	}
	page, sequence := testOggPages(7, 2, 255, packets...)
	for written := int64(len(header)); written < targetBytes; written += int64(len(page)) {
		// 每一页都完整结束音频包，页序号不参与本解析器的指纹。
		if _, err := file.Write(page); err != nil {
			t.Fatal(err)
		}
	}
	_ = sequence
	return file.Name()
}

func TestOggStreamingMemoryDoesNotGrowWithFileSize(t *testing.T) {
	small := writeMemoryOgg(t, 1<<20)
	large := writeMemoryOgg(t, 64<<20)
	for _, operation := range []string{"parse", "fingerprint"} {
		t.Run(operation, func(t *testing.T) {
			measure := func(path string) uint64 {
				runtime.GC()
				previous := debug.SetGCPercent(-1)
				defer debug.SetGCPercent(previous)
				var before, after runtime.MemStats
				runtime.ReadMemStats(&before)
				if operation == "parse" {
					observation, err := ParseAudioFile(path)
					if err != nil {
						t.Fatal(err)
					}
					if observation.ContentFingerprint == "" {
						t.Fatal("缺少音频指纹")
					}
				} else {
					fingerprint, err := ContentFingerprint(context.Background(), path)
					if err != nil || fingerprint == "" {
						t.Fatalf("fingerprint=%q err=%v", fingerprint, err)
					}
				}
				runtime.ReadMemStats(&after)
				// 此区间禁止 GC，TotalAlloc 差值同时给出本次解析峰值存活堆增量的保守上界。
				return after.TotalAlloc - before.TotalAlloc
			}
			smallBytes, largeBytes := measure(small), measure(large)
			smallInfo, err := os.Stat(small)
			if err != nil {
				t.Fatal(err)
			}
			largeInfo, err := os.Stat(large)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("file_bytes small=%d large=%d; peak_heap_upper_bound_bytes small=%d large=%d", smallInfo.Size(), largeInfo.Size(), smallBytes, largeBytes)
			if largeBytes > smallBytes+(1<<20) || largeBytes > 2<<20 {
				t.Fatalf("内存随文件大小增长：small=%d large=%d", smallBytes, largeBytes)
			}
		})
	}
}
