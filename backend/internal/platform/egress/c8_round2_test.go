package egress

import (
	"context"
	"net/netip"
	"testing"
)

func TestC8PublicNewExcludedNetworks(t *testing.T) {
	for _, address := range []string{"192.88.99.0", "192.88.99.255", "2001:2::", "2001:2:0:ffff:ffff:ffff:ffff:ffff", "2001:10::", "2001:1f:ffff:ffff:ffff:ffff:ffff:ffff", "3fff::", "3fff:fff:ffff:ffff:ffff:ffff:ffff:ffff", "::ffff:0:0:0", "::ffff:0:ffff:ffff"} {
		t.Run(address, func(t *testing.T) {
			ip := netip.MustParseAddr(address)
			if IsPublicIP(ip) {
				t.Fatal("特殊用途网段被判公网")
			}
			client, err := NewClient(Options{Policy: PublicOnly, LookupNetIP: func(context.Context, string, string) ([]netip.Addr, error) { return []netip.Addr{ip}, nil }})
			if err != nil {
				t.Fatal(err)
			}
			defer client.CloseIdleConnections()
			if err := CheckEndpoint(context.Background(), client, "https://synthetic.invalid"); err == nil {
				t.Fatal("域名解析至排除网段未拒绝")
			}
		})
	}
	for _, address := range []string{"192.88.98.255", "192.88.100.0", "2001:20::", "3fff:1000::", "::ffff:8.8.8.8"} {
		t.Run("public_"+address, func(t *testing.T) {
			if !IsPublicIP(netip.MustParseAddr(address)) {
				t.Fatal("排除范围扩大至公网")
			}
		})
	}
}
