package parser

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/library/parser/parsertest"
)

const wavpackStereo44100 = uint32(1 | 9<<23 | 0x800 | 0x1000)

func TestAPEHeaders(t *testing.T) {
	audio := bytes.Repeat([]byte{0x23}, 32)
	cases := []struct {
		name   string
		data   []byte
		bits   int
		frames uint64
	}{
		{"新版", parsertest.APE(md5.Sum(audio), audio), 16, 4410},
		{"3.80普通", parsertest.APELegacy(3800, 2000, 0x20, audio), 16, 9216 + 4410},
		{"3.80高压缩8位", parsertest.APELegacy(3800, 4000, 0x21, audio), 8, 73728 + 4410},
		{"3.90含WAV头", parsertest.APELegacy(3900, 2000, 0, audio), 16, 73728 + 4410},
		{"3.95含峰值seek数", parsertest.APELegacy(3950, 2000, 0x34, audio), 16, 294912 + 4410},
		{"3.97含全部可选字段24位", parsertest.APELegacy(3970, 4000, 0x1c, audio), 24, 294912 + 4410},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			obs, err := ParseAudioFile(writeFingerprintFixture(t, "曲目.APE", tc.data))
			if err != nil {
				t.Fatal(err)
			}
			assertLosslessFacts(t, obs, "ape_apev2", "ape", 44100, tc.bits, 2, float64(tc.frames)/44100)
			if obs.Title != "曲目" || !obs.InferredFields["title"] || obs.Bitrate != int(float64(len(tc.data))*8/(float64(tc.frames)/44100)/1000) {
				t.Fatalf("文件名兜底或估算码率有误：%+v", obs)
			}
		})
	}
}

func TestAPEID3Prefix(t *testing.T) {
	audio := bytes.Repeat([]byte{0x45}, 32)
	tag := repairMP3Frames(map[string]string{"TIT2": "前缀不作为标签"})
	withFooter := append([]byte(nil), tag...)
	withFooter[5] |= 0x10
	footer := append([]byte(nil), withFooter[:10]...)
	copy(footer, "3DI")
	withFooter = append(withFooter, footer...)
	for _, legacy := range []bool{false, true} {
		for name, prefix := range map[string][]byte{"ID3头": tag, "ID3头及页脚": withFooter} {
			t.Run(fmt.Sprintf("旧头=%v/%s", legacy, name), func(t *testing.T) {
				data := parsertest.APE(md5.Sum(audio), audio)
				if legacy {
					data = parsertest.APELegacy(3970, 2000, 0x20, audio)
				}
				bare := fingerprintOf(t, writeFingerprintFixture(t, "bare.ape", data))
				path := writeFingerprintFixture(t, "带前缀.ape", append(append([]byte(nil), prefix...), data...))
				obs, err := ParseAudioFile(path)
				if err != nil || obs.Title != "带前缀" || obs.SampleRate != 44100 {
					t.Fatalf("ID3 前缀应只跳过：%+v，错误 %v", obs, err)
				}
				if got := fingerprintOf(t, path); got != bare {
					t.Fatalf("添加前缀改变指纹：%s / %s", got, bare)
				}
			})
		}
	}
}

