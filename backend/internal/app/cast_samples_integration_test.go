package app

import (
	"context"
	"net"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/devices/fakecast"
)

func TestPostgreSQLCastAPISamples(t *testing.T) {
	f := newUPnPFixture(t, false)
	const id = "00112233445566778899aabbccddeeff"
	base := upnpAdmin + "/devices"
	headers := map[string]string{"X-Request-ID": "sample-request"}
	invalidAddress := map[string]string{"kind": "cast", "address": "127.0.0.1"}
	assertUPnPSample(t, "cast-disabled", f.request(t, "POST", base, invalidAddress, &f.admin, headers))
	settings := map[string]bool{"enabled": true, "upnp_enabled": false, "airplay_enabled": false}
	assertUPnPSample(t, "cast-settings", f.request(t, "PUT", upnpAdmin+"/settings", settings, &f.admin, nil))
	assertUPnPSample(t, "cast-invalid-address", f.request(t, "POST", base, invalidAddress, &f.admin, headers))
	listener := lanTestListener(t)
	httpListener := lanTestListener(t)
	address := listener.Addr().String()
	ip, _, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	receiver := fakecast.New(fakecast.Options{ID: id, Name: "客厅 Cast"})
	go func() { done <- receiver.Serve(ctx, listener, httpListener, nil) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	var added *httptest.ResponseRecorder
	f.wait(t, 10*time.Second, "Cast 接收端可登记", func() bool {
		added = f.request(t, "POST", base, map[string]string{"kind": "cast", "address": ip}, &f.admin, nil)
		return added.Code == 200
	})
	assertUPnPSample(t, "cast-manual-added", added, "192.168.44.2:8009", address)
	assertUPnPSample(t, "cast-invalid-format", f.request(t, "PUT", base+"/cast:"+id+"/format", map[string]any{"codec": "mp3", "max_sample_rate": 96000}, &f.admin, headers))
	format := map[string]any{"codec": "flac", "max_sample_rate": 96000}
	assertUPnPSample(t, "cast-format", f.request(t, "PUT", base+"/cast:"+id+"/format", format, &f.admin, nil))
	assertUPnPSample(t, "cast-list", f.request(t, "GET", base, nil, &f.admin, nil), "192.168.44.2:8009", address)
}
