package curation

import (
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
)

func TestJudgeNearbyNameClassifications(t *testing.T) {
	if err := textnorm.LoadEmbedded(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, album, externalAlbum, track, externalTrack string
		want                                             bool
	}{
		{"等价", "夜曲．鋼琴篇", "夜曲 钢琴篇", "ＣＡＦÉ!", "cafe", false},
		{"跨文字", "星の窓", "Hoshi no Mado", "夜の街", "Yoru no Machi", false},
		{"只多后缀", "夜幕边缘", "夜幕边缘 (Remastered 2009)", "晚风", "晚风 feat. 合成艺人", false},
		{"少量错字", "枝行谱", "枝航谱", "暮色之弦", "暮色之舷", true},
		{"相差很多", "东京深蓝夜曲", "晚风拂过长河", "微光照亮山谷", "列车掠过荒野", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, v := range [][2]string{{tc.album, tc.externalAlbum}, {tc.track, tc.externalTrack}} {
				for _, pair := range [][2]string{v, {v[1], v[0]}} {
					if got := nearbyName(pair[0], pair[1]); got != tc.want {
						t.Errorf("%q 与 %q 是否送模型=%t，期望 %t", pair[0], pair[1], got, tc.want)
					}
				}
			}
		})
	}
}
