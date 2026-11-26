package egress

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const proxyFixtureUser, proxyFixturePassword = "fixture-user", "fixture:password+test"

type proxyFixture struct {
	Config   ProxyConfig
	mu       sync.Mutex
	targets  []string
	attempts int
}

func (p *proxyFixture) record(target string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.targets = append(p.targets, target)
}

func (p *proxyFixture) attempted() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.attempts++
}

func (p *proxyFixture) Targets() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.targets)
}

func (p *proxyFixture) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.attempts
}

// NewProxyFixture 把目标映射到测试服务器，整个夹具只拨回环。
func NewProxyFixture(t *testing.T, scheme string, auth bool, httpTarget, tlsTarget *httptest.Server) *proxyFixture {
	t.Helper()
	p := &proxyFixture{Config: ProxyConfig{Scheme: scheme}}
	if auth {
		p.Config.Username, p.Config.Password = proxyFixtureUser, proxyFixturePassword
	}
	if scheme == "socks5" {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			var connections sync.WaitGroup
			defer connections.Wait()
			for {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				connections.Go(func() {
					defer conn.Close()
					p.serveSOCKS(conn, httpTarget, tlsTarget)
				})
			}
		}()
		t.Cleanup(func() {
			listener.Close()
			<-done
		})
		p.Config.Host = "127.0.0.1"
		p.Config.Port = listener.Addr().(*net.TCPAddr).Port
		return p
	}

	forward := &http.Transport{}
	t.Cleanup(forward.CloseIdleConnections)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.attempted()
		header := r.Header.Get("Proxy-Authorization")
		wantAuth := ""
		if auth {
			wantAuth = "Basic " + base64.StdEncoding.EncodeToString([]byte(proxyFixtureUser+":"+proxyFixturePassword))
		}
		if header != wantAuth {
			w.WriteHeader(http.StatusProxyAuthRequired)
			return
		}
		if r.Method == http.MethodConnect {
			p.record("CONNECT " + r.Host)
			upstream, err := net.DialTimeout("tcp", tlsTarget.Listener.Addr().String(), 2*time.Second)
			if err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			defer upstream.Close()
			client, buffered, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			defer client.Close()
			client.SetDeadline(time.Now().Add(4 * time.Second))
			if _, err := buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
				t.Error(err)
				return
			}
			if err := buffered.Flush(); err != nil {
				t.Error(err)
				return
			}
			relayProxyFixture(client, buffered.Reader, upstream)
			return
		}
		p.record(r.Method + " " + r.URL.String())
		req := r.Clone(r.Context())
		req.URL.Host = httpTarget.Listener.Addr().String()
		req.RequestURI = ""
		req.Header.Del("Proxy-Authorization")
		resp, err := forward.RoundTrip(req)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		for key, values := range resp.Header {
			w.Header()[key] = values
		}
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	}))
	t.Cleanup(server.Close)
	p.Config.Host = "127.0.0.1"
	p.Config.Port = server.Listener.Addr().(*net.TCPAddr).Port
	return p
}

func (p *proxyFixture) serveSOCKS(conn net.Conn, httpTarget, tlsTarget *httptest.Server) {
	conn.SetDeadline(time.Now().Add(4 * time.Second))
	read := bufio.NewReader(conn)
	header := make([]byte, 2)
	if _, err := io.ReadFull(read, header); err != nil || header[0] != 5 {
		return
	}
	p.attempted()
	methods := make([]byte, header[1])
	if _, err := io.ReadFull(read, methods); err != nil {
		return
	}
	method := byte(0)
	if p.Config.Username != "" {
		method = 2
	}
	if !slices.Contains(methods, method) {
		conn.Write([]byte{5, 255})
		return
	}
	if _, err := conn.Write([]byte{5, method}); err != nil {
		return
	}
	if method == 2 {
		version, err := read.ReadByte()
		user, userErr := readSOCKSString(read)
		password, passwordErr := readSOCKSString(read)
		if err != nil || userErr != nil || passwordErr != nil || version != 1 {
			return
		}
		if user != p.Config.Username || password != p.Config.Password {
			conn.Write([]byte{1, 1})
			return
		}
		if _, err := conn.Write([]byte{1, 0}); err != nil {
			return
		}
	}

	command := make([]byte, 4)
	if _, err := io.ReadFull(read, command); err != nil || !slices.Equal(command[:3], []byte{5, 1, 0}) {
		return
	}
	var host string
	switch command[3] {
	case 1:
		var ip [4]byte
		if _, err := io.ReadFull(read, ip[:]); err != nil {
			return
		}
		host = netip.AddrFrom4(ip).String()
	case 3:
		var err error
		host, err = readSOCKSString(read)
		if err != nil {
			return
		}
	default:
		return
	}
	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(read, portBytes); err != nil {
		return
	}
	port := int(portBytes[0])*256 + int(portBytes[1])
	p.record(net.JoinHostPort(host, strconv.Itoa(port)))
	target := httpTarget
	if port == 443 {
		target = tlsTarget
	}
	upstream, err := net.DialTimeout("tcp", target.Listener.Addr().String(), 2*time.Second)
	if err != nil {
		return
	}
	defer upstream.Close()
	if _, err := conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0}); err != nil {
		return
	}
	relayProxyFixture(conn, read, upstream)
}

