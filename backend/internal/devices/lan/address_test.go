package lan

import (
	"net/netip"
	"testing"
)

func TestCastLANAddresses(t *testing.T) {
	for _, tc := range []struct {
		ip   string
		want bool
	}{
		{"10.1.2.3", true}, {"172.16.0.1", true}, {"172.31.255.254", true},
		{"192.168.1.20", true}, {"169.254.1.5", true},
		{"172.32.0.1", false}, {"198.51.100.20", false}, {"100.64.0.1", false},
		{"127.0.0.1", false}, {"0.0.0.0", false}, {"224.0.0.251", false},
		{"fc00::1", false}, {"::ffff:192.168.1.20", false},
	} {
		if got := IsLANAddress(netip.MustParseAddr(tc.ip)); got != tc.want {
			t.Errorf("IsLANAddress(%s) = %t，期望 %t", tc.ip, got, tc.want)
		}
	}
}
