package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/platform/config"
)

func TestPostgreSQLSearchMaintenanceLifecycleKeepsReady(t *testing.T) {
	fixture := newIntegrationTestApplication(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	lease, err := fixture.database.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	// 模拟同一 schema 的另一进程已拥有维护锁；保存实际键用于解锁。
	var leaseKey int32
	if err = lease.QueryRowContext(ctx, `SELECT hashtext(current_schema())`).Scan(&leaseKey); err != nil {
		t.Fatal(err)
	}
	// 正常应用应立即能服务，不能在装配时等回填。
	if _, err = lease.ExecContext(ctx, `SELECT pg_advisory_lock($1::integer,$2::integer)`, 0x4341544d, leaseKey); err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			_, _ = lease.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1::integer,$2::integer)`, 0x4341544d, leaseKey)
		}
	}()
	life, stop := context.WithCancel(ctx)
	application := New(life, config.Server{AllowedLibraryRoots: []string{fixture.allowedRoot}, DataDirectory: fixture.dataDirectory}, fixture.database, testFrontend(), nil)
	defer stop()
	response := httptest.NewRecorder()
	application.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("未回填不能阻断ready：%d %s", response.Code, response.Body.String())
	}
	state, err := catalog.NewStore(fixture.database).LoadCatalogState(ctx)
	if err != nil || state.SearchMode != "legacy" {
		t.Fatalf("未回填状态：%+v %v", state, err)
	}
	stop()
	if err = application.WaitForCatalogMaintenance(ctx); err != nil {
		t.Fatalf("取消后维护者未退出：%v", err)
	}
	if _, err = lease.ExecContext(ctx, `SELECT pg_advisory_unlock($1::integer,$2::integer)`, 0x4341544d, leaseKey); err != nil {
		t.Fatal(err)
	}
	locked = false
	// 正常装配再次启动，空库也必须自动完成发现/原子发布，并能等待worker退出。
	application = New(ctx, config.Server{AllowedLibraryRoots: []string{fixture.allowedRoot}, DataDirectory: fixture.dataDirectory}, fixture.database, testFrontend(), nil)
	if err = application.WaitForCatalogMaintenance(ctx); err != nil {
		t.Fatalf("正常装配未启动维护：%v", err)
	}
	state, err = catalog.NewStore(fixture.database).LoadCatalogState(ctx)
	if err != nil || state.SearchMode != "normalized" {
		t.Fatalf("后台发布状态：%+v %v", state, err)
	}
}
