package app

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/artwork"
)

func upnpRouteRequest(t *testing.T, f *upnpFixture, method, path, peer string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = peer
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, req)
	return w
}

func TestPostgreSQLUPnPStreamTokens(t *testing.T) {
	r := newUPnPTestRenderer(t, "normal")
	r.holdAudio.Store(true)
	f := newUPnPFixture(t, true)
	f.enable(t)
	f.add(t, r)
	a, b := f.createUser(t, "user"), f.createUser(t, "user")
	da, db := f.device(t, &a, r), f.device(t, &b, r)
	track := f.tracks(t, 40)
	var releaseID string
	if err := f.database.QueryRow("SELECT media.release_id FROM tracks JOIN media ON tracks.medium_id=media.id WHERE tracks.id=$1", track[0]).Scan(&releaseID); err != nil {
		t.Fatal(err)
	}
	var cover bytes.Buffer
	if err := png.Encode(&cover, image.NewNRGBA(image.Rect(0, 0, 3, 2))); err != nil {
		t.Fatal(err)
	}
	if err := artwork.NewStore(f.dataDirectory).Save(t.Context(), f.database, releaseID, cover.Bytes(), "embedded"); err != nil {
		t.Fatal(err)
	}
	f.play(t, &a, da, track)
	f.wait(t, 8*time.Second, "获取设备流 URI", func() bool { return r.State().URI != "" })
	uri := r.State().URI
	path := strings.TrimPrefix(uri, f.server.URL)
	if !strings.HasPrefix(path, "/upnp/streams/") {
		t.Fatalf("设备流地址 = %q", uri)
	}
	peer := "192.168.10.2:9000"
	requireAPIError(t, upnpRouteRequest(t, f, "GET", "/upnp/streams/", peer), 404, "not_found")
	requireAPIError(t, upnpRouteRequest(t, f, "GET", path, "192.0.2.44:9000"), 404, "not_found")
	for i := 0; i < 2; i++ {
		audioHead := upnpRouteRequest(t, f, "HEAD", path, peer)
		if audioHead.Code != 200 || audioHead.Body.Len() != 0 || audioHead.Header().Get("Content-Type") != "audio/flac" ||
			audioHead.Header().Get("transferMode.dlna.org") != "Streaming" || audioHead.Header().Get("Accept-Ranges") != "none" {
			t.Fatalf("流 HEAD = %d %v，长度 %d", audioHead.Code, audioHead.Header(), audioHead.Body.Len())
		}
	}
	head := upnpRouteRequest(t, f, "HEAD", path+"/artwork", peer)
	get := upnpRouteRequest(t, f, "GET", path+"/artwork", peer)
	if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("Content-Type") != "image/png" ||
		get.Code != 200 || !bytes.Equal(get.Body.Bytes(), cover.Bytes()) {
		t.Fatalf("流封面 HEAD = %d %v (%d)，GET = %d (%d)", head.Code, head.Header(), head.Body.Len(), get.Code, get.Body.Len())
	}
	if !strings.Contains(r.State().Metadata, path+"/artwork") {
		t.Fatalf("DIDL 未引用令牌封面 %s", r.State().Metadata)
	}

	readFLAC := func(ctx context.Context) *http.Response {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, "GET", uri, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := f.server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = resp.Body.Close() })
		var magic [4]byte
		if resp.StatusCode != 200 {
			t.Fatalf("取流响应 %d", resp.StatusCode)
		}
		if _, err := io.ReadFull(resp.Body, magic[:]); err != nil || string(magic[:]) != "fLaC" ||
			resp.Header.Get("Accept-Ranges") != "none" || !slices.Contains(resp.TransferEncoding, "chunked") {
			t.Fatalf("取流头 = %q，传输编码 = %v，错误 %v", magic, resp.TransferEncoding, err)
		}
		return resp
	}
	f.wait(t, 8*time.Second, "播放区流已建立", func() bool {
		z := f.zone(t, &a, da)
		return z.Status == "playing" && z.Stream != nil
	})
	first := f.zone(t, &a, da).Stream.Seq
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	old := readFLAC(ctx)
	if got := f.zone(t, &a, da).Stream.Seq; got != first {
		t.Fatalf("首次 GET 前 HEAD 或封面已消费音频，seq 从 %d 变为 %d", first, got)
	}
	f.wait(t, 3*time.Second, "重连前有播放进度", func() bool { return f.zone(t, &a, da).Position.Seconds >= .5 })
	before := f.zone(t, &a, da).Position.Seconds
	readFLAC(ctx)
	z := f.zone(t, &a, da)
	if z.Stream == nil || z.Stream.Seq <= first || z.Position.Seconds < before-.2 ||
		z.Stream.Segments[0].TrackStartSeconds < before-.2 || f.zone(t, &b, db).Stream != nil {
		t.Fatalf("重连后播放区 = %+v，重连前 seq = %d，位置 = %f", z, first, before)
	}
	if _, err := io.Copy(io.Discard, old.Body); ctx.Err() != nil {
		t.Fatalf("旧连接未在重连后结束: %v", err)
	}
	f.command(t, &a, da, map[string]any{"command": "seek", "position_seconds": 12})
	f.wait(t, 5*time.Second, "跳转生成另一个令牌", func() bool { return r.State().URI != uri })
	for _, suffix := range []string{"", "/artwork"} {
		requireAPIError(t, upnpRouteRequest(t, f, "GET", path+suffix, peer), 404, "not_found")
	}
	for i := 1; i <= 30; i++ {
		miss := upnpRouteRequest(t, f, "GET", "/upnp/streams/expired", "192.168.10.9:9000")
		if i == 30 {
			requireTooManyAttempts(t, miss, "60")
		} else {
			requireAPIError(t, miss, 404, "not_found")
		}
	}
	valid := strings.TrimPrefix(r.State().URI, f.server.URL)
	requireTooManyAttempts(t, upnpRouteRequest(t, f, "HEAD", valid, "192.168.10.9:9001"), "60")
	if got := upnpRouteRequest(t, f, "HEAD", valid, peer); got.Code != 200 {
		t.Fatalf("另一来源的新令牌 HEAD = %d", got.Code)
	}
}
