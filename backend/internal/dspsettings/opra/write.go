package opra

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"sort"
)

// WriteJSONL 只输出裁剪字段，全部记录按 ID 排序；参数保持上游数值和段顺序。
func (c *Catalog) WriteJSONL(w io.Writer) error {
	type outputRecord struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Data any    `json:"data"`
	}
	records := make([]outputRecord, 0, len(c.vendors)+len(c.products)+len(c.eqs))
	for id, v := range c.vendors {
		data := struct {
			Name string `json:"name"`
		}{v.Name}
		records = append(records, outputRecord{"vendor", id, data})
	}
	for id, p := range c.products {
		data := struct {
			VendorID string `json:"vendor_id"`
			Name     string `json:"name"`
			Subtype  string `json:"subtype"`
		}{p.VendorID, p.Name, p.Subtype}
		records = append(records, outputRecord{"product", id, data})
	}
	for id, eq := range c.eqs {
		data := struct {
			ProductID  string     `json:"product_id"`
			Author     string     `json:"author"`
			Details    string     `json:"details,omitempty"`
			Link       string     `json:"link,omitempty"`
			Parameters Parameters `json:"parameters"`
		}{eq.ProductID, eq.Author, eq.Details, eq.Link, eq.Parameters}
		records = append(records, outputRecord{"eq", id, data})
	}
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	for _, rec := range records {
		if err := enc.Encode(rec); err != nil {
			return err
		}
	}
	return nil
}

// WriteGzip 使用无时间戳、无文件名的 gzip 头，对同一裁剪内容生成稳定字节。
func (c *Catalog) WriteGzip(w io.Writer) error {
	gz, err := gzip.NewWriterLevel(w, gzip.BestCompression)
	if err != nil {
		return err
	}
	if err := c.WriteJSONL(gz); err != nil {
		_ = gz.Close()
		return err
	}
	return gz.Close()
}