func TestWavPackParameters(t *testing.T) {
	payload := parsertest.WavPackMetadata(0x0a, []byte{1, 2, 3, 4})
	custom := append(parsertest.WavPackMetadata(0x27, []byte{0x00, 0x77, 0x01}), payload...)
	channels := append(parsertest.WavPackMetadata(0x0d, []byte{6, 0x3f}), payload...)
	dsd := append(parsertest.WavPackMetadata(0x0e, []byte{3, 0}), payload...)
	unknown := bytes.Join([][]byte{
		parsertest.WavPackBlock(wavpackStereo44100&^0x1000, -1, 22050, payload),
		parsertest.WavPackBlock(wavpackStereo44100&^0x800, -1, 22050, payload),
		parsertest.WavPackBlock(wavpackStereo44100&^0x1000, -1, 11025, payload),
		parsertest.WavPackBlock(wavpackStereo44100&^0x800, -1, 11025, payload),
	}, nil)
	cases := []struct {
		name, codec          string
		data                 []byte
		rate, bits, channels int
		duration             float64
	}{
		{"标准立体声", "wavpack", parsertest.WavPackBlock(wavpackStereo44100, 44100, 44100, payload), 44100, 16, 2, 1},
		{"自定义采样率24位", "wavpack", parsertest.WavPackBlock(2|15<<23|0x1800, 96000, 96000, custom), 96000, 24, 2, 1},
		{"有效位深20位", "wavpack", parsertest.WavPackBlock((wavpackStereo44100&^3)|2|4<<13, 44100, 44100, payload), 44100, 20, 2, 1},
		{"单声道", "wavpack", parsertest.WavPackBlock(wavpackStereo44100|4, 44100, 44100, payload), 44100, 16, 1, 1},
		{"声道子块", "wavpack", parsertest.WavPackBlock(wavpackStereo44100, 44100, 44100, channels), 44100, 16, 6, 1},
		{"累计多块声道", "wavpack", bytes.Join([][]byte{
			parsertest.WavPackBlock(wavpackStereo44100&^0x1000, 44100, 44100, payload),
			parsertest.WavPackBlock(wavpackStereo44100&^0x1800, 44100, 44100, payload),
			parsertest.WavPackBlock(wavpackStereo44100&^0x800, 44100, 44100, payload),
		}, nil), 44100, 16, 6, 1},
		{"未知样本不重复累计声道块", "wavpack", unknown, 44100, 16, 4, .75},
		{"浮点", "wavpack_float", parsertest.WavPackBlock(wavpackStereo44100|0x80, 44100, 44100, payload), 44100, 32, 2, 1},
		{"混合", "wavpack_hybrid", parsertest.WavPackBlock(wavpackStereo44100|8, 44100, 44100, payload), 44100, 16, 2, 1},
		{"DSD原生参数", "wavpack_dsd", parsertest.WavPackBlock(wavpackStereo44100|0x80000000, 352800, 352800, dsd), 2822400, 1, 2, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			obs, err := ParseAudioFile(writeFingerprintFixture(t, "track.wv", tc.data))
			if err != nil {
				t.Fatal(err)
			}
			assertLosslessFacts(t, obs, "wavpack_apev2", tc.codec, tc.rate, tc.bits, tc.channels, tc.duration)
		})
	}
}

func TestAIFFCodecMapping(t *testing.T) {
	for _, tc := range []struct {
		compression, codec string
		bits               int
	}{
		{"", "aiff", 24}, {"NONE", "aiff", 16}, {"twos", "aiff", 24}, {"sowt", "aiff", 16},
		{"fl32", "aiff_float", 32}, {"FL64", "aiff_float", 64}, {"ulaw", "aifc_ulaw", 8}, {"raw ", "aifc_raw", 8},
	} {
		t.Run(tc.compression, func(t *testing.T) {
			data := parsertest.AIFF(tc.compression, 48000, tc.bits, 2, 24000, []byte{1, 2, 3, 4})
			for _, extension := range []string{".aiff", ".AIF"} {
				obs, err := ParseAudioFile(writeFingerprintFixture(t, "track"+extension, data))
				if err != nil {
					t.Fatal(err)
				}
				assertLosslessFacts(t, obs, "aiff_id3", tc.codec, 48000, tc.bits, 2, .5)
			}
		})
	}
}

