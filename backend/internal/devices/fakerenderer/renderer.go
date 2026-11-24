package fakerenderer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type Options struct {
	Name    string
	UDN     string
	RelTime string
}

type State struct {
	UDN            string `json:"udn"`
	Name           string `json:"name"`
	URI            string `json:"uri"`
	Metadata       string `json:"metadata"`
	TransportState string `json:"transport_state"`
	Volume         int    `json:"volume"`
	RelTime        string `json:"rel_time"`
	BytesRead      int64  `json:"bytes_read"`
	SampleRate     int    `json:"sample_rate"`
	BitDepth       int    `json:"bit_depth"`
}

type Renderer struct {
	mu       sync.Mutex
	state    State
	zeroTime bool
	client   *http.Client
	started  time.Time
	elapsed  time.Duration
	cancel   context.CancelFunc
	changed  chan struct{}
	pulls    sync.WaitGroup
	closed   bool
}

func New(opts Options) (*Renderer, error) {
	if opts.RelTime != "" && opts.RelTime != "normal" && opts.RelTime != "zero" {
		return nil, fmt.Errorf("rel-time 应为 normal 或 zero")
	}
	if opts.UDN == "" {
		opts.UDN = "uuid:" + uuid.New()
	}
	if opts.Name == "" {
		opts.Name = "ROOMusic 假渲染器"
	}
	return &Renderer{state: State{UDN: opts.UDN, Name: opts.Name, TransportState: "STOPPED", Volume: 50},
		zeroTime: opts.RelTime == "zero", client: streamClient(), changed: make(chan struct{})}, nil
}

func (r *Renderer) Close() {
	r.mu.Lock()
	r.closed = true
	r.stopLocked()
	r.mu.Unlock()
	r.pulls.Wait()
	r.client.CloseIdleConnections()
}

func (r *Renderer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /description.xml", r.description)
	mux.HandleFunc("GET /scpd/{service}", r.scpd)
	mux.HandleFunc("POST /control/{service}", r.soap)
	mux.HandleFunc("GET /_fake/state", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(r.State())
	})
	return mux
}

func (r *Renderer) State() State {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.state
	s.RelTime = r.relTimeLocked()
	return s
}

func (r *Renderer) elapsedLocked() time.Duration {
	if r.started.IsZero() {
		return r.elapsed
	}
	return r.elapsed + time.Since(r.started)
}

func (r *Renderer) relTimeLocked() string {
	s := int(r.elapsedLocked() / time.Second)
	if r.zeroTime {
		s = 0
	}
	return fmt.Sprintf("%02d:%02d:%02d", s/3600, s/60%60, s%60)
}

func (r *Renderer) signalLocked() { close(r.changed); r.changed = make(chan struct{}) }

func (r *Renderer) stopLocked() {
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	r.started = time.Time{}
	r.elapsed = 0
	r.state.TransportState = "STOPPED"
	r.signalLocked()
}

type actionError struct {
	code        int
	description string
}

func (r *Renderer) act(action string, args map[string]string) (map[string]string, *actionError) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, &actionError{501, "Action Failed"}
	}
	if action != "GetProtocolInfo" && args["InstanceID"] != "0" {
		return nil, &actionError{718, "Invalid InstanceID"}
	}
	switch action {
	case "SetAVTransportURI":
		if !validStreamURI(args["CurrentURI"]) {
			return nil, &actionError{402, "Invalid Args"}
		}
		r.stopLocked()
		r.state.URI = args["CurrentURI"]
		r.state.Metadata = args["CurrentURIMetaData"]
		r.state.BytesRead, r.state.SampleRate, r.state.BitDepth = 0, 0, 0
	case "Play":
		if args["Speed"] != "1" {
			return nil, &actionError{717, "Play Speed Not Supported"}
		}
		if r.state.URI == "" {
			return nil, &actionError{701, "Transition Not Available"}
		}
		if r.state.TransportState == "PLAYING" {
			return nil, nil
		}
		r.state.TransportState = "PLAYING"
		r.started = time.Now()
		r.signalLocked()
		if r.cancel == nil {
			ctx, cancel := context.WithCancel(context.Background())
			r.cancel = cancel
			r.elapsed = 0
			r.state.BytesRead = 0
			r.pulls.Add(1)
			go r.pull(ctx, r.state.URI)
		}
	case "Pause":
		if r.state.TransportState != "PLAYING" {
			return nil, &actionError{701, "Transition Not Available"}
		}
		r.elapsed = r.elapsedLocked()
		r.started = time.Time{}
		r.state.TransportState = "PAUSED_PLAYBACK"
		r.signalLocked()
	case "Stop":
		r.stopLocked()
	case "GetTransportInfo":
		return map[string]string{"CurrentTransportState": r.state.TransportState, "CurrentTransportStatus": "OK", "CurrentSpeed": "1"}, nil
	case "GetPositionInfo":
		pos := r.relTimeLocked()
		return map[string]string{"Track": "1", "TrackDuration": "00:00:00", "TrackMetaData": r.state.Metadata, "TrackURI": r.state.URI, "RelTime": pos, "AbsTime": pos, "RelCount": "2147483647", "AbsCount": "2147483647"}, nil
	case "GetVolume", "SetVolume":
		if args["Channel"] != "Master" {
			return nil, &actionError{402, "Invalid Args"}
		}
		if action == "GetVolume" {
			return map[string]string{"CurrentVolume": strconv.Itoa(r.state.Volume)}, nil
		}
		volume, err := strconv.Atoi(args["DesiredVolume"])
		if err != nil || volume < 0 || volume > 100 {
			return nil, &actionError{402, "Invalid Args"}
		}
		r.state.Volume = volume
	case "GetProtocolInfo":
		return map[string]string{"Source": "", "Sink": "http-get:*:audio/flac:*,http-get:*:audio/wav:*"}, nil
	default:
		return nil, &actionError{401, "Invalid Action"}
	}
	return nil, nil
}
