package downmix_test

import (
	"testing"

	"github.com/qlqq/roomusic/backend/internal/dsp/downmix"
)

func TestMaskLayout(t *testing.T) {
	for _, tt := range []struct {
		mask     downmix.Mask
		channels int
		name     string
	}{
		{0, 0, "0 声道"},
		{0x4, 1, "1.0"},
		{0x3, 2, "2.0"},
		{0x7, 3, "3.0"},
		{0x107, 4, "4.0"},
		{0x33, 4, "4.0"},
		{0x603, 4, "4.0"},
		{0x607, 5, "5.0"},
		{0x37, 5, "5.0"},
		{0x60f, 6, "5.1"},
		{0x3f, 6, "5.1"},
		{0x70f, 7, "6.1"},
		{0x637, 7, "7.0"},
		{0x63f, 8, "7.1"},
		{0x800008c7, 7, "7 声道"},
	} {
		t.Run(tt.name+"/"+tt.mask.String(), func(t *testing.T) {
			if got := tt.mask.Channels(); got != tt.channels {
				t.Errorf("掩码 %#x：声道数=%d，期望 %d", uint32(tt.mask), got, tt.channels)
			}
			if got := tt.mask.String(); got != tt.name {
				t.Errorf("掩码 %#x：名称=%q，期望 %q", uint32(tt.mask), got, tt.name)
			}
		})
	}
	// 布局数据和矩阵期望均使用协议掩码，另行固定公开位置常量。
	for _, tt := range []struct {
		name string
		got  downmix.Mask
		want downmix.Mask
	}{
		{"FL", downmix.FL, 0x1}, {"FR", downmix.FR, 0x2},
		{"FC", downmix.FC, 0x4}, {"LFE", downmix.LFE, 0x8},
		{"BL", downmix.BL, 0x10}, {"BR", downmix.BR, 0x20},
		{"FLC", downmix.FLC, 0x40}, {"FRC", downmix.FRC, 0x80},
		{"BC", downmix.BC, 0x100}, {"SL", downmix.SL, 0x200},
		{"SR", downmix.SR, 0x400},
		{"Stereo", downmix.Stereo, 0x3}, {"Surround51", downmix.Surround51, 0x60f},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("%s=%#x，期望 %#x", tt.name, uint32(tt.got), uint32(tt.want))
			}
		})
	}
}

func TestTarget(t *testing.T) {
	for _, tt := range []struct {
		name    string
		src     downmix.Mask
		outMax  int
		enabled bool
		want    downmix.Mask
		ok      bool
	}{
		{"关闭", 0x63f, 2, false, 0, false},
		{"声道数相等", 0x63f, 8, true, 0, false},
		{"输出声道更多", 0x60f, 8, true, 0, false},
		{"输出为零", 0x63f, 0, true, 0, false},
		{"单声道输出不混合", 0x63f, 1, true, 0, false},
		{"5.1到2.0", 0x60f, 2, true, 0x3, true},
		{"7.1输出不足六声道", 0x63f, 5, true, 0x3, true},
		{"7.1输出六声道", 0x63f, 6, true, 0x60f, true},
		{"7.1输出七声道", 0x63f, 7, true, 0x60f, true},
		{"6.1到5.1", 0x70f, 6, true, 0x60f, true},
		{"7.0到5.1", 0x637, 6, true, 0x60f, true},
		{"未知布局", 0, 2, true, 0, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dst, ok := downmix.Target(tt.src, tt.outMax, tt.enabled)
			if dst != tt.want || ok != tt.ok {
				t.Errorf("Target=(%#x, %v)，期望 (%#x, %v)", uint32(dst), ok, uint32(tt.want), tt.ok)
			}
		})
	}
}