func TestAIFFTagsPreferID3ToTextChunks(t *testing.T) {
	id3 := createMP3Fixture(t, map[string]string{"TIT2": "ID3标题", "TPE1": "ID3艺人", "TALB": "专辑", "TRCK": "3/12", "TPOS": "2/3"})
	for _, id := range []string{"ID3 ", "id3 "} {
		for _, id3First := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/ID3在前=%v", id, id3First), func(t *testing.T) {
				chunks := [][]byte{parsertest.AIFFChunk("NAME", []byte("后备标题x")), parsertest.AIFFChunk("AUTH", []byte("后备艺人"))}
				if id3First {
					chunks = append([][]byte{parsertest.AIFFChunk(id, id3)}, chunks...)
				} else {
					chunks = append(chunks, parsertest.AIFFChunk(id, id3))
				}
				obs, err := ParseAudioFile(writeFingerprintFixture(t, "track.aiff", parsertest.AIFF("", 44100, 16, 2, 4410, []byte{1, 2}, chunks...)))
				if err != nil || obs.Title != "ID3标题" || obs.Artist != "ID3艺人" || obs.Album != "专辑" || obs.TrackNumber != 3 || obs.DiscNumber != 2 {
					t.Fatalf("ID3 映射或优先级有误：%+v，错误 %v", obs, err)
				}
			})
		}
	}
	t.Run("缺少ID3时文本兜底", func(t *testing.T) {
		data := parsertest.AIFF("", 44100, 16, 2, 4410, []byte{1, 2}, parsertest.AIFFChunk("NAME", []byte("标题\x00")), parsertest.AIFFChunk("AUTH", []byte("艺人")))
		obs, err := ParseAudioFile(writeFingerprintFixture(t, "track.aiff", data))
		if err != nil || obs.Title != "标题" || obs.Artist != "艺人" || obs.InferredFields["title"] || obs.InferredFields["artist"] {
			t.Fatalf("文本标签兜底有误：%+v，错误 %v", obs, err)
		}
	})
}

func TestAPEv2Tags(t *testing.T) {
	items := []parsertest.APEv2Item{
		{Key: "tItLe", Value: []byte("标题")}, {Key: "Artist", Value: []byte("甲\x00乙")},
		{Key: "Album", Value: []byte("专辑")}, {Key: "Album Artist", Value: []byte("专辑艺人")},
		{Key: "Track", Value: []byte("3/12")}, {Key: "Disc", Value: []byte("2/3")},
		{Key: "Year", Value: []byte("2026-11-08")}, {Key: "Genre", Value: []byte("Rock\x00Pop")},
		{Key: "Composer", Value: []byte("作曲甲\x00作曲乙")}, {Key: "Lyrics", Value: []byte("第一行\n第二行")},
	}
	for _, version := range []uint32{1000, 2000} {
		for _, header := range []bool{false, true} {
			t.Run(fmt.Sprintf("版本%d/有头=%v", version, header), func(t *testing.T) {
				tag := parsertest.APEv2(version, header, items...)
				for extension, stream := range map[string][]byte{
					".ape": parsertest.APE([16]byte{}, []byte{1, 2, 3, 4}),
					".wv":  parsertest.WavPackBlock(wavpackStereo44100, 4410, 4410, parsertest.WavPackMetadata(0x0a, []byte{1, 2})),
				} {
					for _, trailingID3 := range []bool{false, true} {
						data := append(append([]byte(nil), stream...), tag...)
						if trailingID3 {
							data = append(data, testID3v1()...)
						}
						obs, err := ParseAudioFile(writeFingerprintFixture(t, "track"+extension, data))
						if err != nil {
							t.Fatal(err)
						}
						if obs.Title != "标题" || obs.Artist != "乙 / 甲" || obs.Album != "专辑" || obs.AlbumArtist != "专辑艺人" || obs.TrackNumber != 3 || obs.DiscNumber != 2 || obs.Year != 2026 || obs.Lyrics != "第一行\n第二行" {
							t.Fatalf("%s/尾部ID3=%v：标签映射有误：%+v", extension, trailingID3, obs)
						}
						if !reflect.DeepEqual(obs.Genres, []string{"Rock", "Pop"}) || len(obs.Credits) != 2 {
							t.Fatalf("多值流派或作曲丢失：%+v", obs)
						}
						var artists []string
						for _, item := range obs.ArtistTagEvidence {
							if item.Role == "track_artist" {
								artists = append(artists, item.RawValue)
							}
						}
						if !reflect.DeepEqual(artists, []string{"甲", "乙"}) {
							t.Fatalf("APEv2 多值没有逐个交给标签读取：%#v", artists)
						}
					}
				}
			})
		}
	}
}

