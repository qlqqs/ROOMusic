package ptp

import (
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestPTPLocalStatusInterface(t *testing.T) {
	peer, unknown := netip.MustParseAddr("192.168.44.2"), netip.MustParseAddr("192.168.44.3")
	s := &state{}
	master := Status{Available: true, Role: "master", ClockID: 17, GrandmasterID: 17}
	follower := Status{Available: true, Role: "follower", ClockID: 17, GrandmasterID: 28, OffsetNS: -14590}
	s.publish(master, map[netip.Addr]Status{peer: follower})
	server := httptest.NewServer(s)
	defer server.Close()
	client := Client{Address: strings.TrimPrefix(server.URL, "http://")}
	if got, err := client.Status(t.Context()); err != nil || got != master {
		t.Fatalf("本机时钟 = %+v，错误 %v", got, err)
	}
	if got, err := client.PeerStatus(t.Context(), peer); err != nil || got != follower {
		t.Fatalf("接收端时钟 = %+v，错误 %v", got, err)
	}
	if got, err := client.PeerStatus(t.Context(), unknown); err != nil || got.Available || got.Reason != "peer_unknown" || got.ClockID != 17 {
		t.Fatalf("未知对端时钟 = %+v，错误 %v", got, err)
	}
	for _, tc := range []struct {
		method, path string
		code         int
	}{
		{"POST", "/status", 405}, {"GET", "/status?peer=127.0.0.1", 400}, {"GET", "/status?peer=8.8.8.8", 400}, {"GET", "/other", 404},
	} {
		res := httptest.NewRecorder()
		s.ServeHTTP(res, httptest.NewRequest(tc.method, tc.path, nil))
		if res.Code != tc.code {
			t.Errorf("%s %s = %d，期望 %d", tc.method, tc.path, res.Code, tc.code)
		}
	}
	if got, err := client.PeerStatus(t.Context(), netip.MustParseAddr("8.8.8.8")); err == nil || got.Available {
		t.Fatalf("客户端接受了公网对端，状态 %+v", got)
	}
	s.publish(Status{ClockID: 17, Reason: "port_in_use"}, nil)
	if got, err := client.Status(t.Context()); err != nil || got.CanNegotiate() || got.Reason != "port_in_use" {
		t.Fatalf("正在占用的 PTP 状态 = %+v，错误 %v", got, err)
	}
	s.publish(Status{Available: true}, nil)
	if _, err := client.Status(t.Context()); err == nil {
		t.Fatal("接受了无角色或时钟标识的 available 状态")
	}
	server.Close()
	if _, err := client.Status(t.Context()); err == nil {
		t.Fatal("状态服务停止后仍然返回成功")
	}
	if _, err := (Client{Address: "192.168.44.2:55440"}).Status(t.Context()); err == nil {
		t.Fatal("客户端接受了非回环状态服务地址")
	}
}
