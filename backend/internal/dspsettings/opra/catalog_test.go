package opra_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/dspsettings/opra"
)

func sampleCatalog(t *testing.T, preamp float64, bands []opra.SourceBand) *opra.Catalog {
	t.Helper()
	data, err := json.Marshal(bands)
	if err != nil {
		t.Fatal(err)
	}
	// 始终只有厂商、产品、EQ 三行；段顺序就是上游优先级。
	text := fmt.Sprintf(`{"type":"vendor","id":"v","data":{"name":"Ａｃｍｅ－ Audio"}}
{"type":"product","id":"p","data":{"vendor_id":"v","name":"ＨＤ－６５０","subtype":"over_the_ear"}}
{"type":"eq","id":"eq","data":{"type":"parametric_eq","product_id":"p","author":"测试作者","details":"测试校正","link":"https://example.com/eq","parameters":{"gain_db":%g,"bands":%s}}}`, preamp, data)
	catalog, err := opra.ParseUpstream(strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func TestMapEQTypesAndClamping(t *testing.T) {
	cases := []struct {
		source opra.SourceBand
		want   opra.Band
	}{
		{opra.SourceBand{Type: "peak_dip", Frequency: 8.5, GainDB: -35, Q: 0.05}, opra.Band{Type: "peaking", FreqHz: 10, GainDB: -30, Q: 0.1}},
		{opra.SourceBand{Type: "low_shelf", Frequency: 45000, GainDB: 35, Q: 35}, opra.Band{Type: "low_shelf", FreqHz: 40000, GainDB: 30, Q: 30}},
		{opra.SourceBand{Type: "high_shelf", Frequency: 8000, GainDB: 3, Q: 0.7}, opra.Band{Type: "high_shelf", FreqHz: 8000, GainDB: 3, Q: 0.7}},
		{opra.SourceBand{Type: "low_pass", Frequency: 9000, Slope: 24}, opra.Band{Type: "low_pass", FreqHz: 9000, SlopeDB: 24}},
		{opra.SourceBand{Type: "high_pass", Frequency: 40, Slope: 12}, opra.Band{Type: "high_pass", FreqHz: 40, SlopeDB: 12}},
		{opra.SourceBand{Type: "band_pass", Frequency: 1000, GainDB: 6, Q: 2}, opra.Band{Type: "band_pass", FreqHz: 1000, Q: 2}},
		{opra.SourceBand{Type: "band_stop", Frequency: 2000, Q: 3}, opra.Band{Type: "notch", FreqHz: 2000, Q: 3}},
	}
	bands := make([]opra.SourceBand, len(cases))
	for i, tc := range cases {
		bands[i] = tc.source
	}
	for _, preamp := range []float64{-35, 35} {
		t.Run(fmt.Sprintf("preamp_%g", preamp), func(t *testing.T) {
			catalog := sampleCatalog(t, preamp, bands)
			mapped, ok := catalog.MapEQ("eq")
			if !ok || len(mapped.Bands) != len(cases) {
				t.Fatalf("映射结果 ok=%v，段数=%d，期望 %d", ok, len(mapped.Bands), len(cases))
			}
			for i, tc := range cases {
				if mapped.Bands[i] != tc.want {
					t.Errorf("%s: 得到 %+v，期望 %+v", tc.source.Type, mapped.Bands[i], tc.want)
				}
			}
			if mapped.PreampDB != preamp*30/35 || !mapped.PreampClamped || mapped.ClampedBands != 2 {
				t.Errorf("钳位结果错误：%+v", mapped)
			}
			wantSource := opra.Attribution{EQID: "eq", Vendor: "Ａｃｍｅ－ Audio", Product: "ＨＤ－６５０", Author: "测试作者", Details: "测试校正", Link: "https://example.com/eq"}
			if mapped.Source != wantSource {
				t.Errorf("署名得到 %+v，期望 %+v", mapped.Source, wantSource)
			}
			raw, ok := catalog.EQ("eq")
			if !ok || raw.Parameters.GainDB != preamp || !reflect.DeepEqual(raw.Parameters.Bands, bands) {
				t.Errorf("原始参数未保留：%+v", raw)
			}
		})
	}
}

func TestMapEQTruncatesByPriority(t *testing.T) {
	bands := make([]opra.SourceBand, 33)
	for i := range bands {
		bands[i] = opra.SourceBand{Type: "peak_dip", Frequency: float64(330 - i*10), Q: 1}
	}
	catalog := sampleCatalog(t, 0, bands)
	mapped, ok := catalog.MapEQ("eq")
	if !ok || len(mapped.Bands) != 32 {
		t.Fatalf("映射结果 ok=%v，段数=%d，期望 32", ok, len(mapped.Bands))
	}
	for i, band := range mapped.Bands {
		want := opra.Band{Type: "peaking", FreqHz: bands[i].Frequency, Q: 1}
		if band != want {
			t.Errorf("第 %d 段得到 %+v，期望 %+v", i+1, band, want)
		}
	}
	raw, ok := catalog.EQ("eq")
	if !ok || !reflect.DeepEqual(raw.Parameters.Bands, bands) {
		t.Errorf("截断改变了源数据：%+v", raw)
	}
}

func TestSearchNormalization(t *testing.T) {
	catalog := sampleCatalog(t, 0, []opra.SourceBand{})
	matches := catalog.Search("  acme AUDIO : hd 650  ")
	if len(matches) != 1 || matches[0].Product.ID != "p" || matches[0].Vendor.ID != "v" {
		t.Fatalf("规范化后应命中产品 p：%+v", matches)
	}
}

func TestSearchOrder(t *testing.T) {
	catalog, err := opra.ParseUpstream(strings.NewReader(`{"type":"vendor","id":"v","data":{"name":"Acme"}}
{"type":"product","id":"a","data":{"vendor_id":"v","name":"X Pro","subtype":"in_ear"}}
{"type":"product","id":"b","data":{"vendor_id":"v","name":"Pro Max","subtype":"in_ear"}}
{"type":"product","id":"c","data":{"vendor_id":"v","name":"Pro","subtype":"in_ear"}}`))
	if err != nil {
		t.Fatal(err)
	}
	matches := catalog.Search("pro")
	ids := make([]string, len(matches))
	for i, match := range matches {
		ids[i] = match.Product.ID
	}
	// 匹配位置优先，同位置再按名字长度；不能直接按 ID 或长度排序。
	if want := []string{"c", "b", "a"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("排序得到 %v，期望 %v", ids, want)
	}
}

func TestCatalogReturnsIndependentSlices(t *testing.T) {
	catalog := sampleCatalog(t, 0, []opra.SourceBand{{Type: "peak_dip", Frequency: 1000, Q: 1}})
	vendors, products, eqs := catalog.Vendors(), catalog.Products("v"), catalog.EQs("p")
	wantVendor, wantProduct, wantEQ := vendors[0], products[0], eqs[0]
	wantEQ.Parameters.Bands = append([]opra.SourceBand(nil), wantEQ.Parameters.Bands...)
	vendors[0].Name = "已修改"
	products[0].Name = "已修改"
	eqs[0].Author = "已修改"
	eqs[0].Parameters.Bands[0].Frequency = 99
	raw, ok := catalog.EQ("eq")
	if !ok || !reflect.DeepEqual(raw, wantEQ) {
		t.Fatalf("修改 EQs 返回值污染目录：%+v", raw)
	}
	raw.Parameters.Bands[0].Q = 9
	matches := catalog.Search("hd650")
	matches[0].Vendor.Name = "已修改"
	matches[0].Product.Name = "已修改"
	mapped, ok := catalog.MapEQ("eq")
	if !ok {
		t.Fatal("缺少 EQ 映射")
	}
	mapped.Bands[0].FreqHz = 99
	if got := catalog.Vendors(); !reflect.DeepEqual(got, []opra.Vendor{wantVendor}) {
		t.Errorf("厂商目录被污染：%+v", got)
	}
	if got := catalog.Products("v"); !reflect.DeepEqual(got, []opra.Product{wantProduct}) {
		t.Errorf("产品目录被污染：%+v", got)
	}
	if got := catalog.EQs("p"); !reflect.DeepEqual(got, []opra.EQ{wantEQ}) {
		t.Errorf("EQ 目录被污染：%+v", got)
	}
}
