package parser

import (
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
)

func TestLyricsCreditsDesignExamples(t *testing.T) {
	const header = "[00:00.00]作词 : 冥凰\n[00:01.00]作曲 : 兰天奇\n[00:02.00]编曲 : 兰天奇/高艺蒙\n[00:03.00]制作人 : 梦帆"
	for _, tc := range []struct {
		name, lyrics string
		tags         []string
		want         []metadata.Credit
	}{
		{
			name: "时间戳署名块", lyrics: header,
			want: []metadata.Credit{
				{Role: "lyricist", Name: "冥凰", Source: "lyrics"},
				{Role: "composer", Name: "兰天奇", Source: "lyrics"},
				{Role: "arranger", Name: "兰天奇", Source: "lyrics"},
				{Role: "arranger", Name: "高艺蒙", Source: "lyrics"},
				{Role: "producer", Name: "梦帆", Source: "lyrics"},
			},
		},
		{
			name: "标签作曲优先而其它角色取歌词", lyrics: header, tags: []string{"COMPOSER=周杰伦"},
			want: []metadata.Credit{
				{Role: "lyricist", Name: "冥凰", Source: "lyrics"},
				{Role: "composer", Name: "周杰伦", Source: "tag"},
				{Role: "arranger", Name: "兰天奇", Source: "lyrics"},
				{Role: "arranger", Name: "高艺蒙", Source: "lyrics"},
				{Role: "producer", Name: "梦帆", Source: "lyrics"},
			},
		},
		{
			name: "中英混写作词", lyrics: "词 Lyricist：球球",
			want: []metadata.Credit{{Role: "lyricist", Name: "球球", Source: "lyrics"}},
		},
		{
			name: "中英混写编曲", lyrics: "编曲Arranger：李瀚洋",
			want: []metadata.Credit{{Role: "arranger", Name: "李瀚洋", Source: "lyrics"}},
		},
		{
			name: "Rap作词", lyrics: "Rap作词 : 严艺丹",
			want: []metadata.Credit{{Role: "lyricist", Name: "严艺丹", Source: "lyrics"}},
		},
		{
			name: "词曲双产出", lyrics: "词曲:宇桐非",
			want: []metadata.Credit{{Role: "lyricist", Name: "宇桐非", Source: "lyrics"}, {Role: "composer", Name: "宇桐非", Source: "lyrics"}},
		},
		{
			name: "一行多个工种", lyrics: "编曲/混音：茶语music",
			want: []metadata.Credit{{Role: "arranger", Name: "茶语music", Source: "lyrics"}, {Role: "producer", Name: "茶语music", Detail: "混音", Source: "lyrics"}},
		},
		{
			name: "监制归制作人", lyrics: "监制：某某",
			want: []metadata.Credit{{Role: "producer", Name: "某某", Source: "lyrics"}},
		},
		{
			name: "混音和录音保留原工种", lyrics: "混音工程师：欧姆\n录音师：within_轶名",
			want: []metadata.Credit{{Role: "producer", Name: "within_轶名", Detail: "录音师", Source: "lyrics"}, {Role: "producer", Name: "欧姆", Detail: "混音工程师", Source: "lyrics"}},
		},
		{
			name: "中英文乐器与所属团体后缀", lyrics: "吉他 : 怪兽@五月天\nGuitar：赵米",
			want: []metadata.Credit{{Role: "performer", Name: "赵米", Detail: "Guitar", Source: "lyrics"}, {Role: "performer", Name: "怪兽", Detail: "吉他", Source: "lyrics"}},
		},
		{
			name: "和声归演奏", lyrics: "和声：叶里",
			want: []metadata.Credit{{Role: "performer", Name: "叶里", Detail: "和声", Source: "lyrics"}},
		},
		{
			name: "忽略未知子标签但读取所有已知工种", lyrics: "吉他间奏/录音/制作人：周成",
			want: []metadata.Credit{{Role: "producer", Name: "周成", Source: "lyrics"}, {Role: "producer", Name: "周成", Detail: "录音", Source: "lyrics"}},
		},
		{
			name: "协力结束署名块", lyrics: "作词：甲\n协力 : 李汪哲\n作曲：不读取\n编曲：也不读取",
			want: []metadata.Credit{{Role: "lyricist", Name: "甲", Source: "lyrics"}},
		},
		{
			name: "标签制作人压过整个歌词制作人角色", lyrics: "制作人：甲\n混音：丙\n作词：丁", tags: []string{"PRODUCER=乙"},
			want: []metadata.Credit{{Role: "lyricist", Name: "丁", Source: "lyrics"}, {Role: "producer", Name: "乙", Source: "tag"}},
		},
		{
			name: "正文开头不再读取后续署名", lyrics: "我们的爱…\n作词：不读取",
		},
		{
			name: "跳过元信息标签", lyrics: "[ti:晴天]\n[ar:周杰伦]\n[00:00.00]作词：周杰伦",
			want: []metadata.Credit{{Role: "lyricist", Name: "周杰伦", Source: "lyrics"}},
		},
		{
			name: "正文中的合唱提示不是署名", lyrics: "作曲：甲\n[00:01.00]我们的爱\n[00:02.00]合：爱怎么做 怎么错\n作词：不读取",
			want: []metadata.Credit{{Role: "composer", Name: "甲", Source: "lyrics"}},
		},
		{
			name: "合不能独立命中乐器表", lyrics: "合：爱怎么做 怎么错\n作词：不读取",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			comments := append([]string{"LYRICS=" + tc.lyrics}, tc.tags...)
			observation := parseLyricsFixture(t, "credits.flac", repairFLACComments(comments...))
			requireCredits(t, observation.Credits, tc.want)
			requireLyrics(t, observation, tc.lyrics, LyricsSourceVorbisLyrics)
			if len(observation.ArtistTagEvidence) != len(tc.tags) {
				t.Fatalf("歌词署名进入了标签原文证据：%+v", observation.ArtistTagEvidence)
			}
		})
	}
}

func TestLyricsCreditsNormalizeBeforeReadingHeader(t *testing.T) {
	raw := "\ufeff\r\n[ti:歌名]\r\n[00:00.00][00:01.00]　作词　：　甲　\r\n\r\n[00:02.00]\r\n[00:03.00]　编曲　:　乙　\r\n正文\r\n作曲：不读取\r\n"
	observation := parseLyricsFixture(t, "credits.flac", repairFLACComments("LYRICS="+raw))
	requireCredits(t, observation.Credits, []metadata.Credit{{Role: "lyricist", Name: "甲", Source: "lyrics"}, {Role: "arranger", Name: "乙", Source: "lyrics"}})
	wantLyrics := "[ti:歌名]\n[00:00.00][00:01.00]　作词　：　甲　\n\n[00:02.00]\n[00:03.00]　编曲　:　乙　\n正文\n作曲：不读取"
	requireLyrics(t, observation, wantLyrics, LyricsSourceVorbisLyrics)
}

func TestLyricsCreditsReadAtMostThirtyLines(t *testing.T) {
	var lyrics string
	for i := 0; i < 29; i++ {
		lyrics += "[00:00.00]\n"
	}
	lyrics += "作词：甲\n作曲：不读取"
	observation := parseLyricsFixture(t, "credits.flac", repairFLACComments("LYRICS="+lyrics))
	requireCredits(t, observation.Credits, []metadata.Credit{{Role: "lyricist", Name: "甲", Source: "lyrics"}})
}