func TestAPEv2Artwork(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value []byte
		mime  string
		image []byte
	}{
		{"JPEG", append([]byte("cover.jpg\x00"), 0xff, 0xd8, 1, 2), "image/jpeg", []byte{0xff, 0xd8, 1, 2}},
		{"PNG", []byte("cover.bin\x00\x89PNG\r\n\x1a\nimage"), "image/png", []byte("\x89PNG\r\n\x1a\nimage")},
		{"扩展名不能冒充图片", []byte("cover.jpg\x00not-an-image"), "", nil},
		{"缺少文件名分隔符", []byte{0xff, 0xd8, 1, 2}, "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := append(parsertest.APE([16]byte{}, []byte{1, 2}), parsertest.APEv2(2000, false, parsertest.APEv2Item{Key: "Cover Art (Front)", Flags: 2, Value: tc.value})...)
			obs, err := ParseAudioFile(writeFingerprintFixture(t, "track.ape", data))
			if err != nil || obs.ArtworkMIME != tc.mime || !bytes.Equal(obs.Artwork, tc.image) {
				t.Fatalf("封面有误：%x / %s，错误 %v", obs.Artwork, obs.ArtworkMIME, err)
			}
		})
	}
}

func TestAPEv2MalformedTagsAreIgnoredAtomically(t *testing.T) {
	valid := parsertest.APEv2(2000, false, parsertest.APEv2Item{Key: "Title", Value: []byte("不能残留")}, parsertest.APEv2Item{Key: "Artist", Value: []byte("艺人")})
	for name, mutate := range map[string]func([]byte){
		"末项值长度越界": func(data []byte) {
			binary.LittleEndian.PutUint32(data[8+len("Title")+1+len("不能残留"):], 0xffffffff)
		},
		"末项键不是ASCII": func(data []byte) { data[8+len("Title")+1+len("不能残留")+8] = 0xff },
		"条目过多":       func(data []byte) { binary.LittleEndian.PutUint32(data[len(data)-16:], 1025) },
		"页脚长度越界":     func(data []byte) { binary.LittleEndian.PutUint32(data[len(data)-20:], 0xffffffff) },
	} {
		t.Run(name, func(t *testing.T) {
			tag := append([]byte(nil), valid...)
			mutate(tag)
			data := append(parsertest.APE([16]byte{}, []byte{1, 2}), tag...)
			obs, err := ParseAudioFile(writeFingerprintFixture(t, "后备标题.ape", data))
			if err != nil || obs.Title != "后备标题" || obs.Artist != "未知艺术家" || len(obs.ArtistTagEvidence) != 0 {
				t.Fatalf("畸形标签没有整段忽略：%+v，错误 %v", obs, err)
			}
		})
	}
}

func TestAPERejectsDamagedFiles(t *testing.T) {
	for name, mutate := range map[string]func([]byte){
		"魔数":    func(data []byte) { data[0] = 'x' },
		"版本过旧":  func(data []byte) { binary.LittleEndian.PutUint16(data[4:6], 3799) },
		"版本过新":  func(data []byte) { binary.LittleEndian.PutUint16(data[4:6], 10000) },
		"描述块长度": func(data []byte) { binary.LittleEndian.PutUint32(data[8:12], 51) },
		"帧范围越界": func(data []byte) { binary.LittleEndian.PutUint32(data[24:28], 10000) },
		"零声道":   func(data []byte) { binary.LittleEndian.PutUint16(data[70:72], 0) },
		"零采样率":  func(data []byte) { binary.LittleEndian.PutUint32(data[72:76], 0) },
		"零总帧数":  func(data []byte) { binary.LittleEndian.PutUint32(data[64:68], 0) },
	} {
		t.Run(name, func(t *testing.T) {
			data := parsertest.APE([16]byte{}, []byte{1, 2})
			mutate(data)
			assertLosslessRejected(t, ".ape", data)
		})
	}
	for name, data := range map[string][]byte{
		"截断头":        []byte("MAC \x96\x0f"),
		"ID3长度超限":    {'I', 'D', '3', 4, 0, 0, 0x7f, 0x7f, 0x7f, 0x7f},
		"ID3非同步安全长度": {'I', 'D', '3', 4, 0, 0, 0x80, 0, 0, 0},
	} {
		t.Run(name, func(t *testing.T) { assertLosslessRejected(t, ".ape", data) })
	}
}

