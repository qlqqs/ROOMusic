package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/devices/upnp"
	"golang.org/x/sys/unix"
)

func ownUDPSockets(t *testing.T) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	sockets := map[string]bool{}
	for _, entry := range entries {
		fd, err := strconv.Atoi(entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		protocol, err := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_PROTOCOL)
		if err == nil && protocol == unix.IPPROTO_UDP {
			target, err := os.Readlink("/proc/self/fd/" + entry.Name())
			if err == nil {
				sockets[target] = true
			}
		}
	}
	return sockets
}

func TestPostgreSQLUPnPDisabledHasNoNetworkActivity(t *testing.T) {
	var hits atomic.Int32
	probe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.NotFound(w, r)
	}))
	defer probe.Close()
	before := ownUDPSockets(t)
	f := newUPnPFixture(t, false)
	assertUPnPSample(t, "settings", f.request(t, "GET", upnpAdmin+"/settings", nil, &f.admin, nil))
	requireAPIError(t, f.request(t, "POST", upnpAdmin+"/devices", map[string]string{"address": probe.URL}, &f.admin, nil), 409, "upnp_disabled")
	if got := f.snapshot(t, &f.admin); len(got.Devices) != 0 || len(got.Zones) != 0 {
		t.Fatalf("关闭时播放区快照 = %+v", got)
	}
	if after := ownUDPSockets(t); !reflect.DeepEqual(before, after) || hits.Load() != 0 {
		t.Fatalf("关闭时 UDP 套接字从 %v 变为 %v，探测 HTTP 请求数 %d", before, after, hits.Load())
	}
}

func TestPostgreSQLCastProtocolStopsDiscovery(t *testing.T) {
	f := newUPnPFixture(t, false)
	before := ownUDPSockets(t)
	assertUPnPSample(t, "settings", f.request(t, "GET", upnpAdmin+"/settings", nil, &f.admin, nil))
	enableCast(t, f)
	if active := ownUDPSockets(t); len(active) <= len(before) {
		t.Fatal("启用 Cast 时没有打开 mDNS 套接字")
	}
	settings := zonesSampleDecode[upnp.Settings](t, f.request(t, "PUT", upnpAdmin+"/settings",
		map[string]bool{"cast_enabled": false}, &f.admin, nil), 200)
	if !settings.Enabled || settings.CastEnabled || settings.UPnPEnabled {
		t.Fatalf("单独关闭 Cast 后的设置 = %+v", settings)
	}
	if after := ownUDPSockets(t); !reflect.DeepEqual(before, after) {
		t.Fatalf("关闭 Cast 后 UDP 套接字从 %v 变为 %v", before, after)
	}
	requireAPIError(t, f.request(t, "POST", upnpAdmin+"/devices",
		map[string]string{"kind": "cast", "address": "192.168.44.2"}, &f.admin, nil), 409, "cast_disabled")
}
