package opra

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
)

const maxLineBytes = 1 << 20

type record struct {
	Type string          `json:"type"`
	ID   string          `json:"id"`
	Data json.RawMessage `json:"data"`
}

// ParseUpstream 校验上游 JSONL 的字段及引用，忽略非 parametric_eq 配置。
// 可选说明、链接和未知的介绍/图片字段不影响解析；数值范围在映射时钳位。
func ParseUpstream(r io.Reader) (*Catalog, error) { return parse(r, true) }

// Load 读取 WriteJSONL 产生的裁剪 JSONL；不负责解压。
func Load(r io.Reader) (*Catalog, error) { return parse(r, false) }

func parse(r io.Reader, upstream bool) (*Catalog, error) {
	c := &Catalog{vendors: make(map[string]Vendor), products: make(map[string]Product), eqs: make(map[string]EQ)}
	seen := make(map[string]bool)
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64<<10), maxLineBytes)
	line := 0
	for scanner.Scan() {
		line++
		var rec record
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			return nil, fmt.Errorf("opra: 第 %d 行: %w", line, err)
		}
		if strings.TrimSpace(rec.ID) == "" || seen[rec.ID] {
			return nil, fmt.Errorf("opra: 第 %d 行缺少 ID 或 ID 重复", line)
		}
		seen[rec.ID] = true
		if err := c.add(rec, upstream); err != nil {
			return nil, fmt.Errorf("opra: 第 %d 行 (%s): %w", line, rec.ID, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("opra: 读取 JSONL: %w", err)
	}
	if err := c.index(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Catalog) add(rec record, upstream bool) error {
	obj, err := object(rec.Data)
	if err != nil {
		return err
	}
	switch rec.Type {
	case "vendor":
		if err := requiredStrings(obj, "name"); err != nil {
			return err
		}
		var v Vendor
		if err := json.Unmarshal(rec.Data, &v); err != nil {
			return err
		}
		v.ID = rec.ID
		c.vendors[v.ID] = v
	case "product":
		if err := requiredStrings(obj, "vendor_id", "name", "subtype"); err != nil {
			return err
		}
		var p Product
		if err := json.Unmarshal(rec.Data, &p); err != nil {
			return err
		}
		switch p.Subtype {
		case "over_the_ear", "on_ear", "in_ear", "earbuds":
		default:
			return fmt.Errorf("不支持的产品 subtype %q", p.Subtype)
		}
		p.ID = rec.ID
		c.products[p.ID] = p
	case "eq":
		if upstream {
			if err := requiredStrings(obj, "type"); err != nil {
				return err
			}
			var kind string
			if err := json.Unmarshal(obj["type"], &kind); err != nil {
				return err
			}
			if kind != "parametric_eq" {
				return nil
			}
		}
		if err := requiredStrings(obj, "product_id", "author"); err != nil {
			return err
		}
		for _, name := range []string{"details", "link"} {
			if value, ok := obj[name]; ok {
				var text string
				if isNull(value) || json.Unmarshal(value, &text) != nil {
					return fmt.Errorf("%s 必须为字符串", name)
				}
			}
		}
		if err := validateParameters(obj["parameters"]); err != nil {
			return err
		}
		var eq EQ
		if err := json.Unmarshal(rec.Data, &eq); err != nil {
			return err
		}
		eq.ID = rec.ID
		c.eqs[eq.ID] = eq
	default:
		return fmt.Errorf("不支持的记录类型 %q", rec.Type)
	}
	return nil
}

func validateParameters(raw json.RawMessage) error {
	obj, err := object(raw)
	if err != nil {
		return fmt.Errorf("parameters: %w", err)
	}
	if err := number(obj, "gain_db", true); err != nil {
		return err
	}
	var bands []json.RawMessage
	if isNull(obj["bands"]) || json.Unmarshal(obj["bands"], &bands) != nil {
		return fmt.Errorf("bands 必须为数组")
	}
	for i, rawBand := range bands {
		if err := validateBand(rawBand); err != nil {
			return fmt.Errorf("bands[%d]: %w", i, err)
		}
	}
	return nil
}

func validateBand(raw json.RawMessage) error {
	obj, err := object(raw)
	if err != nil {
		return err
	}
	if err := requiredStrings(obj, "type"); err != nil {
		return err
	}
	if err := number(obj, "frequency", true); err != nil {
		return err
	}
	for _, field := range []string{"gain_db", "q", "slope"} {
		if err := number(obj, field, false); err != nil {
			return err
		}
	}
	var b SourceBand
	if err := json.Unmarshal(raw, &b); err != nil {
		return err
	}
	switch b.Type {
	case "peak_dip", "low_shelf", "high_shelf", "band_pass", "band_stop":
		return number(obj, "q", true)
	case "low_pass", "high_pass":
		if err := number(obj, "slope", true); err != nil {
			return err
		}
		switch b.Slope {
		case 6, 12, 18, 24, 30, 36:
			return nil
		default:
			return fmt.Errorf("不支持的 slope %g", b.Slope)
		}
	default:
		return fmt.Errorf("不支持的滤波器类型 %q", b.Type)
	}
}

func object(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	if obj == nil {
		return nil, fmt.Errorf("必须为对象")
	}
	return obj, nil
}

func requiredStrings(obj map[string]json.RawMessage, names ...string) error {
	for _, name := range names {
		var value string
		if err := json.Unmarshal(obj[name], &value); err != nil || strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s 必须为非空字符串", name)
		}
	}
	return nil
}

func number(obj map[string]json.RawMessage, name string, required bool) error {
	raw, ok := obj[name]
	if !ok && !required {
		return nil
	}
	var value float64
	if isNull(raw) || json.Unmarshal(raw, &value) != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return fmt.Errorf("%s 必须为有限数值", name)
	}
	return nil
}

func isNull(raw json.RawMessage) bool { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }
