package postgres

import (
	"testing"

	"github.com/qlqq/roomusic/backend/migrations"
)

func TestEmbeddedMigrationsIncludeCatalogSearchAfterScanFeedback(t *testing.T) {
	available, err := loadMigrations(migrations.Files)
	if err != nil {
		t.Fatalf("内置迁移必须连续且无重复：%v", err)
	}
	if len(available) < 25 {
		t.Fatalf("内置迁移缺少搜索版本25：共%d项", len(available))
	}
	for i, migration := range available {
		if migration.Version != int64(i+1) {
			t.Fatalf("迁移不连续：位置%d，版本%d", i, migration.Version)
		}
	}
	if available[24].Name != "0025_catalog_search.sql" {
		t.Fatalf("搜索迁移必须在P/F之后：%s", available[24].Name)
	}
}
