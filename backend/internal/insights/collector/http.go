package collector

import (
	"encoding/json"
	"mime"
	"net/http"
	"net/netip"
	"slices"
	"sync"
	"time"

	"github.com/qlqq/roomusic/backend/internal/insights/schema"
)

// 每个地址最多持有 30 枚令牌，每枚消耗一小时后归还。
// 按消耗顺序循环使用，保证任意连续一小时内最多放行 30 次。
type bucket struct {
	available [30]time.Time
	next      int
	expires   time.Time
}

type limiter struct {
	mu        sync.Mutex
	clients   map[netip.Prefix]*bucket
	nextSweep time.Time
}

func (l *limiter) allow(addr netip.Addr, now time.Time) bool {
	ip := addr.Unmap()
	bits := 32
	if ip.Is6() {
		bits = 64
	}
	key := netip.PrefixFrom(ip, bits).Masked()
	l.mu.Lock()
	defer l.mu.Unlock()
	if !now.Before(l.nextSweep) {
		for k, b := range l.clients {
			if !now.Before(b.expires) {
				delete(l.clients, k)
			}
		}
		l.nextSweep = now.Add(time.Hour)
	}
	b := l.clients[key]
	if b == nil {
		b = &bucket{}
		l.clients[key] = b
	}
	if now.Before(b.available[b.next]) {
		return false
	}
	b.expires = now.Add(time.Hour)
	b.available[b.next] = b.expires
	b.next = (b.next + 1) % len(b.available)
	return true
}

func NewHandler(store *Store, trustedProxies ...netip.Addr) http.Handler {
	limit := limiter{clients: make(map[netip.Prefix]*bucket)}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		peer, err := netip.ParseAddrPort(r.RemoteAddr)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		addr := peer.Addr()
		// 本机反向代理覆盖 X-Real-IP；其他连接始终按 TCP 对端限速。
		if slices.Contains(trustedProxies, addr.Unmap()) {
			if forwarded, err := netip.ParseAddr(r.Header.Get("X-Real-IP")); err == nil && forwarded.Zone() == "" {
				addr = forwarded
			}
		}
		if !limit.allow(addr, time.Now()) {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		kind, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || kind != "application/json" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		p, err := schema.Decode(http.MaxBytesReader(w, r.Body, schema.MaxBytes))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if err := store.put(p, time.Now()); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /api/v1/summary", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(store.Summary(time.Now()))
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = summaryPage.Execute(w, store.Summary(time.Now()))
	})
	return mux
}
