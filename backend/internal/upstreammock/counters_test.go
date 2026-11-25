package upstreammock_test

import (
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"github.com/qlqq/roomusic/backend/internal/upstreammock"
)

func TestMockCountersReadAndClear(t *testing.T) {
	h := newMock(t, upstreammock.Options{})
	readCounters := func() map[string]map[string]int {
		t.Helper()
		resp := callMock(h, http.MethodGet, "/_mock/counters", "")
		requireStatus(t, resp, http.StatusOK)
		return decodeBody[map[string]map[string]int](t, resp)
	}
	if got := readCounters(); got == nil || len(got) != 0 {
		t.Fatalf("初始计数应为空，实际 %v", got)
	}
	for _, path := range []string{
		"/musicbrainz/ws/2/release?query=Glass",
		"/musicbrainz/ws/2/artist?query=Island",
		"/musicbrainz/ws/2/release/" + releaseID,
		"/musicbrainz/ws/2/isrc/QMZ9X2400001",
		"/musicbrainz/ws/2/discid/BCwmjDmGIsfPoenrs_aBAAmu1xU-",
		"/musicbrainz/ws/2/release?release-group=" + groupID,
		"/coverartarchive/release/" + releaseID,
		"/coverartarchive/release/" + releaseID + "/1",
	} {
		t.Run(path, func(t *testing.T) {
			requireStatus(t, callMock(h, http.MethodGet, path, ""), http.StatusOK)
		})
	}
	// 日志只保留最近 500 条，计数应累加所有请求，包括未命中的请求。
	for range 501 {
		requireStatus(t, callMock(h, http.MethodGet, "/musicbrainz/ws/2/release/missing", ""), http.StatusNotFound)
	}
	want := map[string]map[string]int{
		"musicbrainz":     {"search": 2, "lookup": 502, "isrc": 1, "discid": 1, "browse": 1},
		"coverartarchive": {"manifest": 1, "image": 1},
	}
	for range 2 {
		if got := readCounters(); !reflect.DeepEqual(got, want) {
			t.Fatalf("累计计数 = %v，期望 %v", got, want)
		}
	}
	requireStatus(t, callMock(h, http.MethodDelete, "/_mock/counters", ""), http.StatusNoContent)
	if got := readCounters(); got == nil || len(got) != 0 {
		t.Fatalf("清零后的计数 = %v，期望为空", got)
	}
	requireStatus(t, callMock(h, http.MethodGet, "/coverartarchive/release/"+releaseID, ""), http.StatusOK)
	want = map[string]map[string]int{"coverartarchive": {"manifest": 1}}
	if got := readCounters(); !reflect.DeepEqual(got, want) {
		t.Fatalf("清零后的新计数 = %v，期望 %v", got, want)
	}
}

func TestMockRateThreshold(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newMock(t, upstreammock.Options{})
		lookup := "/musicbrainz/ws/2/release/" + releaseID
		search := "/musicbrainz/ws/2/release?query=Glass"
		cover := "/coverartarchive/release/" + releaseID
		check := func(path string, status int, retryAfter string) {
			t.Helper()
			resp := callMock(h, http.MethodGet, path, "")
			requireStatus(t, resp, status)
			if got := resp.Header().Get("Retry-After"); got != retryAfter {
				t.Fatalf("%s 的 Retry-After = %q，期望 %q", path, got, retryAfter)
			}
		}

		check(lookup, http.StatusOK, "")
		check(lookup, http.StatusOK, "")

		const interval = time.Second
		const scenario = `{"scenario":"rate_threshold","min_interval_ms":%d,"retry_after":7}`
		for _, service := range []string{"musicbrainz", "coverartarchive"} {
			requireStatus(t, callMock(h, http.MethodPut, "/_mock/scenarios/"+service,
				fmt.Sprintf(scenario, interval.Milliseconds())), http.StatusOK)
		}
		check(lookup, http.StatusOK, "")
		time.Sleep(interval - time.Millisecond)
		check(search, http.StatusServiceUnavailable, "7")
		check(cover, http.StatusOK, "")
		check(cover, http.StatusServiceUnavailable, "7")
		time.Sleep(time.Millisecond)
		check(lookup, http.StatusServiceUnavailable, "7")
		time.Sleep(interval)
		check(lookup, http.StatusOK, "")
		time.Sleep(interval + time.Millisecond)
		check(search, http.StatusOK, "")
	})
}
