// Package opra 解析 OPRA 数据和参数文本，只提供中间结果，不拥有播放区设置合同。
package opra

// Band 是供调用方转换为播放区设置或算法参数的归一解析结果。
type Band struct {
	Type    string  `json:"type"`
	FreqHz  float64 `json:"freq_hz"`
	GainDB  float64 `json:"gain_db,omitempty"`
	Q       float64 `json:"q,omitempty"`
	SlopeDB int     `json:"slope_db,omitempty"`
}

type Vendor struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Product struct {
	ID       string `json:"id"`
	VendorID string `json:"vendor_id"`
	Name     string `json:"name"`
	Subtype  string `json:"subtype"`
}

// SourceBand 保留上游参数，不在裁剪时钳位或截断。
type SourceBand struct {
	Type      string  `json:"type"`
	Frequency float64 `json:"frequency"`
	GainDB    float64 `json:"gain_db,omitempty"`
	Q         float64 `json:"q"`
	Slope     float64 `json:"slope,omitempty"`
}

type Parameters struct {
	GainDB float64      `json:"gain_db"`
	Bands  []SourceBand `json:"bands"`
}

type EQ struct {
	ID         string     `json:"id"`
	ProductID  string     `json:"product_id"`
	Author     string     `json:"author"`
	Details    string     `json:"details,omitempty"`
	Link       string     `json:"link,omitempty"`
	Parameters Parameters `json:"parameters"`
}

type Attribution struct {
	EQID    string
	Vendor  string
	Product string
	Author  string
	Details string
	Link    string
}

// MappedEQ 不包含 enabled/channels 等播放区设置；调用方负责构造完整文档。
type MappedEQ struct {
	PreampDB      float64
	Bands         []Band
	Source        Attribution
	ClampedBands  int
	PreampClamped bool
}

type Counts struct {
	Vendors  int
	Products int
	EQs      int
}

type ProductMatch struct {
	Product Product
	Vendor  Vendor
}
