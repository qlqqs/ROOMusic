package dspsettings

import (
	"fmt"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/dspsettings/opra"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

type Product struct {
	ProductID string `json:"product_id"`
	Vendor    string `json:"vendor"`
	Name      string `json:"name"`
	Subtype   string `json:"subtype"`
}

type ProductMatch struct {
	Product
	EQCount int `json:"eq_count"`
}

type ProductEQ struct {
	EQID    string                 `json:"eq_id"`
	Author  string                 `json:"author"`
	Details string                 `json:"details"`
	Link    string                 `json:"link"`
	PEQ     zones.SettingsDocument `json:"peq"`
}

type ProductDetails struct {
	Product Product     `json:"product"`
	EQs     []ProductEQ `json:"eqs"`
}

func (s *Service) SearchOPRA(query string) ([]ProductMatch, error) {
	items := make([]ProductMatch, 0)
	if strings.TrimSpace(query) == "" {
		return items, nil
	}
	catalog, err := opra.LoadEmbedded()
	if err != nil {
		return nil, fmt.Errorf("加载 OPRA: %w", err)
	}
	for _, match := range catalog.Search(query) {
		items = append(items, ProductMatch{Product: productSummary(match.Product, match.Vendor), EQCount: len(catalog.EQs(match.Product.ID))})
	}
	return items, nil
}

func (s *Service) OPRAProduct(id string) (ProductDetails, error) {
	catalog, err := opra.LoadEmbedded()
	if err != nil {
		return ProductDetails{}, fmt.Errorf("加载 OPRA: %w", err)
	}
	product, ok := catalog.Product(id)
	if !ok {
		return ProductDetails{}, ErrNotFound
	}
	vendor, _ := catalog.Vendor(product.VendorID)
	result := ProductDetails{Product: productSummary(product, vendor), EQs: []ProductEQ{}}
	for _, eq := range catalog.EQs(id) {
		mapped, ok := catalog.MapEQ(eq.ID)
		if !ok {
			return ProductDetails{}, fmt.Errorf("OPRA 索引不一致")
		}
		source := &zones.PEQSource{Kind: "opra", EQID: mapped.Source.EQID, Vendor: mapped.Source.Vendor, Product: mapped.Source.Product, Author: mapped.Source.Author, Details: mapped.Source.Details, Link: mapped.Source.Link}
		document, err := mappedDocument(mapped.PreampDB, mapped.Bands, source)
		if err != nil {
			return ProductDetails{}, fmt.Errorf("映射 OPRA 文档: %w", err)
		}
		result.EQs = append(result.EQs, ProductEQ{EQID: eq.ID, Author: eq.Author, Details: eq.Details, Link: eq.Link, PEQ: document})
	}
	return result, nil
}

func productSummary(product opra.Product, vendor opra.Vendor) Product {
	return Product{ProductID: product.ID, Vendor: vendor.Name, Name: product.Name, Subtype: product.Subtype}
}

type ImportResult struct {
	PEQ               zones.SettingsDocument `json:"peq"`
	UnrecognizedLines []int                  `json:"unrecognized_lines"`
}

func (s *Service) ImportPEQ(text string) (ImportResult, error) {
	parsed, err := opra.ParseText(text)
	if err != nil {
		return ImportResult{}, err
	}
	document, err := mappedDocument(parsed.PreampDB, parsed.Bands, &zones.PEQSource{Kind: "import"})
	if err != nil {
		return ImportResult{}, ErrInvalidInput
	}
	return ImportResult{PEQ: document, UnrecognizedLines: parsed.UnrecognizedLines}, nil
}

func mappedDocument(preamp float64, bands []opra.Band, source *zones.PEQSource) (zones.SettingsDocument, error) {
	settings := zones.PEQSettings{Enabled: true, PreampDB: preamp, Bands: make([]zones.PEQBand, 0, len(bands)), Source: source}
	for _, band := range bands {
		converted := zones.PEQBand{Type: band.Type, FreqHz: band.FreqHz, Channels: zones.PEQChannels{All: true}, Enabled: true}
		switch band.Type {
		case "peaking", "low_shelf", "high_shelf":
			converted.GainDB, converted.Q = &band.GainDB, &band.Q
		case "low_pass", "high_pass":
			converted.SlopeDB = &band.SlopeDB
			if band.Q != 0 {
				converted.Q = &band.Q
			}
		default:
			converted.Q = &band.Q
		}
		settings.Bands = append(settings.Bands, converted)
	}
	return zones.NewPEQDocument(settings)
}
