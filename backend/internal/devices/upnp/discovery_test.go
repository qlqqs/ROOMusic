package upnp

import (
	"context"
	"net/netip"
	"testing"
	"testing/synctest"
	"time"
)

const testRendererTarget = "urn:schemas-upnp-org:device:MediaRenderer:1"

func TestSSDPSearchPacket(t *testing.T) {
	want := "M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nMAN: \"ssdp:discover\"\r\nMX: 2\r\nST: " + testRendererTarget + "\r\n\r\n"
	if got := string(searchPacket(ssdpGroup)); got != want {
		t.Fatalf("M-SEARCH = %q", got)
	}
}

func TestSSDPAnnouncements(t *testing.T) {
	now := time.Now()
	ip := netip.MustParseAddr("192.168.10.2")
	location := "http://192.168.10.2:8080/description.xml"
	tail := "USN: uuid:roomusic-test::" + testRendererTarget + "\r\n"
	alive := "NOTIFY * HTTP/1.1\r\nNT: " + testRendererTarget + "\r\nNTS: ssdp:alive\r\n" + tail
	reply := "HTTP/1.1 200 OK\r\nST: " + testRendererTarget + "\r\n" + tail
	meta := "LOCATION: " + location + "\r\nCache-Control: MAX-AGE=\"3\"\r\n\r\n"
	for _, tc := range []struct {
		name, packet    string
		notify, ok, bye bool
	}{
		{"reply", reply + meta, false, true, false},
		{"alive", alive + meta, true, true, false},
		{"byebye", "NOTIFY * HTTP/1.1\r\nNT: " + testRendererTarget + "\r\nNTS: ssdp:byebye\r\n" + tail + "\r\n", true, true, true},
		{"no expiry", reply + "LOCATION: " + location + "\r\n\r\n", false, false, false},
		{"invalid expiry", reply + "LOCATION: " + location + "\r\nCache-Control: max-age=-1\r\n\r\n", false, false, false},
		{"wrong message type", alive + meta, false, false, false},
		{"wrong device type", "HTTP/1.1 200 OK\r\nST: upnp:rootdevice\r\n" + tail + meta, false, false, false},
		{"other host", reply + "LOCATION: http://192.168.10.3/description.xml\r\nCache-Control: max-age=3\r\n\r\n", false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseSSDP([]byte(tc.packet), ip, tc.notify, now)
			if ok != tc.ok {
				t.Fatalf("解析结果 = %+v, %v", got, ok)
			}
			if ok && (got.udn != "uuid:roomusic-test" || got.source != ip || got.byebye != tc.bye ||
				(!tc.bye && (got.location != location || !got.expires.Equal(now.Add(3*time.Second))))) {
				t.Fatalf("公告内容 = %+v", got)
			}
		})
	}
}

func TestRegistryExpiryAndByebye(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := NewRegistry()
		run := &discovery{ctx: t.Context(), jobs: make(chan *refresh, 4), pending: make(map[string]*refresh)}
		ip := netip.MustParseAddr("192.168.10.2")
		a := announcement{udn: "uuid:roomusic-test", source: ip, location: "http://192.168.10.2/d", expires: time.Now().Add(time.Second)}
		d := Device{UDN: a.udn, Name: "客厅", Address: a.location, peerIP: ip}
		assertOnline := func(want bool) {
			t.Helper()
			if entry, _ := r.Lookup(a.udn); entry.Online != want {
				t.Fatalf("设备在线 = %v，期望 %v", entry.Online, want)
			}
		}
		r.observe(run, a)
		r.applyRefresh(run, <-run.jobs, d, nil)
		assertOnline(true)
		time.Sleep(time.Second)
		assertOnline(false)

		a.expires = time.Now().Add(time.Minute)
		r.observe(run, a)
		r.applyRefresh(run, <-run.jobs, d, nil)
		assertOnline(true)
		time.Sleep(searchInterval)
		r.observe(run, a)
		inFlight := <-run.jobs
		r.observe(run, announcement{udn: a.udn, source: ip, byebye: true})
		assertOnline(false)
		r.applyRefresh(run, inFlight, d, nil)
		assertOnline(false)

		a.expires = time.Now().Add(time.Minute)
		r.observe(run, a)
		r.applyRefresh(run, <-run.jobs, d, nil)
		assertOnline(true)
	})
}

func TestRegistryPeriodicallyReprobesManualDevice(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		httpServer := fakeDescriptionTransport(t, nil)
		r := NewRegistry()
		ctx, cancel := context.WithCancel(t.Context())
		run := &discovery{ctx: ctx, cancel: cancel, done: make(chan struct{}), jobs: make(chan *refresh, 4), pending: make(map[string]*refresh)}
		a := "http://192.168.10.2/description.xml"
		r.run = run
		r.records[testUDN] = &record{manualAddress: a}
		go r.discover(run, nil)
		defer r.Stop()
		synctest.Wait()
		if got, found := r.Lookup(testUDN); !found || !got.Online || !got.Manual {
			t.Fatalf("首轮探测后设备 = %+v", got)
		}
		httpServer.offline.Store(true)
		time.Sleep(searchInterval)
		synctest.Wait()
		if got, _ := r.Lookup(testUDN); got.Online {
			t.Fatalf("周期探测失败后设备仍在线: %+v", got)
		}
		httpServer.offline.Store(false)
		time.Sleep(searchInterval)
		synctest.Wait()
		if got, _ := r.Lookup(testUDN); !got.Online {
			t.Fatalf("周期探测恢复后设备 = %+v", got)
		}
	})
}
