package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/personal"
)

// TestPostgreSQLPersonalHTTPPerformance 通过本机 TCP 请求真实 app；只用合成目录事实，不读取音乐文件。
func TestPostgreSQLPersonalHTTPPerformance(t *testing.T) {
	f := newPersonalFixture(t, 1)
	mustExec(t, f.app, `INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,source_kind,duration_seconds,codec)
 SELECT md5('personal-http-perf-'||i)::uuid,t.medium_id,i+1,'合成曲目','合成艺人',1,t.source_root_id,'http-perf-'||i||'.flac','present',NOW(),'physical',120,'flac'
 FROM tracks t CROSS JOIN generate_series(1,9999) i WHERE t.id=$1::uuid`, f.tracks[0])
	rows, err := f.app.database.Query(`SELECT id::text FROM tracks ORDER BY position`)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if len(ids) != 10000 {
		t.Fatalf("fixture=%d", len(ids))
	}
	p := f.create("HTTP 满额歌单", ids)
	server := httptest.NewServer(f.app.handler)
	defer server.Close()
	client := server.Client()
	client.Timeout = 2 * time.Minute
	times, decodes := []float64{}, []float64{}
	bytes := 0
	repeats := 1
	if os.Getenv("ROOMUSIC_PERSONAL_EXPLAIN_DIR") != "" {
		repeats = 5
	}
	for i := 0; i < repeats; i++ {
		request, err := http.NewRequest("GET", server.URL+playlistsURL+"/"+p.ID+"/queue", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: f.user.token})
		started := time.Now()
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		times = append(times, float64(time.Since(started))/float64(time.Millisecond))
		if response.StatusCode != 200 || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("HTTP=%d body=%s", response.StatusCode, body)
		}
		started = time.Now()
		var queue personal.PlaylistQueue
		if err = json.Unmarshal(body, &queue); err != nil {
			t.Fatal(err)
		}
		decodes = append(decodes, float64(time.Since(started))/float64(time.Millisecond))
		if len(queue.Items) != 10000 || queue.Total != 10000 || queue.ReturnedCount != 10000 || queue.OmittedCount != 0 || queue.Truncated {
			t.Fatal("queue count contract")
		}
		bytes = max(bytes, len(body))
	}
	sort.Float64s(times)
	sort.Float64s(decodes)
	report := map[string]any{"samples": repeats, "tcp_http_p50_ms": times[len(times)/2], "tcp_http_p95_ms": times[len(times)-1], "response_bytes": bytes, "go_json_decode_p50_ms": decodes[len(decodes)/2], "go_json_decode_p95_ms": decodes[len(decodes)-1], "fixture": "10000 present 曲目、一个发行、本机 TCP、真实 app handler；非浏览器 decoder"}
	t.Logf("HTTP 满额队列：%+v", report)
	if dir := os.Getenv("ROOMUSIC_PERSONAL_EXPLAIN_DIR"); dir != "" {
		if err = os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		body, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, "http-performance.json"), append(body, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
}
