package parser

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
)

func requireCredits(t *testing.T, got, want []metadata.Credit) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("署名数量 = %d，期望 %d：实际 %+v，期望 %+v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("署名[%d] = %+v，期望 %+v；全部实际 %+v", i, got[i], want[i], got)
		}
	}
}

func TestVorbisCreditRolesAndPerformerInstruments(t *testing.T) {
	for _, tc := range []struct {
		key, raw string
		want     []metadata.Credit
	}{
		{"LYRICIST", "方文山", []metadata.Credit{{Role: "lyricist", Name: "方文山", Source: "tag"}}},
		{"ARRANGER", "林迈可; 黄雨勋", []metadata.Credit{{Role: "arranger", Name: "林迈可", Source: "tag"}, {Role: "arranger", Name: "黄雨勋", Source: "tag"}}},
		{"MIXER", "某某", []metadata.Credit{{Role: "producer", Name: "某某", Detail: "mix", Source: "tag"}}},
		{"ENGINEER", "录音者", []metadata.Credit{{Role: "producer", Name: "录音者", Detail: "recording", Source: "tag"}}},
		{"REMIXER", "重混者", []metadata.Credit{{Role: "producer", Name: "重混者", Detail: "remix", Source: "tag"}}},
		{"MIXARTIST", "重混者", []metadata.Credit{{Role: "producer", Name: "重混者", Detail: "remix", Source: "tag"}}},
		{"DJMIXER", "DJ", []metadata.Credit{{Role: "producer", Name: "DJ", Detail: "dj_mix", Source: "tag"}}},
		{"LIBRETTIST", "剧作者", []metadata.Credit{{Role: "lyricist", Name: "剧作者", Detail: "libretto", Source: "tag"}}},
		{"LIBRETTO", "剧作者", []metadata.Credit{{Role: "lyricist", Name: "剧作者", Detail: "libretto", Source: "tag"}}},
		{"VOCALS", "演唱者", []metadata.Credit{{Role: "performer", Name: "演唱者", Detail: "vocals", Source: "tag"}}},
		{"VOCALIST", "主唱", []metadata.Credit{{Role: "performer", Name: "主唱", Detail: "vocals", Source: "tag"}}},
		{"REMIXED BY", "重混者", []metadata.Credit{{Role: "producer", Name: "重混者", Detail: "remix", Source: "tag"}}},
		{"AUTHOR", "作者", []metadata.Credit{{Role: "lyricist", Name: "作者", Source: "tag"}}},
		{"WRITER", "某某", []metadata.Credit{{Role: "lyricist", Name: "某某", Source: "tag"}, {Role: "composer", Name: "某某", Source: "tag"}}},
		{"PERFORMER", "Hilary Hahn (violin)", []metadata.Credit{{Role: "performer", Name: "Hilary Hahn", Detail: "violin", Source: "tag"}}},
		{"PERFORMER", "Yo-Yo Ma (cello, conductor)", []metadata.Credit{{Role: "conductor", Name: "Yo-Yo Ma", Source: "tag"}, {Role: "performer", Name: "Yo-Yo Ma", Detail: "cello", Source: "tag"}}},
		{"PERFORMER", "Clara Rockmore (theremin)", []metadata.Credit{{Role: "performer", Name: "Clara Rockmore", Detail: "theremin", Source: "tag"}}},
		{"PERFORMER", "杨璐（羊鹿鹿）", []metadata.Credit{{Role: "performer", Name: "杨璐（羊鹿鹿）", Source: "tag"}}},
		{"PERFORMER", "Wiener Philharmoniker (orchestra)", []metadata.Credit{{Role: "performer", Name: "Wiener Philharmoniker", Detail: "orchestra", Source: "tag"}}},
		{"PERFORMER", "A (violin); B (cello)", []metadata.Credit{{Role: "performer", Name: "B", Detail: "cello", Source: "tag"}, {Role: "performer", Name: "A", Detail: "violin", Source: "tag"}}},
		{"PERFORMER", "Simon & Garfunkel (guitar)", []metadata.Credit{{Role: "performer", Name: "Simon & Garfunkel", Detail: "guitar", Source: "tag"}}},
		{"PERFORMER", "Bach, Johann (piano)", []metadata.Credit{{Role: "performer", Name: "Bach, Johann", Detail: "piano", Source: "tag"}}},
		{"PERFORMERS", "甲（钢琴）", []metadata.Credit{{Role: "performer", Name: "甲", Detail: "piano", Source: "tag"}}},
		{"SOLOIST", "A (violin & conductor)", []metadata.Credit{{Role: "conductor", Name: "A", Source: "tag"}, {Role: "performer", Name: "A", Detail: "violin", Source: "tag"}}},
		{"SOLOISTS", "A (violin and cello)", []metadata.Credit{{Role: "performer", Name: "A", Detail: "cello", Source: "tag"}, {Role: "performer", Name: "A", Detail: "violin", Source: "tag"}}},
		{"PERFORMER", "甲 (violin、指挥)", []metadata.Credit{{Role: "conductor", Name: "甲", Source: "tag"}, {Role: "performer", Name: "甲", Detail: "violin", Source: "tag"}}},
		{"PERFORMER", "杨璐（violin, 羊鹿鹿）", []metadata.Credit{{Role: "performer", Name: "杨璐（violin, 羊鹿鹿）", Source: "tag"}}},
		{"ENSEMBLE", "乐团甲", []metadata.Credit{{Role: "performer", Name: "乐团甲", Detail: "orchestra", Source: "tag"}}},
		{"ORCHESTRA", "乐团乙", []metadata.Credit{{Role: "performer", Name: "乐团乙", Detail: "orchestra", Source: "tag"}}},
		{"ART_DIRECTION", "未知工种不应被读入", nil},
	} {
		t.Run(tc.key+"/"+tc.raw, func(t *testing.T) {
			observation := parseLyricsFixture(t, "credits.flac", repairFLACComments(tc.key+"="+tc.raw))
			requireCredits(t, observation.Credits, tc.want)
			if len(tc.want) == 0 {
				if len(observation.ArtistTagEvidence) != 0 {
					t.Fatalf("未知键生成了署名证据：%+v", observation.ArtistTagEvidence)
				}
				return
			}
			role := tc.want[0].Role
			if tc.key == "PERFORMER" || tc.key == "SOLOIST" || tc.key == "SOLOISTS" {
				role = "performer"
			}
			wantEvidence := metadata.ArtistTagEvidence{Role: role, TagKey: tc.key, RawValue: tc.raw, SourceKind: "flac_vorbis_comment"}
			if len(observation.ArtistTagEvidence) != 1 || observation.ArtistTagEvidence[0] != wantEvidence {
				t.Fatalf("新键的原文证据 = %+v，期望 %+v", observation.ArtistTagEvidence, wantEvidence)
			}
		})
	}
}

