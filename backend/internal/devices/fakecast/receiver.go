// Package fakecast 提供可在局域网内运行的开发接收端。
package fakecast

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/qlqq/roomusic/backend/internal/devices/cast/castv2"
	"github.com/qlqq/roomusic/backend/internal/devices/lan"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type Options struct {
	ID, Name    string
	FixedVolume bool
}

type State struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	AppID          string          `json:"app_id"`
	URI            string          `json:"uri"`
	Metadata       json.RawMessage `json:"metadata"`
	PlayerState    string          `json:"player_state"`
	IdleReason     string          `json:"idle_reason"`
	MediaSessionID int             `json:"media_session_id"`
	Volume         float64         `json:"volume"`
	Muted          bool            `json:"muted"`
	BytesRead      int64           `json:"bytes_read"`
	LoadCount      int             `json:"load_count"`
	CurrentTime    float64         `json:"current_time"`
}

type Receiver struct {
	mu                   sync.Mutex
	state                State
	fixed                bool
	transport, sessionID string
	media                mediaInfo
	started              time.Time
	cancel               context.CancelFunc
	changed              chan struct{}
	peers                map[*peer]bool
	client               *http.Client
	workers              sync.WaitGroup
}

type mediaInfo struct {
	ContentID   string          `json:"contentId"`
	ContentType string          `json:"contentType"`
	Metadata    json.RawMessage `json:"metadata"`
	Duration    float64         `json:"duration,omitempty"`
	StreamType  string          `json:"streamType"`
}

type peer struct {
	net.Conn
	sender, transport string
}

func New(o Options) *Receiver {
	if o.ID == "" {
		o.ID = strings.ReplaceAll(uuid.New(), "-", "")
	}
	if o.Name == "" {
		o.Name = "ROOMusic 假 Cast 接收端"
	}
	return &Receiver{state: State{ID: o.ID, Name: o.Name, Volume: 0.5, PlayerState: "IDLE"}, fixed: o.FixedVolume, changed: make(chan struct{}), peers: map[*peer]bool{}, client: lan.NewStreamClient()}
}

func (r *Receiver) now() float64 {
	if !r.started.IsZero() {
		return r.state.CurrentTime + time.Since(r.started).Seconds()
	}
	return r.state.CurrentTime
}

func (r *Receiver) State() State {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.state
	s.CurrentTime = r.now()
	return s
}

func (r *Receiver) setState(state, reason string) {
	r.state.CurrentTime = r.now()
	r.started = time.Time{}
	if state == "PLAYING" {
		r.started = time.Now()
	}
	r.state.PlayerState, r.state.IdleReason = state, reason
	if state == "IDLE" && r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	close(r.changed)
	r.changed = make(chan struct{})
}

func (r *Receiver) launch(id string) {
	r.setState("IDLE", "INTERRUPTED")
	r.state.AppID = id
	r.transport = "transport-" + uuid.New()
	r.sessionID = uuid.New()
	r.state.MediaSessionID = 0
	r.state.URI = ""
	r.media = mediaInfo{}
}

func (r *Receiver) volume() castv2.Volume {
	kind := "attenuation"
	if r.fixed {
		kind = "fixed"
	}
	return castv2.Volume{Level: &r.state.Volume, Muted: r.state.Muted, ControlType: kind}
}

func (r *Receiver) status(ns string) any {
	if ns == castv2.Receiver {
		status := castv2.ReceiverStatus{Volume: r.volume()}
		if r.state.AppID != "" {
			status.Applications = []castv2.Application{{AppID: r.state.AppID, TransportID: r.transport, SessionID: r.sessionID}}
		}
		return status
	}
	if r.state.MediaSessionID == 0 {
		return []any{}
	}
	return []any{map[string]any{"mediaSessionId": r.state.MediaSessionID, "playerState": r.state.PlayerState, "idleReason": r.state.IdleReason, "currentTime": r.now(), "media": r.media}}
}

func (r *Receiver) send(p *peer, ns, source, dest string, body any) {
	payload, err := json.Marshal(body)
	if err != nil {
		return
	}
	p.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if err := castv2.Write(p.Conn, castv2.Message{Source: source, Destination: dest, Namespace: ns, Payload: payload}); err != nil {
		p.Close()
	}
}

func (r *Receiver) notify(ns string, caller *peer, id int, broadcast bool) {
	kind, src := "RECEIVER_STATUS", castv2.ReceiverID
	if ns == castv2.Media {
		kind, src = "MEDIA_STATUS", r.transport
	}
	status := r.status(ns)
	for p := range r.peers {
		if p != caller && (!broadcast || p.sender == "" || (ns == castv2.Media && p.transport != r.transport)) {
			continue
		}
		requestID := 0
		if p == caller {
			requestID = id
		}
		r.send(p, ns, src, p.sender, map[string]any{"type": kind, "requestId": requestID, "status": status})
	}
}

