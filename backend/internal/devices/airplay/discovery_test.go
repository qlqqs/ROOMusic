package airplay

import (
	"errors"
	"maps"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/devices/mdns"
	"github.com/qlqq/roomusic/backend/internal/ptp"
)

func nativeAnnouncement() mdns.Entry {
	return mdns.Entry{
		Service: airplayService, Instance: " 合成音箱 ", IP: netip.MustParseAddr("192.168.44.2"),
		LocalIP: netip.MustParseAddr("192.168.44.3"), Port: 7000, Expires: time.Now().Add(time.Minute),
		TXT: map[string]string{"deviceid": "020000000001", "features": "0x1000,0X200", "flags": "0x8",
			"pk": strings.Repeat("AB", 32)},
	}
}

func TestAirPlayDiscoveryMergesAndExpiresAnnouncements(t *testing.T) {
	native := nativeAnnouncement()
	classic := native
	classic.Service, classic.Instance, classic.Port = raopService, "020000000001@旧名称", 5000
	classic.TXT = map[string]string{"am": "Synthetic", "ft": "0x4000,0x80000", "sf": "0x200", "et": "0,1"}
	r := NewRegistry()
	r.receive(native)
	rows := r.List()
	if len(rows) != 1 {
		t.Fatalf("原生公告数量 = %d", len(rows))
	}
	d := rows[0]
	if !d.Online || !d.FairPlay || !d.Unsupported || !d.RequiresPIN || d.RequiresPassword ||
		d.Features != 1<<41|1<<12 || d.Flags != 8 || d.PublicKey != strings.Repeat("ab", 32) {
		t.Fatalf("原生公告 = %+v", d)
	}
	r.receive(classic)
	rows = r.List()
	if len(rows) != 1 {
		t.Fatalf("同一物理设备出现在 %d 行", len(rows))
	}
	d = rows[0]
	if !d.Online || d.Key != "airplay:02:00:00:00:00:01" || d.Name != "合成音箱" || d.Model != "Synthetic" ||
		d.AirPlayAddress != "192.168.44.2:7000" || d.RAOPAddress != "192.168.44.2:5000" ||
		d.Address != d.AirPlayAddress || d.LocalAddress != "192.168.44.3" || d.RAOPEncryption != "0,1" ||
		d.Features != 1<<51|1<<41|1<<14|1<<12 || d.Flags != 0x208 || d.Unsupported || !d.FairPlay {
		t.Fatalf("合并公告 = %+v", d)
	}
	native.Expires = time.Now().Add(-time.Second)
	r.receive(native)
	if got, _ := r.Lookup(d.Key); !got.Online || got.AirPlayAddress != "" || got.Address != d.RAOPAddress {
		t.Fatalf("原生下线后的公告 = %+v", got)
	}
	classic.Expires = native.Expires
	r.receive(classic)
	if got, _ := r.Lookup(d.Key); got.Online {
		t.Fatalf("两种公告均下线后的设备 = %+v", got)
	}
}

func TestAirPlayDiscoveryValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*mdns.Entry)
	}{
		{"missing id", func(e *mdns.Entry) { delete(e.TXT, "deviceid") }},
		{"extra word", func(e *mdns.Entry) { e.TXT["features"] = "1,2,3" }},
		{"overflowing word", func(e *mdns.Entry) { e.TXT["features"] = "100000000,0" }},
		{"bad flags", func(e *mdns.Entry) { e.TXT["flags"] = "-1" }},
		{"short key", func(e *mdns.Entry) { e.TXT["pk"] = "ab" }},
		{"invalid name", func(e *mdns.Entry) { e.Instance = "bad\x00name" }},
		{"invalid UTF-8", func(e *mdns.Entry) { e.TXT["model"] = "\xff" }},
		{"zero port", func(e *mdns.Entry) { e.Port = 0 }},
		{"public", func(e *mdns.Entry) { e.IP = netip.MustParseAddr("8.8.8.8") }},
		{"loopback", func(e *mdns.Entry) { e.IP = netip.MustParseAddr("127.0.0.1") }},
		{"IPv6", func(e *mdns.Entry) { e.IP = netip.MustParseAddr("fd00::2") }},
		{"RAOP mismatched id", func(e *mdns.Entry) { e.Service, e.Instance = raopService, "020000000002@音箱" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := nativeAnnouncement()
			tc.edit(&e)
			if got, ok := parseEntry(e); ok {
				t.Fatalf("接受了畸形公告 %+v", got)
			}
		})
	}
	for _, address := range []string{"8.8.8.8", "127.0.0.1", "fd00::2", "example.invalid", "192.168.44.2:7000"} {
		if _, err := Probe(t.Context(), address); !errors.Is(err, ErrAddress) {
			t.Errorf("Probe(%q) 错误 = %v", address, err)
		}
	}
}

func TestAirPlayAuthenticationAndReceiverTiming(t *testing.T) {
	for _, tc := range []struct {
		model, flags, features               string
		pin, password, fairPlay, unsupported bool
	}{
		{"AppleTV14,1", "0", "0", true, false, false, false},
		{"Synthetic", "0x200", "0", true, false, false, false},
		{"Synthetic", "0x80", "0", false, true, false, false},
		{"Synthetic", "0", "0x4000", false, false, true, true},
		{"Synthetic", "0", "0,0x80000", false, false, true, true},
		{"Synthetic", "0", "0x1000,0x800", false, false, true, false},
		{"Synthetic", "0", "0x4000,0x4000", false, false, true, false},
	} {
		e := nativeAnnouncement()
		maps.Copy(e.TXT, map[string]string{"model": tc.model, "flags": tc.flags, "features": tc.features})
		d, ok := parseEntry(e)
		if !ok || d.RequiresPIN != tc.pin || d.RequiresPassword != tc.password || d.FairPlay != tc.fairPlay || d.Unsupported != tc.unsupported {
			t.Errorf("公告 %s/%s/%s = %+v", tc.model, tc.flags, tc.features, d.Info)
		}
	}
	for _, tc := range []struct {
		model    string
		required bool
	}{
		{"AudioAccessory5,1", true}, {" HomePod mini ", true}, {"Era 100", true}, {"Sonos Era 300", true},
		{"Sonos Roam", false}, {"Mac14,2", false}, {"AppleTV14,1", false}, {"ShairportSync", false},
	} {
		t.Run(tc.model, func(t *testing.T) {
			info := Info{AirPlayAddress: "192.168.44.2:7000", Features: 1 << 41}
			info.ApplyTiming(tc.model, func() ptp.Status { return ptp.Status{Reason: "port_in_use"} })
			if info.PTPRequired != tc.required || info.Unsupported != tc.required || info.TimingReason != "port_in_use" {
				t.Fatalf("时钟端口占用 = %+v", info)
			}
			if tc.required && (info.TimingProtocol != "" || info.UnavailableReason != PrecisionUnavailable) ||
				!tc.required && (info.TimingProtocol != "NTP" || info.UnavailableReason != "") {
				t.Fatalf("回退策略 = %+v", info)
			}
			info.ApplyTiming(tc.model, func() ptp.Status { return ptp.Status{Available: true, ClockID: 1, GrandmasterID: 1} })
			if info.Unsupported || info.TimingProtocol != "PTP" || info.UnavailableReason != "" || info.TimingReason != "" {
				t.Fatalf("时钟恢复 = %+v", info)
			}
		})
	}
}