func TestCreditTagsAccumulateAliasesAndDeduplicateAssignments(t *testing.T) {
	observation := parseLyricsFixture(t, "credits.flac", repairFLACComments(
		"PERFORMER=A (violin)", "PERFORMERS=A (violin)", "SOLOIST=A (cello)",
		"ARRANGER=甲", "arranger=乙", "ARRANGER=甲",
	))
	requireCredits(t, observation.Credits, []metadata.Credit{
		{Role: "arranger", Name: "乙", Source: "tag"},
		{Role: "arranger", Name: "甲", Source: "tag"},
		{Role: "performer", Name: "A", Detail: "cello", Source: "tag"},
		{Role: "performer", Name: "A", Detail: "violin", Source: "tag"},
	})
	if len(observation.ArtistTagEvidence) != 6 || observation.ArtistTagEvidence[5].OccurrenceIndex != 1 {
		t.Fatalf("多键累积时原文证据被去重或丢失次序：%+v", observation.ArtistTagEvidence)
	}
}

func TestID3TEXTTIPLTMCLCreditRoles(t *testing.T) {
	for _, tc := range []struct {
		name, frame, raw string
		major            byte
		want             []metadata.Credit
		evidence         []metadata.ArtistTagEvidence
	}{
		{"作词", "TEXT", "方文山", 4, []metadata.Credit{{Role: "lyricist", Name: "方文山", Source: "tag"}}, []metadata.ArtistTagEvidence{{Role: "lyricist", TagKey: "TEXT", RawValue: "方文山"}}},
		{"参与人员", "TIPL", "producer\x00周杰伦\x00mix\x00某某", 4, []metadata.Credit{{Role: "producer", Name: "周杰伦", Source: "tag"}, {Role: "producer", Name: "某某", Detail: "mix", Source: "tag"}}, []metadata.ArtistTagEvidence{{Role: "producer", TagKey: "TIPL:producer", RawValue: "周杰伦"}, {Role: "producer", TagKey: "TIPL:mix", RawValue: "某某"}}},
		{"未知工种由调用方保留", "TIPL", " art direction \x00某某", 4, []metadata.Credit{{Role: "producer", Name: "某某", Detail: "art direction", Source: "tag"}}, []metadata.ArtistTagEvidence{{Role: "producer", TagKey: "TIPL: art direction ", RawValue: "某某"}}},
		{"乐手与未知乐器", "TMCL", "violin\x00A\x00theremin\x00B", 4, []metadata.Credit{{Role: "performer", Name: "B", Detail: "theremin", Source: "tag"}, {Role: "performer", Name: "A", Detail: "violin", Source: "tag"}}, []metadata.ArtistTagEvidence{{Role: "performer", TagKey: "TMCL:violin", RawValue: "A"}, {Role: "performer", TagKey: "TMCL:theremin", RawValue: "B"}}},
		{"TIPL落单工种丢弃", "TIPL", "producer\x00A\x00mix", 4, []metadata.Credit{{Role: "producer", Name: "A", Source: "tag"}}, []metadata.ArtistTagEvidence{{Role: "producer", TagKey: "TIPL:producer", RawValue: "A"}}},
		{"TMCL落单乐器丢弃", "TMCL", "violin\x00A\x00cello", 4, []metadata.Credit{{Role: "performer", Name: "A", Detail: "violin", Source: "tag"}}, []metadata.ArtistTagEvidence{{Role: "performer", TagKey: "TMCL:violin", RawValue: "A"}}},
		{"旧版参与人员", "IPLS", "producer\x00A\x00art direction\x00B", 3, []metadata.Credit{{Role: "producer", Name: "A", Source: "tag"}, {Role: "producer", Name: "B", Detail: "art direction", Source: "tag"}}, []metadata.ArtistTagEvidence{{Role: "producer", TagKey: "IPLS:producer", RawValue: "A"}, {Role: "producer", TagKey: "IPLS:art direction", RawValue: "B"}}},
		{"词曲双产出只记首角色证据", "TIPL", "songwriter\x00甲", 4, []metadata.Credit{{Role: "lyricist", Name: "甲", Source: "tag"}, {Role: "composer", Name: "甲", Source: "tag"}}, []metadata.ArtistTagEvidence{{Role: "lyricist", TagKey: "TIPL:songwriter", RawValue: "甲"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, encoding := range []byte{1, 2, 3} {
				t.Run(fmt.Sprint(encoding), func(t *testing.T) {
					text := []byte(tc.raw)
					if encoding == 1 {
						text = repairUTF16(tc.raw, true)
					} else if encoding == 2 {
						text = repairUTF16(tc.raw, false)[2:]
					}
					frame := id3Frame(tc.major, tc.frame, [2]byte{}, append([]byte{encoding}, text...))
					observation := parseLyricsFixture(t, "credits.mp3", id3Tag(tc.major, 0, frame))
					requireCredits(t, observation.Credits, tc.want)
					if len(observation.ArtistTagEvidence) != len(tc.evidence) {
						t.Fatalf("成对署名证据数量不符：%+v", observation.ArtistTagEvidence)
					}
					for i, want := range tc.evidence {
						want.SourceKind = "mp3_id3v2"
						if got := observation.ArtistTagEvidence[i]; got != want {
							t.Errorf("证据[%d] = %+v，期望 %+v", i, got, want)
						}
					}
				})
			}
		})
	}
}

func TestM4AFreeformAndID3TXXXCreditRoleEntrances(t *testing.T) {
	for _, tc := range []struct {
		key, raw string
		want     []metadata.Credit
	}{
		{"LYRICIST", "方文山", []metadata.Credit{{Role: "lyricist", Name: "方文山", Source: "tag"}}},
		{"ARRANGER", "林迈可; 黄雨勋", []metadata.Credit{{Role: "arranger", Name: "林迈可", Source: "tag"}, {Role: "arranger", Name: "黄雨勋", Source: "tag"}}},
		{"MIXER", "某某", []metadata.Credit{{Role: "producer", Name: "某某", Detail: "mix", Source: "tag"}}},
		{"WRITER", "某某", []metadata.Credit{{Role: "lyricist", Name: "某某", Source: "tag"}, {Role: "composer", Name: "某某", Source: "tag"}}},
		{"PERFORMER", "A (violin); B (cello)", []metadata.Credit{{Role: "performer", Name: "B", Detail: "cello", Source: "tag"}, {Role: "performer", Name: "A", Detail: "violin", Source: "tag"}}},
		{"ART_DIRECTION", "某某", nil},
	} {
		for _, format := range []string{"m4a", "mp3"} {
			t.Run(format+"/"+tc.key, func(t *testing.T) {
				var content []byte
				prefix, source := "----:", "m4a"
				if format == "m4a" {
					content = lyricsM4A("mp4a", repairM4AFreeform(tc.key, tc.raw))
				} else {
					prefix, source = "TXXX:", "mp3_id3v2"
					content = id3Tag(4, 0, id3Frame(4, "TXXX", [2]byte{}, append([]byte{3}, []byte(tc.key+"\x00"+tc.raw)...)))
				}
				observation := parseLyricsFixture(t, "credits."+format, content)
				requireCredits(t, observation.Credits, tc.want)
				var evidence []metadata.ArtistTagEvidence
				for _, entry := range observation.ArtistTagEvidence {
					if entry.TagKey == prefix+tc.key {
						evidence = append(evidence, entry)
					}
				}
				if len(tc.want) == 0 {
					if len(evidence) != 0 {
						t.Fatalf("未知署名键未被忽略：%+v", evidence)
					}
					return
				}
				want := metadata.ArtistTagEvidence{Role: tc.want[0].Role, TagKey: prefix + tc.key, RawValue: tc.raw, SourceKind: source}
				if len(evidence) != 1 || evidence[0] != want {
					t.Fatalf("入口原文证据 = %+v，期望 %+v", evidence, want)
				}
			})
		}
	}
}

func TestContainerCreditAliasEntrances(t *testing.T) {
	for _, tc := range []struct {
		format, key, role, source string
	}{
		{"m4a", "©con", "conductor", "m4a"},
		{"m4a", "©prf", "performer", "m4a"},
		{"wav", "IPRO", "producer", "wav_info"},
		{"wav", "IWRI", "lyricist", "wav_info"},
		{"mp3", "AUTHOR", "lyricist", "mp3_id3v2"},
	} {
		t.Run(tc.format+"/"+tc.key, func(t *testing.T) {
			const raw = "测试署名者"
			var content []byte
			key := tc.key
			switch tc.format {
			case "m4a":
				atom := strings.ReplaceAll(key, "©", "\xa9")
				content = lyricsM4A("mp4a", repairM4AAtom(atom, repairM4AData(1, []byte(raw))))
			case "wav":
				info := append([]byte("INFO"), testWAVChunk(key, append([]byte(raw), 0))...)
				body := bytes.Join([][]byte{[]byte("WAVE"), testWAVChunk("fmt ", testWAVFormat()), testWAVChunk("LIST", info), testWAVChunk("data", []byte{0, 0, 0, 0})}, nil)
				content = append(binary.LittleEndian.AppendUint32([]byte("RIFF"), uint32(len(body))), body...)
			case "mp3":
				content = id3Tag(4, 0, id3Frame(4, "TXXX", [2]byte{}, append([]byte{3}, []byte(key+"\x00"+raw)...)))
				key = "TXXX:" + key
			}
			observation := parseLyricsFixture(t, "credits."+tc.format, content)
			requireCredits(t, observation.Credits, []metadata.Credit{{Role: tc.role, Name: raw, Source: "tag"}})
			var evidence []metadata.ArtistTagEvidence
			for _, entry := range observation.ArtistTagEvidence {
				if entry.TagKey == key {
					evidence = append(evidence, entry)
				}
			}
			want := metadata.ArtistTagEvidence{Role: tc.role, TagKey: key, RawValue: raw, SourceKind: tc.source}
			if len(evidence) != 1 || evidence[0] != want {
				t.Fatalf("容器署名证据 = %+v，期望 %+v", evidence, want)
			}
		})
	}
}

func parsePersonnelCreditFixture(t *testing.T, format, key, raw string) AudioObservation {
	t.Helper()
	var content []byte
	switch format {
	case "flac":
		content = repairFLACComments(key + "=" + raw)
	case "m4a":
		content = lyricsM4A("mp4a", repairM4AFreeform(key, raw))
	case "mp3":
		frame, text := key, raw
		if key == "PERSONNEL" || key == "INVOLVEDPEOPLE" {
			frame, text = "TXXX", key+"\x00"+raw
		}
		content = id3Tag(4, 0, id3Frame(4, frame, [2]byte{}, append([]byte{3}, []byte(text)...)))
	default:
		t.Fatalf("人员表测试不支持格式 %q", format)
	}
	return parseLyricsFixture(t, "personnel."+format, content)
}

func TestPersonnelCreditSeparatorsAndJobMapping(t *testing.T) {
	for _, tc := range []struct {
		name, format, raw, prefix, source string
		want                              []metadata.Credit
		evidence                          []metadata.ArtistTagEvidence
	}{
		{"制表符与角色", "flac", " Alice\tmix ", "", "flac_vorbis_comment",
			[]metadata.Credit{{Role: "producer", Name: "Alice", Detail: "mix", Source: "tag"}},
			[]metadata.ArtistTagEvidence{{Role: "producer", TagKey: "PERSONNEL:mix", RawValue: "Alice"}}},
		{"两个空格与乐器", "m4a", " Bob  violin ", "----:", "m4a",
			[]metadata.Credit{{Role: "performer", Name: "Bob", Detail: "violin", Source: "tag"}},
			[]metadata.ArtistTagEvidence{{Role: "performer", TagKey: "PERSONNEL:violin", RawValue: "Bob"}}},
		{"连字符与未知工种保留原写法", "mp3", " Carol - Art Direction ", "TXXX:", "mp3_id3v2",
			[]metadata.Credit{{Role: "performer", Name: "Carol", Detail: "Art Direction", Source: "tag"}},
			[]metadata.ArtistTagEvidence{{Role: "performer", TagKey: "PERSONNEL:Art Direction", RawValue: "Carol"}}},
		{"逗号不拆工种", "flac", "David\tviolin, cello", "", "flac_vorbis_comment",
			[]metadata.Credit{{Role: "performer", Name: "David", Detail: "violin, cello", Source: "tag"}},
			[]metadata.ArtistTagEvidence{{Role: "performer", TagKey: "PERSONNEL:violin, cello", RawValue: "David"}}},
		{"换行拆记录与姓名拆分", "flac", "Alice; Bob\tproducer\nCarol  piano", "", "flac_vorbis_comment",
			[]metadata.Credit{{Role: "performer", Name: "Carol", Detail: "piano", Source: "tag"}, {Role: "producer", Name: "Alice", Source: "tag"}, {Role: "producer", Name: "Bob", Source: "tag"}},
			[]metadata.ArtistTagEvidence{{Role: "producer", TagKey: "PERSONNEL:producer", RawValue: "Alice; Bob"}, {Role: "performer", TagKey: "PERSONNEL:piano", RawValue: "Carol"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observation := parsePersonnelCreditFixture(t, tc.format, "PERSONNEL", tc.raw)
			requireCredits(t, observation.Credits, tc.want)
			var evidence []metadata.ArtistTagEvidence
			for _, entry := range observation.ArtistTagEvidence {
				if strings.HasPrefix(entry.TagKey, tc.prefix+"PERSONNEL:") {
					evidence = append(evidence, entry)
				}
			}
			if len(evidence) != len(tc.evidence) {
				t.Fatalf("人员表证据数量不符：%+v", evidence)
			}
			for i, want := range tc.evidence {
				want.TagKey = tc.prefix + want.TagKey
				want.SourceKind = tc.source
				if evidence[i] != want {
					t.Errorf("人员表证据[%d] = %+v，期望 %+v", i, evidence[i], want)
				}
			}
		})
	}
}

func TestInvolvedPeopleTIPLTMCLTextCreditForms(t *testing.T) {
	roleCredits := []metadata.Credit{
		{Role: "producer", Name: "Bob", Detail: "Art Direction", Source: "tag"},
		{Role: "producer", Name: "Alice", Detail: "mix", Source: "tag"},
	}
	instrumentCredits := []metadata.Credit{
		{Role: "performer", Name: "Bob", Detail: "Theremin", Source: "tag"},
		{Role: "performer", Name: "Alice", Detail: "violin", Source: "tag"},
	}
	for _, form := range []struct {
		name, roles, instruments string
	}{
		{"括号保持名字在前", "Alice(mix)\nBob(Art Direction)", "Alice(violin)\nBob(Theremin)"},
		{"字面0x00成对", " mix 0x00 Alice 0x00 Art Direction 0x00 Bob ", " violin 0x00 Alice 0x00 Theremin 0x00 Bob "},
		{"分号冒号", "mix:Alice;Art Direction:Bob", "violin:Alice;Theremin:Bob"},
		{"分号偶数段", "mix;Alice;Art Direction;Bob", "violin;Alice;Theremin;Bob"},
	} {
		for _, entrance := range []struct {
			format, key, source string
		}{
			{"flac", "INVOLVEDPEOPLE", "flac_vorbis_comment"},
			{"mp3", "TIPL", "mp3_id3v2"},
			{"mp3", "TMCL", "mp3_id3v2"},
		} {
			t.Run(entrance.key+"/"+form.name, func(t *testing.T) {
				raw, want := form.roles, roleCredits
				role, jobs := "producer", []string{"mix", "Art Direction"}
				if entrance.key == "TMCL" {
					raw, want = form.instruments, instrumentCredits
					role, jobs = "performer", []string{"violin", "Theremin"}
				}
				observation := parsePersonnelCreditFixture(t, entrance.format, entrance.key, raw)
				requireCredits(t, observation.Credits, want)
				if len(observation.ArtistTagEvidence) != 2 {
					t.Fatalf("人员表成对证据数量不符：%+v", observation.ArtistTagEvidence)
				}
				for i, name := range []string{"Alice", "Bob"} {
					want := metadata.ArtistTagEvidence{Role: role, TagKey: entrance.key + ":" + jobs[i], RawValue: name, SourceKind: entrance.source}
					if got := observation.ArtistTagEvidence[i]; got != want {
						t.Errorf("人员表成对证据[%d] = %+v，期望 %+v", i, got, want)
					}
				}
			})
		}
	}
}

func TestInvolvedPeoplePrefixedCreditEntrances(t *testing.T) {
	for _, tc := range []struct {
		name, format, raw, prefix, source string
	}{
		{"freeform文本", "m4a", "mix:Alice", "----:", "m4a"},
		{"TXXX文本", "mp3", "Alice(mix)", "TXXX:", "mp3_id3v2"},
		{"TXXX真实NUL成对与落单项", "mp3", "mix\x00Alice\x00producer", "TXXX:", "mp3_id3v2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observation := parsePersonnelCreditFixture(t, tc.format, "INVOLVEDPEOPLE", tc.raw)
			requireCredits(t, observation.Credits, []metadata.Credit{{Role: "producer", Name: "Alice", Detail: "mix", Source: "tag"}})
			var evidence []metadata.ArtistTagEvidence
			for _, entry := range observation.ArtistTagEvidence {
				if strings.HasPrefix(entry.TagKey, tc.prefix+"INVOLVEDPEOPLE:") {
					evidence = append(evidence, entry)
				}
			}
			want := metadata.ArtistTagEvidence{Role: "producer", TagKey: tc.prefix + "INVOLVEDPEOPLE:mix", RawValue: "Alice", SourceKind: tc.source}
			if len(evidence) != 1 || evidence[0] != want {
				t.Fatalf("人员表入口证据 = %+v，期望 %+v", evidence, want)
			}
		})
	}
}
