package opra_test

import (
	"testing"

	"github.com/qlqq/roomusic/backend/internal/dspsettings/opra"
)

func TestLoadEmbedded(t *testing.T) {
	catalog, err := opra.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	counts := catalog.Counts()
	if counts.Vendors <= 0 || counts.Products <= 0 || counts.EQs <= 0 {
		t.Errorf("嵌入目录三类计数均应大于零：%+v", counts)
	}
}
