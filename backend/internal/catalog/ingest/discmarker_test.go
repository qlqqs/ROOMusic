package ingest

import "testing"

func TestParseDiscMarker(t *testing.T) {
	cases := []struct {
		name, input, prefix, suffix string
		disc                        int
		bonus                       bool
	}{
		{"CD1", "CD1", "", "", 1, false},
		{"cd 01", "cd 01", "", "", 1, false},
		{"Disc.2", "Disc.2", "", "", 2, false},
		{"DISK_3", "DISK_3", "", "", 3, false},
		{"CD(1)", "CD(1)", "", "", 1, false},
		{"Disc One", "Disc One", "", "", 1, false},
		{"Disc II", "Disc II", "", "", 2, false},
		{"subtitle", "Disc 1 - 夏", "", "夏", 1, false},
		{"bonus", "CD1 Bonus", "", "Bonus", 1, true},
		{"bonus cd", "CD 1 Bonus CD", "", "Bonus CD", 1, true},
		{"special title", "Disc 3 - 特典", "", "特典", 3, true},
		{"of count", "Disc 1 of 2", "", "", 1, false},
		{"square brackets", "[Disc 1]", "", "", 1, false},
		{"parentheses", "(CD 2)", "", "", 2, false},
		{"prefix", "Album CD1", "Album", "", 1, false},
		{"bonus prefix", "Bonus CD2", "Bonus", "", 2, true},
		{"CJK prefix", "专辑CD1", "专辑", "", 1, false},
		{"skip invalid first marker", "CD Bonus CD2", "CD Bonus", "", 2, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseDiscMarker(tc.input)
			if !ok || got.Number != tc.disc || got.Prefix != tc.prefix || got.Suffix != tc.suffix || (isBonusRemainder(got.Prefix) || isBonusRemainder(got.Suffix)) != tc.bonus || got.AtStart != (tc.prefix == "") {
				t.Fatalf("parseDiscMarker(%q) = %#v, %v; want number=%d prefix=%q suffix=%q bonus=%v", tc.input, got, ok, tc.disc, tc.prefix, tc.suffix, tc.bonus)
			}
		})
	}
}

func TestParseDiscMarkerRejectsNonDiscNames(t *testing.T) {
	for _, input := range []string{
		"1", "01", "2046", "Discography", "Disco", "CD Collection", "The CD",
		"2CD", "SACD 1", "HDCD", "CD-R", "CD-DA", "CD0", "Disc 100", "Disc Mix",
		"CD1A", "X [CD][ABCD-12345][FLAC]", "X {NetEase, WEB, CD}", "BACD-0001",
		"第一张", "第1碟", "碟1",
	} {
		t.Run(input, func(t *testing.T) {
			if got, ok := parseDiscMarker(input); ok {
				t.Fatalf("parseDiscMarker(%q) = %#v, want no match", input, got)
			}
		})
	}
}

func TestStripAlbumDiscMarker(t *testing.T) {
	cases := []struct {
		input, want string
		disc        int
		ok          bool
	}{
		{"X (Disc 2)", "X", 2, true},
		{"X CD1", "X", 1, true},
		{"X [Disc 1]", "X", 1, true},
		{"X - CD 2", "X", 2, true},
		{"X (Disc 1 of 2)", "X", 1, true},
		{"X (CD 1/2)", "X", 1, true},
		{"X (Deluxe) [CD2]", "X (Deluxe)", 2, true},
		{"X CD2 Live", "X", 2, true},
		{"Disc 2: Y", "Y", 2, true},
		{"CD1", "CD1", 0, false},
		{"Discovery", "Discovery", 0, false},
		{"2CD Set", "2CD Set", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got, disc, ok := stripAlbumDiscMarker(tc.input)
			if got != tc.want || disc != tc.disc || ok != tc.ok {
				t.Fatalf("stripAlbumDiscMarker(%q) = %q, %d, %v; want %q, %d, %v", tc.input, got, disc, ok, tc.want, tc.disc, tc.ok)
			}
		})
	}
}
