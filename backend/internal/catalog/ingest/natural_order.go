package ingest

import "strings"

// naturalLess 按数字段数值与非数字字节比较路径，不把数字转成可能溢出的整数。
func naturalLess(left, right string) bool {
	for i, j := 0, 0; ; {
		if i == len(left) || j == len(right) {
			return i == len(left) && j < len(right)
		}
		if asciiDigit(left[i]) && asciiDigit(right[j]) {
			li, rj := i, j
			for i < len(left) && asciiDigit(left[i]) {
				i++
			}
			for j < len(right) && asciiDigit(right[j]) {
				j++
			}
			a, b := strings.TrimLeft(left[li:i], "0"), strings.TrimLeft(right[rj:j], "0")
			if len(a) != len(b) {
				return len(a) < len(b)
			}
			if a != b {
				return a < b
			}
			if i-li != j-rj {
				return i-li < j-rj
			}
			continue
		}
		if left[i] != right[j] {
			return left[i] < right[j]
		}
		i++
		j++
	}
}
