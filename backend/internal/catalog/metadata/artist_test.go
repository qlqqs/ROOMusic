package metadata

import (
	"reflect"
	"testing"
)

func TestSplitArtistNamesFlattensConservativeSeparators(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{name: "spaced slash", input: "Artist Beta / Artist Alpha", want: []string{"Artist Beta", "Artist Alpha"}},
		{name: "nested comma and ampersand", input: "Artist Alpha, Artist Beta & Artist Gamma", want: []string{"Artist Alpha, Artist Beta", "Artist Gamma"}},
		{name: "compact slash", input: "Artist Alpha/Artist Beta", want: []string{"Artist Alpha", "Artist Beta"}},
		{name: "fixed ampersand group", input: "Simon & Garfunkel", want: []string{"Simon & Garfunkel"}},
		{name: "fixed slash group", input: "AC/DC", want: []string{"AC/DC"}},
		{name: "fixed comma group", input: "Earth, Wind & Fire", want: []string{"Earth, Wind & Fire"}},
		{name: "control cleanup before fixed group", input: "AC/\x00DC", want: []string{"AC/", "DC"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := splitArtistNames(tt.input); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("艺人拆分结果 = %#v，期望 %#v", got, tt.want)
			}
		})
	}
}

func TestCanonicalArtistNamesPreserveSpellingsAndDeduplicateExactNames(t *testing.T) {
	got := CanonicalArtistNames("Jay Chou / 周杰倫 / 周杰伦 / jay chou / Jay Chou / Guest Artist")
	want := []string{"Jay Chou", "周杰倫", "周杰伦", "jay chou", "Guest Artist"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("艺人原文去重结果 = %#v，期望 %#v", got, want)
	}
}

func TestCanonicalTrackArtistUsesStableDisplayOrder(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "拉丁单逗号整体保留", input: "Artist Beta, Artist Alpha", want: "Artist Beta, Artist Alpha"},
		{name: "明确分隔后排序去重", input: "Artist Beta / Artist Alpha / Artist Beta", want: "Artist Alpha / Artist Beta"},
		{name: "多值姓名整体参与排序", input: "Gould, Glenn\x00Bach, Johann Sebastian", want: "Bach, Johann Sebastian / Gould, Glenn"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanonicalTrackArtist(tt.input); got != tt.want {
				t.Fatalf("曲目艺人展示 = %q，期望 %q", got, tt.want)
			}
		})
	}
}

func TestSplitArtistNamesHandlesMultivalueAndSurnameRules(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{name: "网易云紧凑逗号", input: "成龙,金喜善", want: []string{"成龙", "金喜善"}},
		{name: "网易云多个逗号", input: "知更鸟,HOYO-MiX,Chevy", want: []string{"知更鸟", "HOYO-MiX", "Chevy"}},
		{name: "中日文混合艺人", input: "RADWIMPS, 陣内一真", want: []string{"RADWIMPS", "陣内一真"}},
		{name: "中文带空格逗号", input: "周杰伦, 费玉清", want: []string{"周杰伦", "费玉清"}},
		{name: "拉丁姓名", input: "Bach, Johann Sebastian", want: []string{"Bach, Johann Sebastian"}},
		{name: "姓氏前缀", input: "Karajan, Herbert von", want: []string{"Karajan, Herbert von"}},
		{name: "变音字母", input: "Dvořák, Antonín", want: []string{"Dvořák, Antonín"}},
		{name: "姓名缩写", input: "Bach, J.S.", want: []string{"Bach, J.S."}},
		{name: "爱乐团体词", input: "Berliner Philharmoniker, Herbert von Karajan", want: []string{"Berliner Philharmoniker", "Herbert von Karajan"}},
		{name: "交响乐团词", input: "London Symphony Orchestra, Colin Davis", want: []string{"London Symphony Orchestra", "Colin Davis"}},
		{name: "分号嵌套姓名", input: "Bach, Johann Sebastian; Karajan, Herbert von", want: []string{"Bach, Johann Sebastian", "Karajan, Herbert von"}},
		{name: "多逗号仍拆四段", input: "Bach, Johann Sebastian, Gould, Glenn", want: []string{"Bach", "Johann Sebastian", "Gould", "Glenn"}},
		{name: "换行多值", input: "A\nB", want: []string{"A", "B"}},
		{name: "回车换行多值", input: "A\r\nB", want: []string{"A", "B"}},
		{name: "NUL多值", input: "A\x00B", want: []string{"A", "B"}},
		{name: "换行姓名整体保留", input: "Bach, Johann Sebastian\nGould, Glenn", want: []string{"Bach, Johann Sebastian", "Gould, Glenn"}},
		{name: "未登记组合照拆", input: "Tom & Jerry", want: []string{"Tom", "Jerry"}},
		{name: "固定与号组合", input: "Simon & Garfunkel", want: []string{"Simon & Garfunkel"}},
		{name: "固定斜线组合", input: "AC/DC", want: []string{"AC/DC"}},
		{name: "固定逗号组合", input: "Earth, Wind & Fire", want: []string{"Earth, Wind & Fire"}},
		{name: "中文与号名单", input: "张卫帆 & 郑宜农", want: []string{"张卫帆", "郑宜农"}},
		{name: "已知Queen误判保持整体", input: "Queen, David Bowie", want: []string{"Queen, David Bowie"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := splitArtistNames(tt.input); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("艺人拆分结果 = %#v，期望 %#v", got, tt.want)
			}
		})
	}
}

func TestJoinArtistValues(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		want   string
	}{
		{name: "没有值", want: ""},
		{name: "丢弃空值与控制字符", values: []string{"", " ", "\r\n\x00", "\t\x01"}, want: ""},
		{name: "单值清理", values: []string{"  AC/\tDC  "}, want: "AC/DC"},
		{name: "多个值保留首次顺序", values: []string{"B", "A", "B"}, want: "B / A"},
		{name: "换行与NUL展开", values: []string{" A\nB\r\nC\rD\x00E ", "B\x00F"}, want: "A / B / C / D / E / F"},
		{name: "清理后按原文去重", values: []string{" A ", "A", "a", "周杰倫", "周杰伦", "周杰倫"}, want: "A / a / 周杰倫 / 周杰伦"},
		{name: "不解释其它艺人分隔符", values: []string{"成龙,金喜善", "Tom & Jerry", "A; B", "A/B"}, want: "成龙,金喜善 / Tom & Jerry / A; B / A/B"},
		{name: "保留姓名与固定组合", values: []string{"Bach, Johann Sebastian\nAC/DC", "Earth, Wind & Fire"}, want: "Bach, Johann Sebastian / AC/DC / Earth, Wind & Fire"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := JoinArtistValues(tt.values...); got != tt.want {
				t.Fatalf("艺人多值拼接 = %q，期望 %q", got, tt.want)
			}
		})
	}
}
