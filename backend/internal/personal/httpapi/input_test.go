package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/personal"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
)

func TestPersonalStrictObject(t *testing.T) {
	for _, tc := range []struct {
		name, body, code string
		empty            bool
	}{
		{"空体", "", "", true}, {"空对象", "{}", "", false}, {"空白", " \t\r\n ", "", true},
		{"缺体", "", "invalid_json", false}, {"坏JSON", "{", "invalid_json", false},
		{"尾JSON", "{}{}", "invalid_json", true}, {"尾垃圾", "{}x", "invalid_json", true},
		{"数组", "[]", "invalid_input", false}, {"null", "null", "invalid_input", false}, {"字符串", "\"a\"", "invalid_input", false},
		{"未知字段", "{\"user_id\":1}", "invalid_input", false},
		{"重复键", "{\"name\":\"a\",\"name\":\"b\"}", "invalid_input", false},
		{"转义等价键", "{\"name\":\"a\",\"na\\u006de\":\"b\"}", "invalid_input", false},
		{"嵌套重复", "{\"name\":{\"a\":1,\"a\":2}}", "invalid_input", false},
		{"超限", `{"name":"` + strings.Repeat("a", 1<<20) + `"}`, "invalid_json", false},
		{"非法UTF8", string([]byte{'{', '"', 'n', 'a', 'm', 'e', '"', ':', '"', 0xff, '"', '}'}), "invalid_json", false},
		{"合法", "{\"name\":\"中文\"}", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest("POST", "/", strings.NewReader(tc.body))
			_, ok := decodeObject(w, r, tc.empty, "name")
			if ok != (tc.code == "") {
				t.Fatalf("ok=%v body=%s", ok, w.Body)
			}
			if !ok && !strings.Contains(w.Body.String(), `"code":"`+tc.code+`"`) {
				t.Fatalf("%s", w.Body)
			}
		})
	}
	// 限额针对整个原始体，而非修剪空白后的 JSON 长度。
	for _, size := range []int{1 << 20, (1 << 20) + 1} {
		body := "{}" + strings.Repeat(" ", size-2)
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		if _, ok := decodeObject(w, r, false); ok != (size == 1<<20) {
			t.Fatalf("size %d accepted=%v", size, ok)
		}
	}
}
func TestPersonalCommandValidation(t *testing.T) {
	id := "ABCDEF00-0000-4000-8000-000000000001"
	for _, tc := range []struct {
		kind, body string
		valid      bool
	}{
		{"create", `{"name":" 中文 "}`, true}, {"create", `{"name":"😀` + strings.Repeat("中", 99) + `"}`, true},
		{"create", `{"name":"` + strings.Repeat("中", 101) + `"}`, false}, {"create", `{"name":" \t "}`, false},
		{"create", `{"name":"a\u0000b"}`, false}, {"create", `{"name":null}`, false}, {"create", `{"name":1}`, false}, {"create", `{}`, false},
		{"create", `{"name":"a","track_ids":null}`, false}, {"create", `{"name":"a","track_ids":[]}`, true},
		{"create", `{"name":"a","track_ids":[null]}`, false},
		{"rename", `{"name":"a","expected_revision":1}`, true}, {"rename", `{"name":"a","expected_revision":null}`, false},
		{"delete", `{"expected_revision":0}`, false}, {"delete", `{"expected_revision":1.0}`, false},
		{"delete", `{"expected_revision":"1"}`, false}, {"delete", `{"expected_revision":9007199254740992}`, false},
		{"delete", `{"expected_revision":9007199254740991}`, true},
		{"add_items", `{"expected_revision":1,"track_ids":[]}`, false},
		{"add_items", `{"expected_revision":1,"track_ids":["` + id + `","` + id + `"]}`, true},
		{"restore_item", `{"token":"` + id + `"}`, true}, {"restore_item", `{"token":null}`, false},
		{"move_item", `{"expected_revision":1,"action":"up"}`, true},
		{"move_item", `{"expected_revision":1,"action":"up","before_item_id":null}`, false},
		{"move_item", `{"expected_revision":1,"action":"before"}`, false},
		{"move_item", `{"expected_revision":1,"action":"before","before_item_id":null}`, true},
		{"move_item", `{"expected_revision":1,"action":"before","before_item_id":"` + id + `"}`, true},
		{"move_item", `{"expected_revision":1,"action":"before","before_item_id":1}`, false},
		{"move_item", `{"expected_revision":1,"action":"random"}`, false},
	} {
		t.Run(tc.kind+tc.body, func(t *testing.T) {
			var input object
			if err := json.Unmarshal([]byte(tc.body), &input); err != nil {
				t.Fatal(err)
			}
			command := personal.Command{Kind: tc.kind}
			if ok := decodeCommand(input, &command); ok != tc.valid {
				t.Fatalf("accepted=%v %+v", ok, command)
			}
			for _, id := range command.TrackIDs {
				if id != strings.ToLower(id) {
					t.Fatal("UUID 未规范化")
				}
			}
			if tc.kind == "add_items" && tc.valid && len(command.TrackIDs) != 2 {
				t.Fatal("输入重复计数不可提前去重")
			}
		})
	}
	for _, count := range []int{10000, 10001} {
		raw := `["` + strings.Repeat(id+`","`, count-1) + id + `"]`
		input := object{"track_ids": json.RawMessage(raw)}
		_, ok := input.ids("track_ids", true, 10000)
		if ok != (count == 10000) {
			t.Fatalf("count=%d accepted=%v", count, ok)
		}
	}
}
func TestPersonalPagination(t *testing.T) {
	for _, tc := range []struct {
		query, code string
		minimum     int64
	}{
		{"", "", 0}, {"?revision=0", "", 0}, {"?revision=0", "invalid_input", 1},
		{"?revision=9007199254740992", "invalid_input", 0}, {"?revision=1.1", "invalid_input", 0},
		{"?revision=1", "invalid_input", -1}, {"?page=0", "invalid_pagination", 0},
		{"?page_size=101", "invalid_pagination", 0}, {"?page=9223372036854775807&page_size=100", "invalid_pagination", 0},
		{"?page=1&page=2", "invalid_pagination", 0}, {"?page=", "invalid_pagination", 0},
		{"?revision=1&revision=2", "invalid_input", 0}, {"?cursor=abc", "invalid_input", 0},
		{"?x=%GG", "invalid_input", 0}, {"?user_id=someone", "invalid_input", 0},
	} {
		t.Run(fmt.Sprintf("%d%s", tc.minimum, tc.query), func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest("GET", "/"+tc.query, nil)
			_, ok := parsePage(w, r, 50, 100, tc.minimum)
			if ok != (tc.code == "") {
				t.Fatalf("ok=%v %s", ok, w.Body)
			}
			if !ok && !strings.Contains(w.Body.String(), `"code":"`+tc.code+`"`) {
				t.Fatal(w.Body)
			}
		})
	}
}
func TestPersonalGuardOrder(t *testing.T) {
	mux := http.NewServeMux()
	New(identity.NewGuard(nil, false, ""), nil).Register(mux)
	httpserver.RegisterAPIFallback(mux, httpserver.APINotFoundHandler(mux))
	for _, route := range []struct{ method, path string }{
		{"GET", "/me/favorites/tracks?page=0"}, {"GET", "/me/favorites/albums?page=0"}, {"GET", "/me/favorites/tracks/queue?cursor=x"},
		{"GET", "/me/playlists?page=0"}, {"GET", "/me/playlists/invalid"}, {"GET", "/me/playlists/invalid/items"},
		{"GET", "/me/playlists/invalid/queue"}, {"GET", "/me/playlists/containing-track/invalid"},
		{"PUT", "/me/favorites/tracks/invalid"}, {"DELETE", "/me/favorites/tracks/invalid"},
		{"PUT", "/me/favorites/albums/invalid"}, {"DELETE", "/me/favorites/albums/invalid"}, {"POST", "/me/favorites/lookup"},
		{"POST", "/me/playlists"}, {"PATCH", "/me/playlists/invalid"}, {"DELETE", "/me/playlists/invalid"},
		{"POST", "/me/playlists/invalid/items"}, {"DELETE", "/me/playlists/invalid/items/invalid"},
		{"POST", "/me/playlists/invalid/restore"}, {"POST", "/me/playlists/invalid/items/invalid/move"},
	} {
		for _, origin := range []string{"", "http://evil.example", "http://roomusic.test"} {
			r := httptest.NewRequest(route.method, "/api/v1"+route.path, strings.NewReader("{"))
			r.Host = "roomusic.test"
			r.Header.Set("Origin", origin)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			want, code := 401, "unauthorized"
			if route.method != "GET" && origin != "http://roomusic.test" {
				want = 403
				code = "origin_forbidden"
				if origin == "" {
					code = "origin_required"
				}
			}
			if w.Code != want || !strings.Contains(w.Body.String(), `"code":"`+code+`"`) {
				t.Fatalf("%s %s origin=%q: %d %s", route.method, route.path, origin, w.Code, w.Body)
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("个人响应禁止缓存")
			}
		}
	}
}
