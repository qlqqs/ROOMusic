package egress

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestIsPublicIP(t *testing.T) {
	for _, tc := range []struct {
		ip     string
		public bool
	}{
		{"127.0.0.1", false}, {"::1", false}, {"10.1.2.3", false}, {"172.16.1.1", false}, {"192.168.1.1", false}, {"fc00::1", false}, {"169.254.1.1", false}, {"fe80::1", false}, {"100.64.0.1", false}, {"100.127.255.254", false}, {"::ffff:192.168.1.1", false}, {"::ffff:8.8.8.8", true}, {"64:ff9b::a00:1", false}, {"64:ff9b:1::1", false}, {"2002:0a00:0001::1", false}, {"fe80::1%eth0", false}, {"2606:4700::1%eth0", false}, {"192.0.2.1", false}, {"198.51.100.1", false}, {"203.0.113.1", false}, {"2001:db8::1", false}, {"198.18.0.1", false}, {"0.0.0.0", false}, {"::", false}, {"224.0.0.1", false}, {"ff02::1", false}, {"255.255.255.255", false}, {"8.8.8.8", true}, {"1.1.1.1", true}, {"2606:4700:4700::1111", true},
	} {
		t.Run(tc.ip, func(t *testing.T) {
			if got := IsPublicIP(netip.MustParseAddr(tc.ip)); got != tc.public {
				t.Fatalf("got %v want %v", got, tc.public)
			}
		})
	}
}
func testClient(t *testing.T, o Options) *http.Client {
	t.Helper()
	o.Timeout = 2 * time.Second
	c, err := NewClient(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.CloseIdleConnections)
	return c
}
func assertDenied(t *testing.T, err error, code ErrorCode) {
	t.Helper()
	var e *Error
	if !errors.Is(err, ErrDenied) || !errors.As(err, &e) || e.Code != code {
		t.Fatalf("got %v want %s", err, code)
	}
}
func TestDNSRejectsPrivateAndMixedAnswers(t *testing.T) {
	for _, ips := range [][]netip.Addr{{netip.MustParseAddr("10.0.0.1")}, {netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("192.168.1.1")}} {
		c := testClient(t, Options{LookupNetIP: func(context.Context, string, string) ([]netip.Addr, error) { return ips, nil }})
		_, err := c.Get("http://synthetic.invalid/")
		assertDenied(t, err, CodeAddressNotAllowed)
	}
}
func TestAllowlistAndUnrestrictedUseOnlyLocalServer(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "synthetic") }))
	defer s.Close()
	u, _ := url.Parse(s.URL)
	port, _ := strconv.Atoi(u.Port())
	lookup := func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}
	for _, tc := range []struct {
		name   string
		policy Policy
		allow  bool
		ok     bool
	}{{"allowlisted", PublicOrAllowlist, true, true}, {"wrong_port", PublicOrAllowlist, false, false}, {"public_only_ignores_allowlist", PublicOnly, true, false}, {"unrestricted", Unrestricted, false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			c := testClient(t, Options{Policy: tc.policy, LookupNetIP: lookup, Allowlist: func(host string, p int) bool { return tc.allow && host == "synthetic.invalid" && p == port }})
			resp, err := c.Get("http://" + net.JoinHostPort("synthetic.invalid", u.Port()))
			if !tc.ok {
				assertDenied(t, err, CodeAddressNotAllowed)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			raw, _ := io.ReadAll(resp.Body)
			if string(raw) != "synthetic" {
				t.Fatalf("body=%q", raw)
			}
		})
	}
}
func TestRedirectRechecksActualDestination(t *testing.T) {
	var privateHits atomic.Int32
	private := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { privateHits.Add(1); w.WriteHeader(200) }))
	defer private.Close()
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, private.URL+"/private", http.StatusFound)
	}))
	defer public.Close()
	c := testClient(t, Options{MaxRedirects: 5, LookupNetIP: func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
	}})
	p := c.Transport.(*policyTransport)
	originalDial := p.transport.DialContext
	p.transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		// 仅将合成公网首跳路由到假服务；后续私有跳必须经过真实生产拨号策略。
		if address != "synthetic.invalid:80" {
			return originalDial(ctx, network, address)
		}
		if _, err := p.addresses(ctx, "synthetic.invalid", false); err != nil {
			return nil, err
		}
		return (&net.Dialer{}).DialContext(ctx, network, public.Listener.Addr().String())
	}
	_, err := c.Get("http://synthetic.invalid/start")
	assertDenied(t, err, CodeAddressNotAllowed)
	if privateHits.Load() != 0 {
		t.Fatal("私有目标收到了重定向请求")
	}
}
func TestRedirectLimitAndSchemes(t *testing.T) {
	var hits atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, "/again", http.StatusFound)
	}))
	defer s.Close()
	c := testClient(t, Options{Policy: Unrestricted, MaxRedirects: 2})
	_, err := c.Get(s.URL)
	assertDenied(t, err, CodeTooManyRedirects)
	if hits.Load() != 3 {
		t.Fatalf("hits=%d want 3", hits.Load())
	}
	for _, endpoint := range []string{"ftp://synthetic.invalid/file", "file:///synthetic", "gopher://synthetic.invalid"} {
		_, err := c.Get(endpoint)
		assertDenied(t, err, CodeSchemeNotAllowed)
	}
}
