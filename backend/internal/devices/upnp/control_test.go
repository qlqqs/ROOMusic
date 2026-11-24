package upnp

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/qlqq/roomusic/backend/internal/devices/fakerenderer"
)

const testUDN = "uuid:roomusic-test"
const testDescription = "http://192.168.10.2/description.xml"

type descriptionTransport struct {
	handler http.Handler
	offline atomic.Bool
}

func (f *descriptionTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if f.offline.Load() {
		return nil, errors.New("合成设备离线")
	}
	if req.URL.Hostname() != "192.168.10.2" {
		return nil, errors.New("测试响应仅用于合成设备")
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, req)
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	return w.Result(), nil
}

func fakeDescriptionTransport(t *testing.T, wrap func(http.Handler) http.Handler) *descriptionTransport {
	t.Helper()
	renderer, err := fakerenderer.New(fakerenderer.Options{UDN: testUDN})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(renderer.Close)
	handler := renderer.Handler()
	if wrap != nil {
		handler = wrap(handler)
	}
	f := &descriptionTransport{handler: handler}
	original := deviceHTTP.Transport
	deviceHTTP.Transport = f
	t.Cleanup(func() { deviceHTTP.Transport = original })
	return f
}

func TestDescriptionRejectsDisallowedLocations(t *testing.T) {
	fakeDescriptionTransport(t, func(http.Handler) http.Handler {
		return http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("拒绝的地址触发了 HTTP 请求") })
	})
	for _, tc := range []struct{ name, host, peer string }{
		{"other host", "192.168.10.3", "192.168.10.2"},
		{"loopback", "127.0.0.1", "127.0.0.1"},
		{"public", "8.8.8.8", "8.8.8.8"},
		{"multicast", "239.255.255.250", "239.255.255.250"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DiscoverDescription(t.Context(), "http://"+tc.host+"/description.xml", netip.MustParseAddr(tc.peer))
			if !errors.Is(err, ErrDeviceAddress) {
				t.Fatalf("地址错误 = %v", err)
			}
			if tc.name != "other host" {
				_, err = ManualDescription(t.Context(), "http://"+tc.host+"/description.xml")
				if !errors.Is(err, ErrDeviceAddress) {
					t.Fatalf("手动地址错误 = %v", err)
				}
			}
		})
	}
}

func TestDescriptionRejectsCrossHostEndpoints(t *testing.T) {
	for _, tc := range []struct{ name, from, to string }{
		{"URLBase", "<device>", "<URLBase>http://192.168.10.3/</URLBase><device>"},
		{"SCPD", "<SCPDURL>/", "<SCPDURL>http://192.168.10.3/"},
		{"control", "<controlURL>/", "<controlURL>http://192.168.10.3/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeDescriptionTransport(t, func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					if req.URL.Path != "/description.xml" {
						t.Errorf("拒绝的描述仍触发了服务请求 %s", req.URL.Path)
						return
					}
					original := httptest.NewRecorder()
					next.ServeHTTP(original, req)
					body := original.Body.String()
					if !strings.Contains(body, tc.from) {
						t.Errorf("设备描述缺少测试替换项 %s", tc.from)
					}
					_, _ = io.WriteString(w, strings.ReplaceAll(body, tc.from, tc.to))
				})
			})
			if _, err := ManualDescription(t.Context(), testDescription); !errors.Is(err, ErrDeviceAddress) {
				t.Fatalf("跨主机地址错误 = %v", err)
			}
		})
	}
}

func TestDeviceResponseRedirectAndLimits(t *testing.T) {
	fakeDescriptionTransport(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			switch req.URL.Path {
			case "/redirect":
				http.Redirect(w, req, "/redirect-target", http.StatusFound)
			case "/redirect-target":
				t.Error("HTTP 重定向被跟随")
			case "/large":
				_, _ = io.WriteString(w, strings.Repeat("x", 256*1024+1))
			case "/control/AVTransport":
				_, _ = io.WriteString(w, strings.Repeat("x", 64*1024+1))
			default:
				next.ServeHTTP(w, req)
			}
		})
	})
	for _, tc := range []struct{ path, want string }{
		{"/redirect", "HTTP 302"}, {"/large", "超过 262144 字节"},
	} {
		_, err := ManualDescription(t.Context(), "http://192.168.10.2"+tc.path)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s 错误 = %v，期望 %q", tc.path, err, tc.want)
		}
	}
	d, err := ManualDescription(t.Context(), testDescription)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Stop(t.Context()); err == nil || !strings.Contains(err.Error(), "超过 65536 字节") {
		t.Fatalf("超大 SOAP 响应错误 = %v", err)
	}
}

func TestDeviceRequestTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fakeDescriptionTransport(t, func(http.Handler) http.Handler {
			return http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
				<-req.Context().Done()
			})
		})
		begin := time.Now()
		_, err := ManualDescription(t.Context(), testDescription)
		if elapsed := time.Since(begin); elapsed != 5*time.Second || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("设备请求等待 %s，错误 %v", elapsed, err)
		}
	})
}

