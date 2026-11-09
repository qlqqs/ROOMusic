package opra

// MapEQ 按优先级取前 32 段，映射到解析中间类型；源数据保持不变。
// ClampedBands 对频率、有效增益、Q 中任意一项被钳位的段只计一次。
func (c *Catalog) MapEQ(id string) (MappedEQ, bool) {
	eq, ok := c.eqs[id]
	if !ok {
		return MappedEQ{}, false
	}
	p := c.products[eq.ProductID]
	v := c.vendors[p.VendorID]
	result := MappedEQ{
		PreampDB: clamp(eq.Parameters.GainDB, -30, 30),
		Source:   Attribution{EQID: id, Vendor: v.Name, Product: p.Name, Author: eq.Author, Details: eq.Details, Link: eq.Link},
	}
	result.PreampClamped = result.PreampDB != eq.Parameters.GainDB
	bands := eq.Parameters.Bands
	if len(bands) > 32 {
		bands = bands[:32]
	}
	result.Bands = make([]Band, 0, len(bands))
	for _, raw := range bands {
		band := Band{Type: raw.Type, FreqHz: clamp(raw.Frequency, 10, 40000)}
		changed := band.FreqHz != raw.Frequency
		switch raw.Type {
		case "peak_dip", "low_shelf", "high_shelf":
			if raw.Type == "peak_dip" {
				band.Type = "peaking"
			}
			band.GainDB = clamp(raw.GainDB, -30, 30)
			changed = changed || band.GainDB != raw.GainDB
		case "band_stop":
			band.Type = "notch"
		}
		if raw.Type == "low_pass" || raw.Type == "high_pass" {
			band.SlopeDB = int(raw.Slope)
		} else {
			band.Q = clamp(raw.Q, 0.1, 30)
			changed = changed || band.Q != raw.Q
		}
		if changed {
			result.ClampedBands++
		}
		result.Bands = append(result.Bands, band)
	}
	return result, true
}

func clamp(value, low, high float64) float64 { return min(max(value, low), high) }
