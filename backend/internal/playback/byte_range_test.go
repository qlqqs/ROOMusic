package playback

import "testing"

func TestCheckRange(t *testing.T) {
	const size = 1000
	for _, testCase := range []struct {
		name   string
		header string
		size   int64
		want   RangeStatus
	}{
		{"no header", "", size, RangeAbsent},
		{"whole file", "bytes=0-", size, RangeSingle},
		{"first byte", "bytes=0-0", size, RangeSingle},
		{"middle", "bytes=5-9", size, RangeSingle},
		{"open ended from the middle", "bytes=500-", size, RangeSingle},
		{"last byte by offset", "bytes=999-", size, RangeSingle},
		{"end beyond the content is clamped by ServeContent", "bytes=0-99999", size, RangeSingle},
		{"suffix", "bytes=-5", size, RangeSingle},
		{"suffix longer than the content", "bytes=-5000", size, RangeSingle},
		{"spaces around the numbers", "bytes= 5 - 9 ", size, RangeSingle},
		{"start at the size", "bytes=1000-", size, RangeUnsatisfiable},
		{"start beyond the size", "bytes=5000-6000", size, RangeUnsatisfiable},
		{"suffix of an empty file", "bytes=-5", 0, RangeUnsatisfiable},
		{"any range of an empty file", "bytes=0-", 0, RangeUnsatisfiable},
		{"two ranges", "bytes=0-1,5-6", size, RangeInvalid},
		{"overlapping ranges", "bytes=0-10,5-6", size, RangeInvalid},
		{"many ranges", "bytes=0-1,2-3,4-5,6-7", size, RangeInvalid},
		{"other unit", "items=0-5", size, RangeInvalid},
		{"unit is case sensitive", "Bytes=0-5", size, RangeInvalid},
		{"no unit", "0-5", size, RangeInvalid},
		{"empty specifier", "bytes=", size, RangeInvalid},
		{"no dash", "bytes=5", size, RangeInvalid},
		{"only a dash", "bytes=-", size, RangeInvalid},
		{"zero length suffix", "bytes=-0", size, RangeInvalid},
		{"end before start", "bytes=10-5", size, RangeInvalid},
		{"letters", "bytes=a-b", size, RangeInvalid},
		{"signed start", "bytes=+5-", size, RangeInvalid},
		{"negative number after a dash", "bytes=--5", size, RangeInvalid},
		{"hexadecimal", "bytes=0x10-", size, RangeInvalid},
		{"overflowing number", "bytes=99999999999999999999-", size, RangeInvalid},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := CheckRange(testCase.header, testCase.size); got != testCase.want {
				t.Fatalf("CheckRange(%q, %d) = %d, want %d", testCase.header, testCase.size, got, testCase.want)
			}
		})
	}
}