func TestWavPackRejectsDamagedFiles(t *testing.T) {
	for name, mutate := range map[string]func([]byte){
		"魔数":   func(data []byte) { data[0] = 'x' },
		"块过短":  func(data []byte) { binary.LittleEndian.PutUint32(data[4:8], 23) },
		"块越界":  func(data []byte) { binary.LittleEndian.PutUint32(data[4:8], 1000) },
		"版本":   func(data []byte) { binary.LittleEndian.PutUint16(data[8:10], 0x401) },
		"子块越界": func(data []byte) { data[33] = 100 },
	} {
		t.Run(name, func(t *testing.T) {
			data := parsertest.WavPackBlock(wavpackStereo44100, 4410, 4410, parsertest.WavPackMetadata(0x0a, []byte{1, 2}))
			mutate(data)
			assertLosslessRejected(t, ".wv", data)
		})
	}
	t.Run("缺自定义采样率", func(t *testing.T) {
		assertLosslessRejected(t, ".wv", parsertest.WavPackBlock(1|15<<23|0x1800, 4410, 4410, nil))
	})
	t.Run("截断头", func(t *testing.T) { assertLosslessRejected(t, ".wv", []byte("wvpk")) })
}

func TestAIFFRejectsDamagedFiles(t *testing.T) {
	for name, mutate := range map[string]func([]byte){
		"魔数":       func(data []byte) { data[0] = 'x' },
		"FORM越界":   func(data []byte) { binary.BigEndian.PutUint32(data[4:8], uint32(len(data))) },
		"COMM越界":   func(data []byte) { binary.BigEndian.PutUint32(data[16:20], uint32(len(data))) },
		"零声道":      func(data []byte) { binary.BigEndian.PutUint16(data[20:22], 0) },
		"负采样率":     func(data []byte) { data[28] |= 0x80 },
		"SSND偏移越界": func(data []byte) { binary.BigEndian.PutUint32(data[46:50], 1000) },
	} {
		t.Run(name, func(t *testing.T) {
			data := parsertest.AIFF("", 44100, 16, 2, 4410, []byte{1, 2})
			mutate(data)
			assertLosslessRejected(t, ".aiff", data)
		})
	}
	t.Run("没有SSND", func(t *testing.T) {
		data := parsertest.AIFF("", 44100, 16, 2, 4410, nil)[:38]
		binary.BigEndian.PutUint32(data[4:8], uint32(len(data)-8))
		assertLosslessRejected(t, ".aiff", data)
	})
}

