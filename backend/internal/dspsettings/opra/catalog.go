package opra

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Catalog 解析完成后只读；查询返回值副本，可并发使用。
type Catalog struct {
	vendors          map[string]Vendor
	products         map[string]Product
	eqs              map[string]EQ
	vendorIDs        []string
	productsByVendor map[string][]string
	eqsByProduct     map[string][]string
	search           []searchEntry
}

type searchEntry struct {
	id     string
	key    string
	length int
}

func (c *Catalog) index() error {
	c.productsByVendor = make(map[string][]string)
	c.eqsByProduct = make(map[string][]string)
	for id := range c.vendors {
		c.vendorIDs = append(c.vendorIDs, id)
	}
	sort.Strings(c.vendorIDs)
	for id, p := range c.products {
		v, ok := c.vendors[p.VendorID]
		if !ok {
			return fmt.Errorf("opra: 产品 %s 引用了不存在的厂商 %s", id, p.VendorID)
		}
		c.productsByVendor[p.VendorID] = append(c.productsByVendor[p.VendorID], id)
		key := normalize(v.Name + " " + p.Name)
		c.search = append(c.search, searchEntry{id: id, key: key, length: utf8.RuneCountInString(key)})
	}
	for id, eq := range c.eqs {
		if _, ok := c.products[eq.ProductID]; !ok {
			return fmt.Errorf("opra: EQ %s 引用了不存在的产品 %s", id, eq.ProductID)
		}
		c.eqsByProduct[eq.ProductID] = append(c.eqsByProduct[eq.ProductID], id)
	}
	for _, ids := range c.productsByVendor {
		sort.Strings(ids)
	}
	for _, ids := range c.eqsByProduct {
		sort.Strings(ids)
	}
	sort.Slice(c.search, func(i, j int) bool { return c.search[i].id < c.search[j].id })
	return nil
}

func (c *Catalog) Counts() Counts {
	return Counts{Vendors: len(c.vendors), Products: len(c.products), EQs: len(c.eqs)}
}

func (c *Catalog) Vendor(id string) (Vendor, bool) {
	v, ok := c.vendors[id]
	return v, ok
}

func (c *Catalog) Product(id string) (Product, bool) {
	p, ok := c.products[id]
	return p, ok
}

func (c *Catalog) EQ(id string) (EQ, bool) {
	eq, ok := c.eqs[id]
	if ok {
		eq = cloneEQ(eq)
	}
	return eq, ok
}

func (c *Catalog) Vendors() []Vendor {
	result := make([]Vendor, 0, len(c.vendorIDs))
	for _, id := range c.vendorIDs {
		result = append(result, c.vendors[id])
	}
	return result
}

func (c *Catalog) Products(vendorID string) []Product {
	ids := c.productsByVendor[vendorID]
	result := make([]Product, 0, len(ids))
	for _, id := range ids {
		result = append(result, c.products[id])
	}
	return result
}

func (c *Catalog) EQs(productID string) []EQ {
	ids := c.eqsByProduct[productID]
	result := make([]EQ, 0, len(ids))
	for _, id := range ids {
		result = append(result, cloneEQ(c.eqs[id]))
	}
	return result
}

func cloneEQ(eq EQ) EQ {
	eq.Parameters.Bands = append([]SourceBand{}, eq.Parameters.Bands...)
	return eq
}

// Search 对厂商名与产品名的规范化拼接做子串匹配，按位置、长度排序，最多返回 30 项。
func (c *Catalog) Search(query string) []ProductMatch {
	query = normalize(query)
	type match struct {
		entry    searchEntry
		position int
	}
	matches := make([]match, 0)
	for _, entry := range c.search {
		if pos := strings.Index(entry.key, query); pos >= 0 {
			matches = append(matches, match{entry: entry, position: utf8.RuneCountInString(entry.key[:pos])})
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		a, b := matches[i], matches[j]
		if a.position != b.position {
			return a.position < b.position
		}
		if a.entry.length != b.entry.length {
			return a.entry.length < b.entry.length
		}
		if a.entry.key != b.entry.key {
			return a.entry.key < b.entry.key
		}
		return a.entry.id < b.entry.id
	})
	if len(matches) > 30 {
		matches = matches[:30]
	}
	result := make([]ProductMatch, 0, len(matches))
	for _, m := range matches {
		p := c.products[m.entry.id]
		result = append(result, ProductMatch{Product: p, Vendor: c.vendors[p.VendorID]})
	}
	return result
}

func normalize(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '\uff01' && r <= '\uff5e' {
			r -= 0xfee0
		}
		if unicode.IsSpace(r) || unicode.IsPunct(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, s)
}
