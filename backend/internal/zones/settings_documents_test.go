package zones

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestHeadroomDocumentCanonicalAndShape(t *testing.T) {
	for _, tc := range []struct {
		name      string
		raws      []string
		canonical string
	}{
		{"键序空白数值", []string{`{"enabled":true,"db":-3,"clip_indicator":false}`, ` { "clip_indicator" : false, "db": -3.0, "enabled":true } `, `{"db":-30e-1,"enabled":true,"clip_indicator":false}`}, `{"enabled":true,"db":-3,"clip_indicator":false}`},
		{"正负零", []string{`{"enabled":false,"db":0,"clip_indicator":false}`, `{"db":-0.0,"clip_indicator":false,"enabled":false}`}, `{"enabled":false,"db":0,"clip_indicator":false}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var first SettingsDocument
			for i, raw := range tc.raws {
				d, err := ParseHeadroomDocument([]byte(raw))
				if err != nil {
					t.Fatal(err)
				}
				if i == 0 {
					first = d
				} else if d != first {
					t.Fatal("等价文档不相等")
				}
				got, err := json.Marshal(d)
				if err != nil || string(got) != tc.canonical {
					t.Fatalf("规范 JSON=%s，错误=%v", got, err)
				}
			}
		})
	}
	for _, raw := range []string{`{"enabled":false,"db":-3}`, `{"enabled":false,"db":-3,"clip_indicator":false,"extra":1}`, `null`, `{"enabled":null,"db":-3,"clip_indicator":false}`, `{"enabled":false,"db":null,"clip_indicator":false}`, `{"enabled":false,"db":-3,"clip_indicator":null}`} {
		if _, err := ParseHeadroomDocument([]byte(raw)); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("未拒绝不完整对象 %s：%v", raw, err)
		}
		if err := ValidateSettingsShape([]byte(`{"headroom":` + raw + `}`)); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("HTTP 形状未拒绝 %s：%v", raw, err)
		}
	}
}

func TestSettingsDocumentIndependentDecode(t *testing.T) {
	// 只在测试文件定义嵌套文档，不提前注册未来算法设置键。
	type config struct {
		Bands []float64 `json:"bands"`
		Rate  *int      `json:"rate"`
	}
	rate := 48000
	original := config{Bands: []float64{1, 2}, Rate: &rate}
	d, err := newSettingsDocument(original, func(c config) (config, error) { return c, nil })
	if err != nil {
		t.Fatal(err)
	}
	original.Bands[0] = 99
	rate = 1
	a, err := DecodeSettingsDocument[config](d)
	if err != nil {
		t.Fatal(err)
	}
	b, err := DecodeSettingsDocument[config](d)
	if err != nil {
		t.Fatal(err)
	}
	if a.Bands[0] != 1 || *a.Rate != 48000 {
		t.Fatal("构造后仍引用输入")
	}
	a.Bands[0] = 88
	*a.Rate = 2
	if b.Bands[0] != 1 || *b.Rate != 48000 {
		t.Fatal("两次解码共享底层数据")
	}
}