func (r *Receiver) handle(ctx context.Context, p *peer, msg castv2.Message) {
	var req struct {
		Type           string
		AppID          string
		RequestID      int
		MediaSessionID int
		Media          mediaInfo
		Volume         struct {
			Level *float64
			Muted *bool
		}
	}
	if json.Unmarshal(msg.Payload, &req) != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	switch msg.Namespace {
	case castv2.Heartbeat:
		if req.Type == "PING" {
			r.send(p, castv2.Heartbeat, msg.Destination, msg.Source, map[string]string{"type": "PONG"})
		}
		return
	case castv2.Connection:
		if req.Type == "CONNECT" {
			p.sender = msg.Source
			if msg.Destination == r.transport {
				p.transport = r.transport
			}
		}
		return
	case castv2.Receiver:
		if msg.Destination != castv2.ReceiverID {
			return
		}
		switch req.Type {
		case "GET_STATUS":
		case "LAUNCH":
			if req.AppID != r.state.AppID {
				r.launch(req.AppID)
			}
		case "SET_VOLUME":
			if !r.fixed {
				if v := req.Volume.Level; v != nil {
					r.state.Volume = min(1, max(0, *v))
				}
				if m := req.Volume.Muted; m != nil {
					r.state.Muted = *m
				}
			}
		default:
			return
		}
	case castv2.Media:
		if msg.Destination != r.transport || p.transport != r.transport {
			return
		}
		switch req.Type {
		case "GET_STATUS":
		case "LOAD":
			if !lan.ValidStreamURI(req.Media.ContentID) {
				r.send(p, msg.Namespace, msg.Destination, msg.Source, map[string]any{"type": "LOAD_FAILED", "requestId": req.RequestID})
				return
			}
			r.setState("IDLE", "INTERRUPTED")
			r.media = req.Media
			r.state.LoadCount++
			r.state.MediaSessionID = r.state.LoadCount
			r.state.URI, r.state.Metadata = req.Media.ContentID, req.Media.Metadata
			r.state.BytesRead, r.state.CurrentTime = 0, 0
			r.setState("PLAYING", "")
			ctx, cancel := context.WithCancel(ctx)
			r.cancel = cancel
			r.workers.Go(func() { r.pull(ctx, req.Media) })
		case "PAUSE", "PLAY", "STOP":
			if req.MediaSessionID != r.state.MediaSessionID || r.state.PlayerState == "IDLE" {
				r.send(p, msg.Namespace, msg.Destination, msg.Source, map[string]any{"type": "INVALID_REQUEST", "requestId": req.RequestID})
				return
			}
			r.control(req.Type)
		default:
			return
		}
	default:
		return
	}
	r.notify(msg.Namespace, p, req.RequestID, req.Type != "GET_STATUS")
}

func (r *Receiver) control(kind string) {
	switch kind {
	case "PLAY":
		r.setState("PLAYING", "")
	case "PAUSE":
		r.setState("PAUSED", "")
	case "STOP":
		r.setState("IDLE", "CANCELLED")
	case "FINISH":
		r.setState("IDLE", "FINISHED")
	}
}

func (r *Receiver) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /_fake/state", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(r.State())
	})
	mux.HandleFunc("POST /_fake/{action}", func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		defer r.mu.Unlock()
		action := strings.ToUpper(req.PathValue("action"))
		switch action {
		case "TAKEOVER":
			r.launch("FAKEOTHER")
			r.notify(castv2.Receiver, nil, 0, true)
		case "PLAY", "PAUSE", "STOP", "FINISH":
			if r.state.PlayerState == "IDLE" {
				w.WriteHeader(http.StatusConflict)
				return
			}
			r.control(action)
			r.notify(castv2.Media, nil, 0, true)
		default:
			http.NotFound(w, req)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

func (r *Receiver) pull(ctx context.Context, item mediaInfo) {
	reason := "ERROR"
	defer func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if ctx.Err() == nil {
			r.setState("IDLE", reason)
			r.notify(castv2.Media, nil, 0, true)
		}
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, item.ContentID, nil)
	if err != nil {
		return
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	var buf [16384]byte
	for {
		n, err := resp.Body.Read(buf[:])
		r.mu.Lock()
		if ctx.Err() != nil {
			r.mu.Unlock()
			return
		}
		r.state.BytesRead += int64(n)
		remaining := item.Duration - r.now()
		playing := r.state.PlayerState == "PLAYING"
		changed := r.changed
		r.mu.Unlock()
		if err != nil && !errors.Is(err, io.EOF) {
			return
		}
		if !playing {
			select {
			case <-ctx.Done():
				return
			case <-changed:
			}
			continue
		}
		if err == io.EOF {
			if remaining <= 0 {
				reason = "FINISHED"
				return
			}
			timer := time.NewTimer(time.Duration(remaining * float64(time.Second)))
			select {
			case <-ctx.Done():
			case <-changed:
			case <-timer.C:
			}
			timer.Stop()
		}
	}
}
