package ptp

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"syscall"
	"time"

	"github.com/qlqq/roomusic/backend/internal/devices/lan"
)

type state struct {
	mu     sync.RWMutex
	status Status
	peers  map[netip.Addr]Status
}

func (s *state) publish(status Status, peers map[netip.Addr]Status) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status, s.peers = status, peers
}

func (s *state) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/status" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var peer netip.Addr
	if raw := r.URL.Query().Get("peer"); raw != "" {
		var err error
		peer, err = netip.ParseAddr(raw)
		if err != nil || !lan.IsLANAddress(peer) {
			http.Error(w, "需要局域网 IPv4", http.StatusBadRequest)
			return
		}
	}
	s.mu.RLock()
	status := s.status
	if peer.IsValid() {
		if specific, ok := s.peers[peer]; ok {
			status = specific
		} else if status.Available {
			status = Status{ClockID: status.ClockID, Reason: "peer_unknown"}
		}
	}
	s.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(status)
}

func Run(ctx context.Context, addr string, logger *slog.Logger) error {
	addr, err := loopback(addr)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	var seed [8]byte
	rand.Read(seed[:])
	seed[0] = seed[0]&0xfc | 2
	id := be.Uint64(seed[:])
	state := &state{status: Status{ClockID: id, Reason: "starting"}}
	server := http.Server{Handler: state, ReadHeaderTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 4096}
	serverErr := make(chan error, 1)
	go func() { serverErr <- server.Serve(listener) }()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer server.Close()
	udpDone := make(chan struct{})
	go func() {
		defer close(udpDone)
		lastReason := ""
		for ctx.Err() == nil {
			w, err := openWire(id)
			if err == nil {
				logger.Info("PTP 时钟端口已就绪", "event", "ptp.ready", "interfaces", len(w.links), "clock_id", id)
				lastReason = ""
				e := engine{wire: w, local: id, peers: make(map[netip.Addr]*peerClock)}
				err = e.serve(ctx, state)
			}
			if ctx.Err() != nil {
				return
			}
			reason := "network_unavailable"
			if errors.Is(err, syscall.EADDRINUSE) {
				reason = "port_in_use"
			}
			state.publish(Status{ClockID: id, Reason: reason}, nil)
			if reason != lastReason {
				logger.Warn("PTP 时钟暂不可用", "event", "ptp.unavailable", "reason", reason, "error", err)
				lastReason = reason
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second):
			}
		}
	}()
	select {
	case <-ctx.Done():
	case err = <-serverErr:
	}
	cancel()
	<-udpDone
	return err
}
