package dspsettings

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"math"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/dspsettings/irload"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/zones"
	"github.com/qlqq/roomusic/backend/internal/zones/queue"
)

// 装配真实资源服务，并在事务边界观察缓存；不以假缓存代替实际失效。
type deletionIRReferences struct {
	service          *Service
	beforeCommit     func()
	beforeInvalidate func()
}

func (r deletionIRReferences) Check(ctx context.Context, tx *sql.Tx, user, id string) error {
	return r.service.CheckIR(ctx, tx, user, id)
}
func (r deletionIRReferences) Delete(ctx context.Context, tx *sql.Tx, user, id string) error {
	if err := r.service.DeleteIR(ctx, tx, user, id); err != nil {
		return err
	}
	r.beforeCommit()
	return nil
}
func (r deletionIRReferences) Invalidate(user, id string) {
	r.beforeInvalidate()
	r.service.InvalidateIR(user, id)
}

func TestPostgreSQLConvolutionDeleteClearsDatabaseCacheAndRuntime(t *testing.T) {
	ctx := context.Background()
	db, err := postgres.Open(ctx, postgrestest.NewSchema(t, "roomusic_ir_delete_"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	user := uuid.New()
	if _, err := db.Exec(`INSERT INTO users(id,username,password_hash,role) VALUES($1,$2,'test-only','user')`, user, "ir-"+user); err != nil {
		t.Fatal(err)
	}
	resources := NewService(db)
	resources.ConfigureIR(irload.NewLoader(nil))
	var data bytes.Buffer
	writer := zip.NewWriter(&data)
	impulse := make([]byte, 4)
	binary.LittleEndian.PutUint32(impulse, math.Float32bits(1))
	for _, entry := range []struct {
		name string
		data []byte
	}{
		{"impulse.cfg", []byte("48000 1 1 1\n0\n0\nimpulse.pcm\n0\n0.0\n0.0\n")},
		{"impulse.pcm", impulse},
	} {
		part, err := writer.Create(entry.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	ir, err := resources.UploadIR(ctx, user, "删除测试", "impulse.zip", &data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resources.LoadIR(ctx, user, ir.ID, 48000, 2, "3"); err != nil {
		t.Fatal(err)
	}
	cacheCount := func() int {
		resources.irMu.Lock()
		defer resources.irMu.Unlock()
		return len(resources.irCache)
	}
	if cacheCount() != 1 {
		t.Fatal("测试未建立真实 IR 缓存")
	}
	assertDatabase := func(wantFiles, wantReferences int) {
		t.Helper()
		var files, references int
		// 独立连接只观察已提交状态，不读取删除事务里的中间值。
		if err := db.QueryRow(`SELECT (SELECT count(*) FROM dsp_ir_files WHERE id=$1),
			(SELECT count(*) FROM playback_zones WHERE convolution->>'ir_id'=$1::text)`, ir.ID).Scan(&files, &references); err != nil {
			t.Fatal(err)
		}
		if files != wantFiles || references != wantReferences {
			t.Fatalf("已提交状态：IR=%d 引用=%d，期望 %d/%d", files, references, wantFiles, wantReferences)
		}
	}
	invalidated := false
	refs := deletionIRReferences{service: resources,
		beforeCommit: func() {
			assertDatabase(1, 2)
			if cacheCount() != 1 {
				t.Fatal("事务提交前清除了缓存")
			}
		},
		beforeInvalidate: func() {
			assertDatabase(0, 0)
			invalidated = true
		},
	}
	service := zones.NewService(db, zones.Options{IRReferences: refs})
	t.Cleanup(service.Close)
	document, err := zones.NewConvolutionDocument(zones.ConvolutionSettings{Enabled: true, IRID: &ir.ID, BeforePEQ: true})
	if err != nil {
		t.Fatal(err)
	}
	devices := make([]zones.RegisterDeviceResult, 2)
	for i := range devices {
		devices[i], err = service.RegisterDevice(ctx, user, zones.RegisterDeviceInput{Name: "测试设备", Kind: "browser", Volume: 0.5, Settings: zones.SettingsPatch{Convolution: &document}})
		if err != nil {
			t.Fatal(err)
		}
	}
	// 同时保留一个在线区和一个离线区，删除必须同步两者的运行态。
	if _, err := service.SetOutputState(ctx, user, devices[0].Device.DeviceID, zones.OutputState{Online: true}); err != nil {
		t.Fatal(err)
	}
	before, err := service.Snapshot(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	assertDatabase(1, 2)
	if err := service.DeleteIR(ctx, user, ir.ID); err != nil {
		t.Fatal(err)
	}
	assertDatabase(0, 0)
	if !invalidated || cacheCount() != 0 {
		t.Fatal("提交后未清除实际 IR 缓存")
	}
	if _, err := resources.LoadIR(ctx, user, ir.ID, 48000, 2, "3"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删除后仍可装载旧 IR：%v", err)
	}
	assertCleared := func(document zones.SettingsDocument) {
		t.Helper()
		settings, err := document.DecodeConvolution()
		if err != nil {
			t.Fatal(err)
		}
		if settings.Enabled || settings.IRID != nil || !settings.BeforePEQ {
			t.Fatalf("卷积引用未清理或顺序丢失：%+v", settings)
		}
	}
	snapshot, err := service.Snapshot(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Zones) != 2 {
		t.Fatalf("播放区数=%d", len(snapshot.Zones))
	}
	for _, zone := range snapshot.Zones {
		assertCleared(zone.Settings.Convolution)
		for _, previous := range before.Zones {
			if previous.ZoneID == zone.ZoneID && zone.Revision != previous.Revision+1 {
				t.Fatal("删除未使播放区修订号恰增一次")
			}
		}
	}
	for _, device := range devices {
		result, err := service.Command(ctx, user, device.Zone.ZoneID, zones.Command{Command: "set_mode", Mode: queue.ModeRepeatAll})
		if err != nil {
			t.Fatal(err)
		}
		assertCleared(result.Zone.Settings.Convolution)
		var raw []byte
		if err := db.QueryRow(`SELECT convolution FROM playback_zones WHERE id=$1`, device.Zone.ZoneID).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		stored, err := zones.ParseConvolutionDocument(raw)
		if err != nil {
			t.Fatal(err)
		}
		assertCleared(stored)
	}
	assertDatabase(0, 0)
}
