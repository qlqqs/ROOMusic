package fakecast

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"github.com/qlqq/roomusic/backend/internal/devices/cast/castv2"
	"github.com/qlqq/roomusic/backend/internal/devices/lan"
	"github.com/qlqq/roomusic/backend/internal/devices/mdns"
	"golang.org/x/sync/errgroup"
)

func certificate() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	now := time.Now()
	cert := x509.Certificate{SerialNumber: big.NewInt(now.UnixNano()), Subject: pkix.Name{CommonName: "ROOMusic Fake Cast"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, &cert, &cert, &key.PublicKey, key)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, err
}

func lanAddress(listener net.Listener) (netip.AddrPort, error) {
	addr := listener.Addr().(*net.TCPAddr).AddrPort()
	ip := addr.Addr().Unmap()
	if ip.IsUnspecified() {
		conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353})
		if err != nil {
			return netip.AddrPort{}, err
		}
		ip = conn.LocalAddr().(*net.UDPAddr).AddrPort().Addr().Unmap()
		conn.Close()
	}
	if !lan.IsLANAddress(ip) {
		return netip.AddrPort{}, errors.New("Cast 公告需要局域网 IPv4")
	}
	return netip.AddrPortFrom(ip, addr.Port()), nil
}

// Serve 接管两个监听器；ready 提供公告的 TLS 地址和状态接口的 HTTP 基址。
func (r *Receiver) Serve(ctx context.Context, castListener, httpListener net.Listener, ready func(string, string)) error {
	defer castListener.Close()
	defer httpListener.Close()
	address, err := lanAddress(castListener)
	if err != nil {
		return err
	}
	key, err := certificate()
	if err != nil {
		return err
	}
	listener := tls.NewListener(castListener, &tls.Config{Certificates: []tls.Certificate{key}, MinVersion: tls.VersionTLS12})
	s := r.State()
	instance := "roomusic-" + s.ID
	entry := mdns.Entry{Service: "_googlecast._tcp.local.", Instance: instance, Host: instance + ".local.", IP: address.Addr(), Port: address.Port(), TXT: map[string]string{"id": s.ID, "fn": s.Name, "md": "ROOMusic Fake Cast", "ca": "4"}}
	server := &http.Server{Handler: r.Handler(), ReadHeaderTimeout: 5 * time.Second}
	group, run := errgroup.WithContext(ctx)
	group.Go(func() error { return mdns.Advertise(run, entry) })
	group.Go(func() error { return server.Serve(httpListener) })
	group.Go(func() error {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return err
			}
			p := &peer{Conn: conn}
			r.mu.Lock()
			if run.Err() != nil {
				r.mu.Unlock()
				conn.Close()
				return run.Err()
			}
			r.peers[p] = true
			r.workers.Go(func() { r.read(run, p) })
			r.mu.Unlock()
		}
	})
	closeDone := make(chan struct{})
	stop := context.AfterFunc(run, func() {
		defer close(closeDone)
		listener.Close()
		server.Close()
		r.mu.Lock()
		for p := range r.peers {
			p.Close()
		}
		r.mu.Unlock()
	})
	if ready != nil {
		a := httpListener.Addr().(*net.TCPAddr)
		host := a.IP.String()
		if a.IP.IsUnspecified() {
			host = address.Addr().String()
		}
		ready(address.String(), "http://"+net.JoinHostPort(host, strconv.Itoa(a.Port)))
	}
	err = group.Wait()
	if !stop() {
		<-closeDone
	}
	r.mu.Lock()
	r.setState("IDLE", "CANCELLED")
	r.mu.Unlock()
	r.workers.Wait()
	r.client.CloseIdleConnections()
	if ctx.Err() != nil {
		return nil
	}
	return err
}

func (r *Receiver) read(ctx context.Context, p *peer) {
	defer p.Close()
	defer func() { r.mu.Lock(); delete(r.peers, p); r.mu.Unlock() }()
	for {
		p.SetReadDeadline(time.Now().Add(20 * time.Second))
		msg, err := castv2.Read(p.Conn)
		if err != nil {
			return
		}
		r.handle(ctx, p, msg)
	}
}
