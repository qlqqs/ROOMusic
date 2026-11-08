package metadata

import "testing"

func TestIsCompilationPlaceholder(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{input: "various", want: true},
		{input: "Various Artists", want: true},
		{input: "Various Artist", want: true},
		{input: "VA", want: true},
		{input: "V.A.", want: true},
		{input: "V.A", want: true},
		{input: "V/A", want: true},
		{input: "Compilation", want: true},
		{input: "Various Composers", want: true},
		{input: "Various Recording", want: true},
		{input: "Various Recordings", want: true},
		{input: "Various Soundtrack", want: true},
		{input: "Multiple Artists", want: true},
		{input: "Diverse Artists", want: true},
		{input: "Artisti Vari", want: true},
		{input: "Artistes Divers", want: true},
		{input: "Artistas Variados", want: true},
		{input: "Blandade Artister", want: true},
		{input: "Verschillende Artiesten", want: true},
		{input: "群星", want: true},
		{input: "羣星", want: true},
		{input: "华语群星", want: true},
		{input: "華語群星", want: true},
		{input: "多位艺人", want: true},
		{input: "多位藝人", want: true},
		{input: "多位艺术家", want: true},
		{input: "多位藝術家", want: true},
		{input: "オムニバス", want: true},
		{input: "ヴァリアス・アーティスト", want: true},
		{input: "vArIoUs ArTiStS", want: true},
		{input: "ＶＡ", want: true},
		{input: "ｖａｒｉｏｕｓ　Ａｒｔｉｓｔｓ", want: true},
		{input: "Ｖ．Ａ．", want: true},
		{input: "Ｖ／Ａ", want: true},
		{input: "  Various \t\r\n Artists　", want: true},
		{input: "　群星　", want: true},
		{input: "", want: false},
		{input: " \t\r\n　", want: false},
		{input: "Unknown", want: false},
		{input: "Unknown Artist", want: false},
		{input: "未知艺术家", want: false},
		{input: "Misc", want: false},
		{input: "Sampler", want: false},
		{input: "Karaoke", want: false},
		{input: "周杰伦", want: false},
		{input: "Various Artists Orchestra", want: false},
		{input: "The Various Artists", want: false},
		{input: "Various Artists / 周杰伦", want: false},
		{input: "Various Artists; VA", want: false},
		{input: "VariousArtists", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := IsCompilationPlaceholder(tt.input); got != tt.want {
				t.Fatalf("合辑占位词判定(%q) = %t，期望 %t", tt.input, got, tt.want)
			}
		})
	}
}
