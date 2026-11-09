package zones_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/zones"
	"github.com/qlqq/roomusic/backend/internal/zones/zonestest"
)

// 分片保留策略由 render 的测试覆盖；这里仅让媒体端口返回删除后的领域错误，核对 HTTP 映射。
type hlsHTTPMedia struct{ segments map[int64][]byte }

func (m *hlsHTTPMedia) Playlist(context.Context) ([]byte, error) {
	return []byte("#EXTM3U\n#EXT-X-START:TIME-OFFSET=0,PRECISE=YES\n#EXT-X-MAP:URI=\"init.mp4\"\n#EXTINF:4.000,\n0.m4s\n#EXT-X-ENDLIST\n"), nil
}
func (m *hlsHTTPMedia) Init() ([]byte, error) { return []byte("初始化段"), nil }
func (m *hlsHTTPMedia) Segment(n int64) ([]byte, error) {
	if data, ok := m.segments[n]; ok {
		return data, nil
	}
	return nil, zones.ErrSegmentMissing
}

type hlsHTTPSession struct {
	zones.RenderSession
	media  *hlsHTTPMedia
	format zones.StreamFormat
}

func (s *hlsHTTPSession) HLS() zones.HLSMedia        { return s.media }
func (s *hlsHTTPSession) Format() zones.StreamFormat { return s.format }
func (s *hlsHTTPSession) MimeType() string           { return s.format.MimeType }
func (s *hlsHTTPSession) Body() io.Reader            { return bytes.NewReader(nil) }

type hlsHTTPRenderer struct {
	*outputRenderer
	media *hlsHTTPMedia
}

func (r *hlsHTTPRenderer) Negotiate(zones.Capabilities, string) (zones.StreamFormat, error) {
	return zones.StreamFormat{Codec: "alac", MimeType: "application/vnd.apple.mpegurl", Quality: "lossless", Delivery: "hls", SampleRate: 44100, BitDepth: 16, Channels: 2}, nil
}
func (r *hlsHTTPRenderer) Open(ctx context.Context, spec zones.RenderSpec) (zones.RenderSession, error) {
	s, err := r.outputRenderer.Open(ctx, spec)
	if err != nil {
		return nil, err
	}
	return &hlsHTTPSession{RenderSession: s, media: r.media, format: spec.Format}, nil
}

func TestPostgreSQLZonesHLSHTTP(t *testing.T) {
	f := newControlFixture(t)
	media := &hlsHTTPMedia{segments: map[int64][]byte{0: []byte("分片")}}
	r := &hlsHTTPRenderer{outputRenderer: &outputRenderer{Renderer: zonestest.NewRenderer(zonestest.Options{Now: f.clock.current})}, media: media}
	outputService(t, f, r)
	user := f.user(t, "user")
	device := f.device(t, user, false)
	caps := outputCapabilities()
	caps.StreamFormats = []zones.StreamFormatCapability{{Codec: "alac", MIMEType: "application/vnd.apple.mpegurl", Lossless: true}}
	claim := controlDecode[zones.ClaimOutputResult](t, f.request(t, user, "POST", "/devices/"+device.Device.DeviceID+"/output", zones.ClaimOutputInput{Protocol: 1, Capabilities: caps}, controlOrigin), 200)
	z := f.command(t, user, device.Zone.ZoneID, map[string]any{"command": "play_tracks", "track_ids": f.tracks[:2], "start_index": 0}).Zone
	if z.Stream == nil {
		t.Fatal("起播没有生成流")
	}
	path := func(seq int64, file string) string {
		return "/zones/" + z.ZoneID + "/streams/" + strconv.FormatInt(seq, 10) + "/" + file
	}
	get := func(seq int64, file, encoding string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, controlPrefix+path(seq, file), nil)
		request.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: user.token})
		if encoding != "" {
			request.Header.Set("Accept-Encoding", encoding)
		}
		response := httptest.NewRecorder()
		f.handler.ServeHTTP(response, request)
		return response
	}
	want, err := media.Playlist(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	checkPlaylist := func(seq int64) {
		t.Helper()
		for range 2 {
			response := get(seq, "index.m3u8", "")
			if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), want) {
				t.Fatalf("重复取列表 = %d %s", response.Code, response.Body.String())
			}
		}
	}
	current := z.Stream.Seq
	checkPlaylist(current)
	entries := f.queue(t, user, z.ZoneID).Entries
	sub := outputSubscribe(t, f, user)
	outputEmit(t, r.Sessions()[0], zones.Event{Kind: "format_boundary", NextEntryID: entries[1].EntryID, Reason: "format"})
	next := outputZone(t, sub, func(z zones.Zone) bool { return z.NextStream != nil }).NextStream.Seq
	checkPlaylist(next)
	controlError(t, get(current-1, "index.m3u8", ""), 410, "stream_gone")
	controlError(t, get(next+1, "index.m3u8", ""), 410, "stream_gone")
	compressed := get(current, "index.m3u8", "gzip")
	if compressed.Code != 200 || compressed.Header().Get("Content-Encoding") != "gzip" || compressed.Header().Get("Vary") != "Accept-Encoding" {
		t.Fatalf("gzip 响应 = %d %v", compressed.Code, compressed.Header())
	}
	reader, err := gzip.NewReader(compressed.Body)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	decoded, err := io.ReadAll(reader)
	if err != nil || !bytes.Equal(decoded, want) {
		t.Fatalf("gzip 解码后 = %q / %v", decoded, err)
	}
	if response := get(current, "0.m4s", ""); response.Code != 200 {
		t.Fatalf("删除前分片 = %d %s", response.Code, response.Body.String())
	}
	delete(media.segments, 0)
	controlError(t, get(current, "0.m4s", ""), 404, "not_found")
	renewed := outputReport(t, f, user, claim.SessionID, current, "fault", "loading", 0)
	if renewed.Zone.Stream == nil || renewed.Zone.Stream.Seq == current {
		t.Fatal("续流未更换序号")
	}
	controlError(t, get(current, "index.m3u8", ""), 410, "stream_gone")
	controlError(t, get(next, "index.m3u8", ""), 410, "stream_gone")
}