func removeSCPDAction(body, action string) string {
	start := strings.Index(body, "<action><name>"+action+"</name>")
	if start < 0 {
		return body
	}
	_, remainder, _ := strings.Cut(body[start:], "</action>")
	return body[:start] + remainder
}

func TestDeviceOptionalActionsAndProtocolFormats(t *testing.T) {
	for _, tc := range []struct {
		name, sink, codec string
		declared          bool
	}{
		{"flac preferred", "http-get:*:audio/wav:*,http-get:*:audio/x-flac:*", "flac", true},
		{"wav fallback", "http-get:*:audio/L16;rate=44100:*", "wav", true},
		{"undeclared", "http-get:*:audio/mpeg:*", "flac", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var pausedAs string
			fakeDescriptionTransport(t, func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					original := httptest.NewRecorder()
					next.ServeHTTP(original, req)
					body := original.Body.String()
					switch req.URL.Path {
					case "/scpd/AVTransport":
						body = removeSCPDAction(body, "Pause")
					case "/scpd/RenderingControl":
						body = removeSCPDAction(body, "SetVolume")
					case "/control/ConnectionManager":
						body = strings.ReplaceAll(body, "http-get:*:audio/flac:*,http-get:*:audio/wav:*", tc.sink)
					case "/control/AVTransport":
						pausedAs = req.Header.Get("SOAPACTION")
					}
					w.WriteHeader(original.Code)
					_, _ = io.WriteString(w, body)
				})
			})
			d, err := ManualDescription(t.Context(), testDescription)
			if err != nil {
				t.Fatal(err)
			}
			c := d.Capabilities
			if c.Pause || c.Volume != "fixed" || c.FormatDeclared != tc.declared ||
				!reflect.DeepEqual(c.StreamFormats, []string{tc.codec}) || c.MaxSampleRate != 48000 || c.MaxBitDepth != 24 {
				t.Fatalf("设备能力 = %+v", c)
			}
			if err := d.Pause(t.Context()); err != nil || !strings.HasSuffix(pausedAs, "#Stop\"") {
				t.Fatalf("暂停替代动作 = %s，错误 %v", pausedAs, err)
			}
			if err := d.SetVolume(t.Context(), 25); !errors.Is(err, ErrUnsupportedAction) {
				t.Fatalf("固定音量错误 = %v", err)
			}
		})
	}
}

func TestDeviceVolumeConversionAndDIDL(t *testing.T) {
	var desired string
	fakeDescriptionTransport(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if strings.Contains(req.Header.Get("SOAPACTION"), "#SetVolume") {
				raw, _ := io.ReadAll(req.Body)
				var envelope struct {
					Value string `xml:"Body>SetVolume>DesiredVolume"`
				}
				if err := xml.Unmarshal(raw, &envelope); err != nil {
					t.Errorf("解码音量 SOAP: %v", err)
				}
				desired = envelope.Value
				_, _ = io.WriteString(w, `<Envelope><Body><SetVolumeResponse/></Body></Envelope>`)
				return
			}
			original := httptest.NewRecorder()
			next.ServeHTTP(original, req)
			body := original.Body.String()
			if req.URL.Path == "/scpd/RenderingControl" {
				body = strings.Replace(body, "<minimum>0</minimum><maximum>100</maximum><step>1</step>", "<minimum>10</minimum><maximum>90</maximum><step>5</step>", 1)
			}
			w.WriteHeader(original.Code)
			_, _ = io.WriteString(w, body)
		})
	})
	d, err := ManualDescription(t.Context(), testDescription)
	if err != nil {
		t.Fatal(err)
	}
	if percent, err := d.GetVolume(t.Context()); err != nil || percent != 50 {
		t.Fatalf("读取音量 = %d，错误 %v", percent, err)
	}
	for _, tc := range []struct {
		pct  int
		want string
	}{{0, "10"}, {63, "60"}, {100, "90"}} {
		if err := d.SetVolume(t.Context(), tc.pct); err != nil || desired != tc.want {
			t.Fatalf("设置 %d%% 得到 %q，期望 %q，错误 %v", tc.pct, desired, tc.want, err)
		}
	}

	metadata := TrackMetadata{Title: "海 & 🌊", Artist: "艺人", Album: "唱片", URI: "http://192.168.10.2:8080/track?a=1&b=2", ArtURI: "http://192.168.10.2:8080/封面", MIMEType: "audio/flac"}
	raw := BuildDIDL(metadata)
	for _, r := range raw {
		if r > 127 {
			t.Fatalf("DIDL 含原样非 ASCII 字符 %q", r)
		}
	}
	var parsed struct {
		Title  string `xml:"item>title"`
		Artist string `xml:"item>artist"`
		Album  string `xml:"item>album"`
		URI    string `xml:"item>res"`
		ArtURI string `xml:"item>albumArtURI"`
	}
	if err := xml.Unmarshal([]byte(raw), &parsed); err != nil || parsed.Title != metadata.Title ||
		parsed.Artist != metadata.Artist || parsed.Album != metadata.Album || parsed.URI != metadata.URI || parsed.ArtURI != metadata.ArtURI {
		t.Fatalf("DIDL 往返 = %+v，错误 %v\n%s", parsed, err, raw)
	}
}
