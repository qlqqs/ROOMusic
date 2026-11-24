// Package upstreammock 为开发与试用实例提供可进程内启动的外部服务。
package upstreammock

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Options struct {
	DataDir    string
	ScriptFile string
	Scenarios  map[string]string
}

type Server struct {
	mux         *http.ServeMux
	data        dataset
	script      script
	mu          sync.Mutex
	scenarios   map[string]Scenario
	records     [500]*requestRecord
	next, count int
}

type requestRecord struct {
	Time    time.Time  `json:"time"`
	Service string     `json:"service"`
	Method  string     `json:"method"`
	Path    string     `json:"path"`
	Query   url.Values `json:"query"`
	Status  int        `json:"status"`
}

func New(opts Options) (*Server, error) {
	data, err := loadData(opts.DataDir)
	if err != nil {
		return nil, err
	}
	script, err := loadScript(opts.ScriptFile)
	if err != nil {
		return nil, err
	}
	s := &Server{mux: http.NewServeMux(), data: data, script: script}
	s.reset()
	for service, name := range opts.Scenarios {
		sc := defaultScenario()
		sc.Name = name
		if err := s.setScenario(service, sc); err != nil {
			return nil, err
		}
	}
	s.route("GET /musicbrainz/ws/2/{entity}", s.musicbrainz)
	s.route("GET /musicbrainz/ws/2/{entity}/{id}", s.musicbrainz)
	s.route("GET /coverartarchive/{kind}/{id}/{$}", s.coverManifest)
	s.route("GET /coverartarchive/{kind}/{id}", s.coverManifest)
	s.route("GET /coverartarchive/release/{id}/{image}", s.coverImage)
	s.route("GET /lrclib/api/get", s.lyrics)
	s.route("GET /lrclib/api/search", s.lyrics)
	s.route("POST /acoustid/v2/lookup", s.acoustid)
	s.route("GET /searxng/search", s.search)
	s.route("POST /tavily/search", s.search)
	s.route("GET /openai/v1/models", s.models)
	s.route("GET /openai/chat/v1/models", s.models)
	s.route("POST /openai/chat/v1/chat/completions", s.model)
	s.route("GET /anthropic/v1/models", s.models)
	s.route("POST /openai/v1/responses", s.model)
	s.route("POST /openai/v1/chat/completions", s.model)
	s.route("POST /anthropic/v1/messages", s.model)
	s.mux.HandleFunc("GET /_mock/scenarios", s.getScenarios)
	s.mux.HandleFunc("PUT /_mock/scenarios/{service}", s.putScenario)
	s.mux.HandleFunc("GET /_mock/requests", s.getRequests)
	s.mux.HandleFunc("POST /_mock/reset", func(w http.ResponseWriter, r *http.Request) { s.reset(); w.WriteHeader(http.StatusNoContent) })
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

type object = map[string]any
type response struct {
	status int
	json   any
	image  []byte
	events []frame
}

func ok(v any) response { return response{status: 200, json: v} }
func problem(status int, msg string) response {
	return response{status: status, json: object{"error": msg}}
}
func missing() response { return problem(404, "未找到") }

func (s *Server) record(service string, r *http.Request) (Scenario, *requestRecord) {
	q := r.URL.Query()
	for key := range q {
		switch strings.ToLower(key) {
		case "client", "user", "key", "api_key", "token":
			q.Set(key, "***")
		}
	}
	rec := &requestRecord{Time: time.Now().UTC(), Service: service, Method: r.Method, Path: r.URL.Path, Query: q}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[s.next] = rec
	s.next = (s.next + 1) % len(s.records)
	s.count = min(s.count+1, len(s.records))
	return s.scenarios[service], rec
}

func (s *Server) getRequests(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := len(s.records)
	if q.Has("limit") {
		n, err := strconv.Atoi(q.Get("limit"))
		if err != nil || n < 0 {
			http.Error(w, "limit 必须是非负整数", 400)
			return
		}
		limit = min(n, limit)
	}
	service := q.Get("service")
	out := make([]requestRecord, 0, limit)
	s.mu.Lock()
	for i := 0; i < s.count && len(out) < limit; i++ {
		rec := s.records[(s.next-1-i+len(s.records))%len(s.records)]
		if service == "" || service == rec.Service {
			out = append(out, *rec)
		}
	}
	s.mu.Unlock()
	writeJSON(w, 200, out)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) error {
	reader := http.MaxBytesReader(w, r.Body, 8<<20)
	defer reader.Close()
	return decodeJSON(reader, v)
}

func (s *Server) route(pattern string, handler func(*http.Request, bool) response) {
	_, path, _ := strings.Cut(pattern, " /")
	service, _, _ := strings.Cut(path, "/")
	s.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		sc, rec := s.record(service, r)
		r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
		if !sc.wait(r) {
			return
		}
		var res response
		switch sc.Name {
		case "malformed":
			res = ok(nil)
			switch {
			case r.PathValue("image") != "":
				res.image = []byte("不是图片")
			case r.Method == http.MethodPost && (service == "openai" || service == "anthropic"):
				res.events = []frame{{data: "{"}}
			}
		case "rate_limited", "unavailable":
			status := 503
			if sc.Name == "rate_limited" && service != "musicbrainz" {
				status = 429
			}
			w.Header().Set("Retry-After", strconv.Itoa(sc.RetryAfter))
			res = problem(status, sc.Name)
		default:
			res = handler(r, sc.Name == "not_found")
		}
		s.mu.Lock()
		rec.Status = res.status
		s.mu.Unlock()
		res.write(w, sc.Name)
	})
}

func (res response) write(w http.ResponseWriter, scenario string) {
	if res.events != nil {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(res.status)
		for _, f := range res.events {
			if f.name != "" {
				fmt.Fprintf(w, "event: %s\n", f.name)
			}
			fmt.Fprintf(w, "data: %s\n\n", f.data)
			if err := http.NewResponseController(w).Flush(); err != nil {
				return
			}
			if scenario == "drop" && f.content {
				abortStream(w)
				return
			}
		}
		return
	}
	var body []byte
	if res.image != nil {
		w.Header().Set("Content-Type", http.DetectContentType(res.image))
		body = res.image
	} else {
		w.Header().Set("Content-Type", "application/json")
		body, _ = json.Marshal(res.json)
	}
	if scenario == "malformed" && res.image == nil {
		body = []byte(`{"data":`)
	}
	if scenario == "drop" {
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		body = body[:len(body)/2]
	}
	w.WriteHeader(res.status)
	_, _ = w.Write(body)
	if scenario == "drop" {
		abortStream(w)
	}
}

func abortStream(w http.ResponseWriter) {
	ctrl := http.NewResponseController(w)
	_ = ctrl.Flush()
	conn, _, err := ctrl.Hijack()
	if err == nil {
		_ = conn.Close()
		return
	}
	panic(http.ErrAbortHandler)
}