func readSOCKSString(read *bufio.Reader) (string, error) {
	size, err := read.ReadByte()
	if err != nil {
		return "", err
	}
	data := make([]byte, size)
	_, err = io.ReadFull(read, data)
	return string(data), err
}

func relayProxyFixture(client net.Conn, buffered io.Reader, upstream net.Conn) {
	done := make(chan struct{})
	go func() {
		io.Copy(upstream, buffered)
		upstream.(*net.TCPConn).CloseWrite()
		close(done)
	}()
	io.Copy(client, upstream)
	client.Close()
	<-done
}

func TestOutboundProxyProtocols(t *testing.T) {
	t.Cleanup(func() { SetProxy(nil) })
	for _, scheme := range []string{"http", "socks5"} {
		for _, auth := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/auth=%t", scheme, auth), func(t *testing.T) {
				const host = "fixture.example.com"
				origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Host != host || r.Header.Get("Proxy-Authorization") != "" {
						t.Error("目标收到错误的主机名或代理鉴权头")
					}
					io.WriteString(w, r.URL.Path)
				})
				plain, secure := httptest.NewServer(origin), httptest.NewTLSServer(origin)
				t.Cleanup(plain.Close)
				t.Cleanup(secure.Close)
				proxy := NewProxyFixture(t, scheme, auth, plain, secure)
				SetProxy(&proxy.Config)
				// 本地返回公网 IP，代理应收到原主机名，并由夹具映射到合成目标。
				client := testClient(t, Options{Policy: PublicOnly, LookupNetIP: func(context.Context, string, string) ([]netip.Addr, error) {
					return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
				}})
				_, pool := client.Transport.(*policyTransport).proxyPool()
				roots := x509.NewCertPool()
				roots.AddCert(secure.Certificate())
				pool.TLSClientConfig = &tls.Config{RootCAs: roots}
				requireProxyBody(t, client, "http://"+host+"/plain", "/plain")
				requireProxyBody(t, client, "https://"+host+"/secure", "/secure")

				want := []string{"GET http://" + host + "/plain", "CONNECT " + host + ":443"}
				if scheme == "socks5" {
					want = []string{host + ":80", host + ":443"}
				}
				if got := proxy.Targets(); !slices.Equal(got, want) || proxy.count() != 2 {
					t.Fatalf("代理收到 %v，共 %d 次，期望 %v", got, proxy.count(), want)
				}
				if auth {
					wrong := proxy.Config
					wrong.Password += "-incorrect"
					SetProxy(&wrong)
					_, err := client.Get("http://" + host + "/plain")
					assertProxyError(t, err, CodeProxyAuth)
					if scheme == "http" {
						_, err = client.Get("https://" + host + "/secure")
						assertProxyError(t, err, CodeProxyAuth)
					}
				}
			})
		}
	}
}

