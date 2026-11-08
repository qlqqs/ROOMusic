package metadata

import (
	"strings"
	"unicode/utf8"
)

// CreditRoleTableVersion 标识署名角色与乐器映射规则的版本。
const CreditRoleTableVersion = 1

// RoleAssignment 是原始工种映射出的规范角色与可选细分。
type RoleAssignment struct {
	Role   string
	Detail string
}

// CreditRoleOrder 返回曲目署名的固定展示顺序。
func CreditRoleOrder(role string) int {
	switch role {
	case "lyricist":
		return 0
	case "composer":
		return 1
	case "arranger":
		return 2
	case "conductor":
		return 3
	case "performer":
		return 4
	case "producer":
		return 5
	default:
		return 6
	}
}

func creditRoleKey(raw string) string {
	return strings.TrimSuffix(strings.ToLower(NormalizeValue(raw)), " by")
}

// MapCreditRole 只匹配表中明确列出的写法；未知工种由各来源决定兜底角色。
func MapCreditRole(raw string) []RoleAssignment {
	role, detail := "", ""
	switch creditRoleKey(raw) {
	case "lyricist", "lyrics", "words", "text", "作词", "词", "填词":
		role = "lyricist"
	case "librettist", "libretto":
		role, detail = "lyricist", "libretto"
	case "composer", "composed", "music", "作曲", "曲":
		role = "composer"
	case "songwriter", "written", "writer", "written-by", "composerlyricist", "词曲", "作词作曲", "词曲创作":
		return []RoleAssignment{{Role: "lyricist"}, {Role: "composer"}}
	case "arranger", "arranged", "arrangement", "orchestrator", "orchestration", "orchestrated", "编曲", "配器":
		role = "arranger"
	case "conductor", "conducted", "指挥":
		role = "conductor"
	case "producer", "produced", "co-producer", "executive producer", "制作人", "制作", "监制":
		role = "producer"
	case "mix", "mixer", "mixing", "mixed", "mixing engineer", "混音", "混音师", "混音工程师":
		role, detail = "producer", "mix"
	case "mastering", "mastered", "mastering engineer", "母带", "母带处理", "母带工程师":
		role, detail = "producer", "mastering"
	case "engineer", "recording engineer", "recording", "recorded", "录音", "录音师", "录音工程师":
		role, detail = "producer", "recording"
	case "remixer", "remix", "remixed", "重混":
		role, detail = "producer", "remix"
	case "dj-mix", "dj mix", "djmixer":
		role, detail = "producer", "dj_mix"
	case "programming", "programmed", "编程":
		role, detail = "producer", "programming"
	case "performer", "soloist", "演奏":
		role = "performer"
	case "vocals", "vocal", "vocalist", "lead vocals", "演唱", "主唱":
		role, detail = "performer", "vocals"
	case "backing vocals", "background vocals", "和声", "和音":
		role, detail = "performer", "backing_vocals"
	default:
		return nil
	}
	return []RoleAssignment{{Role: role, Detail: detail}}
}

// MapInstrument 将已知乐器写法映射到演奏细分，未知写法返回空集合。
func MapInstrument(raw string) []RoleAssignment {
	detail := ""
	switch creditRoleKey(raw) {
	case "violin", "violins", "小提琴":
		detail = "violin"
	case "viola", "中提琴":
		detail = "viola"
	case "cello", "violoncello", "大提琴":
		detail = "cello"
	case "double bass", "contrabass", "upright bass", "低音提琴":
		detail = "double_bass"
	case "bass", "bass guitar", "electric bass", "贝斯", "贝司":
		detail = "bass"
	case "guitar", "acoustic guitar", "electric guitar", "classical guitar", "吉他":
		detail = "guitar"
	case "piano", "pianoforte", "钢琴":
		detail = "piano"
	case "keyboard", "keyboards", "键盘":
		detail = "keyboards"
	case "organ", "pipe organ", "管风琴":
		detail = "organ"
	case "harpsichord", "羽管键琴", "大键琴":
		detail = "harpsichord"
	case "synthesizer", "synth", "合成器":
		detail = "synthesizer"
	case "drums", "drum kit", "drum set", "鼓", "架子鼓":
		detail = "drums"
	case "percussion", "打击乐":
		detail = "percussion"
	case "flute", "长笛":
		detail = "flute"
	case "clarinet", "单簧管":
		detail = "clarinet"
	case "oboe", "双簧管":
		detail = "oboe"
	case "bassoon", "巴松管", "大管":
		detail = "bassoon"
	case "saxophone", "sax", "alto saxophone", "tenor saxophone", "萨克斯", "萨克斯风":
		detail = "saxophone"
	case "trumpet", "小号":
		detail = "trumpet"
	case "trombone", "长号":
		detail = "trombone"
	case "horn", "french horn", "圆号":
		detail = "horn"
	case "harp", "竖琴":
		detail = "harp"
	case "erhu", "二胡":
		detail = "erhu"
	case "pipa", "琵琶":
		detail = "pipa"
	case "guzheng", "古筝":
		detail = "guzheng"
	case "dizi", "笛子", "竹笛":
		detail = "dizi"
	case "vocals", "vocal", "voice", "singer", "演唱", "主唱":
		detail = "vocals"
	case "backing vocals", "background vocals", "和声", "和音":
		detail = "backing_vocals"
	case "choir", "chorus", "合唱":
		detail = "choir"
	case "orchestra", "ensemble", "乐团":
		detail = "orchestra"
	default:
		return nil
	}
	return []RoleAssignment{{Role: "performer", Detail: detail}}
}

// BoundedCreditDetail 保留细分原写法，同时满足数据库的 256 字节上限。
func BoundedCreditDetail(raw string) string {
	value := BoundedText(raw)
	if len(value) > 256 {
		end := 256
		for !utf8.RuneStart(value[end]) {
			end--
		}
		value = value[:end]
	}
	return strings.TrimSpace(value)
}