func TestFingerprintLosslessTagsAndAudio(t *testing.T) {
	audio := bytes.Repeat([]byte{1, 2, 3, 4}, 40)
	changed := append([]byte(nil), audio...)
	changed[20] ^= 0xff
	for _, tc := range []struct {
		name, extension, kind string
		build                 func([]byte, string) []byte
	}{
		{"APE摘要", ".ape", "ape-md5-v1", func(audio []byte, title string) []byte {
			return append(parsertest.APE(md5.Sum(audio), audio), parsertest.APEv2(2000, true, parsertest.APEv2Item{Key: "Title", Value: []byte(title)})...)
		}},
		{"APE帧", ".ape", "ape-frames-v1", func(audio []byte, title string) []byte {
			return append(parsertest.APE([16]byte{}, audio), parsertest.APEv2(2000, true, parsertest.APEv2Item{Key: "Title", Value: []byte(title)})...)
		}},
		{"APE旧头帧", ".ape", "ape-frames-v1", func(audio []byte, title string) []byte {
			return append(parsertest.APELegacy(3970, 2000, 0x20, audio), parsertest.APEv2(2000, true, parsertest.APEv2Item{Key: "Title", Value: []byte(title)})...)
		}},
		{"WavPack块", ".wv", "wavpack-blocks-v1", func(audio []byte, title string) []byte {
			return append(parsertest.WavPackBlock(wavpackStereo44100, 4410, 4410, parsertest.WavPackMetadata(0x0a, audio)), parsertest.APEv2(2000, true, parsertest.APEv2Item{Key: "Title", Value: []byte(title)})...)
		}},
		{"AIFF声音", ".aiff", "aiff-ssnd-v1", func(audio []byte, title string) []byte {
			return parsertest.AIFF("", 44100, 16, 2, 4410, audio, parsertest.AIFFChunk("NAME", []byte(title)), parsertest.AIFFChunk("AUTH", []byte(title)), parsertest.AIFFChunk("ID3 ", parsertest.ID3v23(repairMP3BinaryFrame("TIT2", append([]byte{3}, title...))[10:])))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			one := writeFingerprintFixture(t, "one"+tc.extension, tc.build(audio, "短标题"))
			two := writeFingerprintFixture(t, "two"+tc.extension, tc.build(audio, strings.Repeat("长标题", 50)))
			three := writeFingerprintFixture(t, "three"+tc.extension, tc.build(changed, "短标题"))
			first, retagged, replaced := fingerprintOf(t, one), fingerprintOf(t, two), fingerprintOf(t, three)
			if !strings.HasPrefix(first, tc.kind+":") || first != retagged || first == replaced {
				t.Fatalf("指纹合同不符：原始 %s，改标签 %s，改音频 %s", first, retagged, replaced)
			}
			obs, err := ParseAudioFile(one)
			if err != nil {
				t.Fatal(err)
			}
			wantAtParse := ""
			if tc.kind == "ape-md5-v1" {
				wantAtParse = first
			}
			if obs.ContentFingerprint != wantAtParse {
				t.Fatalf("解析时指纹 = %q，期望 %q", obs.ContentFingerprint, wantAtParse)
			}
			for _, extension := range []string{tc.extension, strings.ToUpper(tc.extension)} {
				data := tc.build(audio, "短标题")
				got, err := ContentFingerprintOf(context.Background(), extension, bytes.NewReader(data), int64(len(data)))
				if err != nil || got != first {
					t.Fatalf("句柄指纹 = %q，错误 %v，期望 %q", got, err, first)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := ContentFingerprint(ctx, one); !errors.Is(err, context.Canceled) {
				t.Fatalf("取消后错误 = %v，期望 context.Canceled", err)
			}
		})
	}
}

func TestFingerprintAPEZeroMD5Fallback(t *testing.T) {
	audio := []byte{1, 2, 3, 4}
	sum := md5.Sum(audio)
	withMD5 := fingerprintOf(t, writeFingerprintFixture(t, "md5.ape", parsertest.APE(sum, audio)))
	if want := fmt.Sprintf("ape-md5-v1:%x:4410:44100:2:16", sum); withMD5 != want {
		t.Fatalf("原生摘要指纹 = %s，期望 %s", withMD5, want)
	}
	facts := binary.BigEndian.AppendUint64(nil, 4410)
	for _, value := range []uint32{44100, 2, 16} {
		facts = binary.BigEndian.AppendUint32(facts, value)
	}
	withoutMD5 := fingerprintOf(t, writeFingerprintFixture(t, "zero.ape", parsertest.APE([16]byte{}, audio)))
	if want := "ape-frames-v1:" + sha256Hex(facts, audio); withoutMD5 != want {
		t.Fatalf("零摘要应哈希关键参数与帧：%s，期望 %s", withoutMD5, want)
	}
	// 原生摘要相同的重新压缩文件应视为同一内容，不再重复哈希压缩帧。
	if got := fingerprintOf(t, writeFingerprintFixture(t, "recompressed.ape", parsertest.APE(sum, []byte{9, 8, 7, 6, 5, 4}))); got != withMD5 {
		t.Fatalf("相同原生摘要重新压缩后改变指纹：%s / %s", got, withMD5)
	}
}

func TestFingerprintLosslessNoPayload(t *testing.T) {
	for extension, data := range map[string][]byte{
		".ape":  parsertest.APE([16]byte{1}, nil),
		".wv":   parsertest.WavPackBlock(wavpackStereo44100, 0, 0, nil),
		".aiff": parsertest.AIFF("", 44100, 16, 2, 0, nil),
	} {
		t.Run(extension, func(t *testing.T) {
			got, err := ContentFingerprintOf(context.Background(), extension, bytes.NewReader(data), int64(len(data)))
			if err != nil || got != "" {
				t.Fatalf("无音频负载的指纹 = %q，错误 %v，期望空", got, err)
			}
		})
	}
}

func assertLosslessFacts(t *testing.T, obs AudioObservation, source, codec string, rate, bits, channels int, duration float64) {
	t.Helper()
	if obs.SourceKind != source || obs.Codec != codec || obs.SampleRate != rate || obs.BitDepth != bits || obs.Channels != channels || math.Abs(obs.DurationSeconds-duration) > 1e-7 {
		t.Fatalf("参数 = %s/%s %dHz/%dbit/%dch/%fs，期望 %s/%s %dHz/%dbit/%dch/%fs", obs.SourceKind, obs.Codec, obs.SampleRate, obs.BitDepth, obs.Channels, obs.DurationSeconds, source, codec, rate, bits, channels, duration)
	}
}

func assertLosslessRejected(t *testing.T, extension string, data []byte) {
	t.Helper()
	if _, err := ParseAudioFile(writeFingerprintFixture(t, "damaged"+extension, data)); err == nil {
		t.Fatal("损坏文件未被拒绝")
	}
}

func TestFingerprintAIFFSkipsSoundOffsetAndPadding(t *testing.T) {
	audio := []byte{1, 2, 3, 4}
	bare := parsertest.AIFF("sowt", 44100, 16, 2, 1, audio)
	commSize := int(binary.BigEndian.Uint32(bare[16:20]))
	soundChunk := 20 + commSize + commSize%2
	soundData := soundChunk + 16
	padding := []byte{9, 8, 7, 6}
	padded := bytes.Join([][]byte{bare[:soundData], padding, bare[soundData:]}, nil)
	binary.BigEndian.PutUint32(padded[4:8], uint32(len(padded)-8))
	binary.BigEndian.PutUint32(padded[soundChunk+4:soundChunk+8], uint32(8+len(padding)+len(audio)))
	binary.BigEndian.PutUint32(padded[soundChunk+8:soundChunk+12], uint32(len(padding)))
	binary.BigEndian.PutUint32(padded[soundChunk+12:soundChunk+16], 512)
	first := fingerprintOf(t, writeFingerprintFixture(t, "bare.aiff", bare))
	second := fingerprintOf(t, writeFingerprintFixture(t, "padded.aiff", padded))
	if first != second {
		t.Fatalf("SSND offset/blockSize/填充改变内容指纹：%s / %s", first, second)
	}
	changed := append([]byte(nil), padded...)
	changed[len(changed)-1] ^= 0xff
	if fingerprintOf(t, writeFingerprintFixture(t, "changed.aiff", changed)) == first {
		t.Fatal("跳过 offset 后没有哈希实际声音数据")
	}
}
