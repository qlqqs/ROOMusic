package upstreammock

import (
	"fmt"
	"io"
	"maps"
	"net/http"
	"time"
)

type Scenario struct {
	Name       string `json:"scenario"`
	RetryAfter int    `json:"retry_after"`
	DelayMS    int    `json:"delay_ms"`
}

func defaultScenario() Scenario { return Scenario{Name: "ok", RetryAfter: 2, DelayMS: 3000} }

func (s *Server) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scenarios = map[string]Scenario{}
	for _, service := range []string{"musicbrainz", "coverartarchive", "lrclib", "acoustid", "wikidata", "commons", "wikipedia", "fanarttv", "theaudiodb", "openai", "anthropic", "tavily", "searxng"} {
		s.scenarios[service] = defaultScenario()
	}
	clear(s.records[:])
	s.next, s.count = 0, 0
}

func (s *Server) setScenario(service string, sc Scenario) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.scenarios[service]; !ok {
		return fmt.Errorf("未知服务 %q", service)
	}
	switch sc.Name {
	case "ok", "not_found", "unavailable", "rate_limited", "timeout", "slow", "malformed", "drop":
	default:
		return fmt.Errorf("未知情景 %q", sc.Name)
	}
	if sc.RetryAfter < 0 || sc.DelayMS < 0 || int64(sc.DelayMS) > int64((1<<63-1)/time.Millisecond) {
		return fmt.Errorf("情景等待时间无效")
	}
	s.scenarios[service] = sc
	return nil
}

func (sc Scenario) wait(r *http.Request) bool {
	ctx := r.Context()
	switch sc.Name {
	case "timeout":
		// HTTP/1 读完请求体后才会在后台监听连接断开。
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			return false
		}
		<-ctx.Done()
		return false
	case "slow":
		timer := time.NewTimer(time.Duration(sc.DelayMS) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
		}
	}
	return true
}

func (s *Server) getScenarios(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	state := maps.Clone(s.scenarios)
	s.mu.Unlock()
	writeJSON(w, 200, state)
}

func (s *Server) putScenario(w http.ResponseWriter, r *http.Request) {
	sc := defaultScenario()
	sc.Name = ""
	if err := readJSON(w, r, &sc); err != nil {
		http.Error(w, "情景 JSON 无效", 400)
		return
	}
	if err := s.setScenario(r.PathValue("service"), sc); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	writeJSON(w, 200, sc)
}
