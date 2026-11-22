package catalog

import (
	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
	"slices"
	"testing"
)

func TestCurationEvidenceMCN(t *testing.T) {
	if err := textnorm.LoadEmbedded(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		number, barcode string
		cue             bool
		catalog         string
		barcodes        []string
	}{
		{"123456789012", "", false, "", []string{"123456789012"}},
		{"0123456789012", "999", false, "", []string{"999", "0123456789012"}},
		{"123", "", true, "", []string{"123"}},
		{"123", "", false, "123", nil},
		{"0000000000000", "", true, "", nil},
		{"CAT-01", "", true, "CAT-01", nil},
		{"123456789012", "123456789012", true, "", []string{"123456789012"}},
	} {
		e := CurationEvidence{CatalogNumber: tc.number}
		e.setCurationBarcodes(tc.barcode, tc.cue)
		if e.CatalogNumber != tc.catalog || !slices.Equal(e.Barcodes, tc.barcodes) {
			t.Errorf("%+v 得到编号 %q、条码 %v", tc, e.CatalogNumber, e.Barcodes)
		}
	}
}