func TestOutboundProxyBypassAndPolicy(t *testing.T) {
	t.Cleanup(func() { SetProxy(nil) })
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "直连")
	}))
	t.Cleanup(origin.Close)
	proxy := NewProxyFixture(t, "http", false, origin, nil)
	SetProxy(&proxy.Config)
	lookup := func(_ context.Context, _, host string) ([]netip.Addr, error) {
		if host == "private.invalid" {
			return []netip.Addr{netip.MustParseAddr("10.0.0.1")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}
	client := testClient(t, Options{Policy: Unrestricted, LookupNetIP: lookup})
	requireProxyBody(t, client, origin.URL, "直连")
	requireProxyBody(t, client, strings.Replace(origin.URL, "127.0.0.1", "lan.invalid", 1), "直连")
	for _, target := range []string{"http://private.invalid/", "http://lan.invalid/", origin.URL} {
		restricted := testClient(t, Options{Policy: PublicOnly, LookupNetIP: lookup})
		_, err := restricted.Get(target)
		assertDenied(t, err, CodeAddressNotAllowed)
	}
	if proxy.count() != 0 {
		t.Fatalf("局域网目标产生 %d 次代理连接", proxy.count())
	}
}

func TestOutboundProxyPrivateRoutes(t *testing.T) {
	private := netip.MustParseAddr("10.0.0.7")
	linkLocal := netip.MustParseAddr("169.254.10.7")
	transport := policyTransport{options: Options{
		Policy: Unrestricted,
		LookupNetIP: func(context.Context, string, string) ([]netip.Addr, error) {
			return []netip.Addr{private, linkLocal}, nil
		},
	}}
	for _, tc := range []struct {
		address string
		want    []netip.Addr
	}{
		{"http://10.0.0.7/", []netip.Addr{private}},
		{"http://169.254.10.7/", []netip.Addr{linkLocal}},
		{"http://lan.invalid/", []netip.Addr{private, linkLocal}},
	} {
		target, err := url.Parse(tc.address)
		if err != nil {
			t.Fatal(err)
		}
		proxied, ips, err := transport.route(t.Context(), target, true, &ProxyConfig{Scheme: "http"})
		if err != nil || proxied || !slices.Equal(ips, tc.want) {
			t.Fatalf("目标 %s 经代理 %t，直连地址 %v，错误 %v", target, proxied, ips, err)
		}
	}
}

func TestOutboundProxySwitchAndDisable(t *testing.T) {
	t.Cleanup(func() { SetProxy(nil) })
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "目标响应")
	}))
	t.Cleanup(origin.Close)
	old := NewProxyFixture(t, "http", false, origin, nil)
	next := NewProxyFixture(t, "http", false, origin, nil)
	env := NewProxyFixture(t, "http", false, origin, nil)
	envURL := "http://" + net.JoinHostPort(env.Config.Host, strconv.Itoa(env.Config.Port))
	for _, key := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy"} {
		t.Setenv(key, envURL)
	}
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")

	var public atomic.Bool
	client := testClient(t, Options{Policy: Unrestricted, LookupNetIP: func(context.Context, string, string) ([]netip.Addr, error) {
		if public.Load() {
			return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}})
	target := strings.Replace(origin.URL, "127.0.0.1", "switch.invalid", 1)
	SetProxy(nil)
	if client.Transport.(*policyTransport).transport.Proxy != nil {
		t.Fatal("直连传输配置了代理回调")
	}
	requireProxyBody(t, client, target, "目标响应")
	public.Store(true)
	SetProxy(&old.Config)
	requireProxyBody(t, client, target, "目标响应")
	SetProxy(&next.Config)
	requireProxyBody(t, client, target, "目标响应")
	public.Store(false)
	SetProxy(nil)
	requireProxyBody(t, client, target, "目标响应")
	if old.count() != 1 || next.count() != 1 || env.count() != 0 {
		t.Fatalf("旧代理 %d 次，新代理 %d 次，环境代理 %d 次", old.count(), next.count(), env.count())
	}
}

func TestOutboundProxyUnavailable(t *testing.T) {
	t.Cleanup(func() { SetProxy(nil) })
	server := httptest.NewServer(http.NotFoundHandler())
	port := server.Listener.Addr().(*net.TCPAddr).Port
	server.Close()
	for _, scheme := range []string{"http", "socks5"} {
		t.Run(scheme, func(t *testing.T) {
			SetProxy(&ProxyConfig{Scheme: scheme, Host: "127.0.0.1", Port: port})
			client := testClient(t, Options{Policy: Unrestricted, LookupNetIP: func(_ context.Context, _, host string) ([]netip.Addr, error) {
				return nil, &net.DNSError{Name: host, IsNotFound: true}
			}})
			for _, target := range []string{"http://offline.invalid/", "https://offline.invalid/"} {
				_, err := client.Get(target)
				assertProxyError(t, err, CodeProxyUnavailable)
			}
		})
	}
}

func requireProxyBody(t *testing.T, client *http.Client, address, want string) {
	t.Helper()
	resp, err := client.Get(address)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK || string(body) != want {
		t.Fatalf("请求 %s 得到 HTTP %d、正文 %q、读取错误 %v", address, resp.StatusCode, body, err)
	}
}

func assertProxyError(t *testing.T, err error, want ErrorCode) {
	t.Helper()
	var outbound *Error
	if !errors.As(err, &outbound) || outbound.Code != want || errors.Is(err, ErrDenied) {
		t.Fatalf("代理错误 %v，期望 %s", err, want)
	}
	var requestErr *url.Error
	if errors.As(err, &requestErr) && strings.Contains(requestErr.Error(), proxyFixturePassword) {
		t.Fatal("出站错误泄露代理密码")
	}
}
