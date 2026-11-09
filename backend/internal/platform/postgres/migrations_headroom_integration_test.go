package postgres

import (
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/migrations"
)

func TestPostgreSQLMigration0044Headroom(t *testing.T) {
	db := openMigrationTestDatabase(t)
	available, err := loadMigrations(migrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	rollbackApply(t, db, migrationPrefix(available, 43))
	userID, deviceID, zoneID := uuid.New(), uuid.New(), uuid.New()
	rollbackExec(t, db, `INSERT INTO users(id,username,password_hash) VALUES($1,'headroom-migration','test-only')`, userID)
	rollbackExec(t, db, `INSERT INTO playback_devices(id,user_id,name) VALUES($1,$2,'迁移前设备')`, deviceID, userID)
	rollbackExec(t, db, `INSERT INTO playback_zones(id,user_id,device_id,leveling) VALUES($1,$2,$3,'off')`, zoneID, userID, deviceID)

	rollbackApply(t, db, migrationPrefix(available, 44))
	var matches bool
	if err := db.QueryRow(`SELECT headroom = '{"enabled":false,"db":-3.0,"clip_indicator":false}'::jsonb
 FROM playback_zones WHERE id=$1`, zoneID).Scan(&matches); err != nil || !matches {
		t.Fatalf("已有播放区没有回填默认 Headroom：matches=%v err=%v", matches, err)
	}
	// 非默认新设置随列回退丢弃，但不能影响升级前已有的播放区。
	rollbackExec(t, db, `UPDATE playback_zones SET headroom='{"enabled":true,"db":-3.1,"clip_indicator":true}' WHERE id=$1`, zoneID)
	rollbackApply(t, db, migrationPrefix(available, 43))
	var columnExists bool
	if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM information_schema.columns
 WHERE table_schema=current_schema() AND table_name='playback_zones' AND column_name='headroom')`).Scan(&columnExists); err != nil || columnExists {
		t.Fatalf("回退后 headroom 列仍存在：exists=%v err=%v", columnExists, err)
	}
	var leveling string
	if err := db.QueryRow(`SELECT leveling FROM playback_zones WHERE id=$1`, zoneID).Scan(&leveling); err != nil || leveling != "off" {
		t.Fatalf("回退改变了已有播放区：leveling=%q err=%v", leveling, err)
	}
}
