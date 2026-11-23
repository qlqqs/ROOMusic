package acoustid

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/egress"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

const emptyLookup = `{"status":"ok","results":[]}`

func lookupOptions(t *testing.T, handler http.HandlerFunc) Options {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return Options{
		BaseURL: server.URL, Limiter: sources.NewLimiter(0, nil, nil), DataDirectory: t.TempDir(),
		NewClient: func(egress.Options) (*http.Client, error) { return server.Client(), nil },
		Keys: func(context.Context) (map[string]string, error) {
			return map[string]string{"application": "fixture-app", "user": "fixture-user"}, nil
		},
	}
}

func lookupForm(t *testing.T, r *http.Request) url.Values {
	t.Helper()
	if r.Method != "POST" || r.URL.Path != "/v2/lookup" || r.URL.RawQuery != "" ||
		r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || r.Header.Get("Content-Encoding") != "gzip" {
		t.Errorf("请求 = %s %s, Content-Type=%q, Content-Encoding=%q", r.Method, r.URL, r.Header.Get("Content-Type"), r.Header.Get("Content-Encoding"))
	}
	body, err := gzip.NewReader(r.Body)
	if err != nil {
		t.Error(err)
		return nil
	}
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		t.Error(err)
		return nil
	}
	form, err := url.ParseQuery(string(data))
	if err != nil {
		t.Error(err)
	}
	return form
}

func TestLookupFingerprintRequestMappingAndCache(t *testing.T) {
	expectedForm := url.Values{
		"client": {"fixture-app"}, "user": {"fixture-user"}, "duration": {"120"},
		"fingerprint": {"test+print/="}, "meta": {"recordings releases"}, "format": {"json"},
	}
	var outbound atomic.Int32
	opts := lookupOptions(t, func(w http.ResponseWriter, r *http.Request) {
		outbound.Add(1)
		if got := lookupForm(t, r); !reflect.DeepEqual(got, expectedForm) {
			t.Errorf("表单=%v，期望 %v", got, expectedForm)
		}
		io.WriteString(w, `{"status":"ok","results":[
			{"id":"low","score":0.91,"recordings":[{"id":"one","title":"低分重复"},{"id":"two","title":"第二首"}]},
			{"id":"high","score":0.98,"recordings":[{"id":"one","title":"第一首","duration":120.25,"artists":[{"id":"art","name":"合成艺人"}],
			"releases":[{"id":"rel","title":"发行","country":"JP","date":{"year":2024,"month":3,"day":9},"track_count":12,"medium_count":2}]},{"id":"one"}]},
			{"id":"empty","score":1,"recordings":[{"title":"无 ID"}]}
		]}`)
	})
	keyCalls := 0
	keys := map[string]string{"application": "fixture-app", "user": "fixture-user"}
	opts.Keys = func(context.Context) (map[string]string, error) {
		keyCalls++
		return keys, nil
	}
	fp := sources.Fingerprint{Value: "test+print/=", DurationSeconds: 120}
	call := func() []sources.FingerprintMatch {
		t.Helper()
		rows, err := New(opts).LookupFingerprint(t.Context(), fp)
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	rows := call()
	if len(rows) != 2 || rows[0].ID != "high" || rows[0].Score != 0.98 || rows[1].ID != "low" || rows[1].Score != 0.91 ||
		len(rows[0].Recordings) != 1 || len(rows[1].Recordings) != 1 || rows[1].Recordings[0].ID != "two" {
		t.Fatalf("分数或去重错误：%+v", rows)
	}
	recording := rows[0].Recordings[0]
	wantRelease := sources.FingerprintRelease{ID: "rel", Title: "发行", Country: "JP", Date: "2024-03-09", TrackCount: 12, MediumCount: 2, Artists: []sources.Credit{}}
	if recording.ID != "one" || recording.Title != "第一首" || recording.LengthMS == nil || *recording.LengthMS != 120250 ||
		!reflect.DeepEqual(recording.Artists, []sources.Credit{{Name: "合成艺人", ArtistID: "art"}}) ||
		!reflect.DeepEqual(recording.ReleaseIDs, []string{"rel"}) || !reflect.DeepEqual(recording.Releases, []sources.FingerprintRelease{wantRelease}) {
		t.Fatalf("录音映射错误：%+v", recording)
	}
	for _, name := range []string{"", "application", "user"} {
		if name != "" {
			keys[name] += "-new"
		}
		if cached := call(); !reflect.DeepEqual(cached, rows) {
			t.Errorf("缓存结果=%+v，期望 %+v", cached, rows)
		}
	}
	if outbound.Load() != 1 || keyCalls != 1 {
		t.Fatalf("缓存期间出站=%d，读取密钥=%d，均期望 1", outbound.Load(), keyCalls)
	}
}

func TestLookupFingerprintGlobalInterval(t *testing.T) {
	times := make(chan time.Time, 2)
	opts := lookupOptions(t, func(w http.ResponseWriter, _ *http.Request) {
		times <- time.Now()
		io.WriteString(w, emptyLookup)
	})
	opts.Limiter = nil
	for _, value := range []string{"first", "second"} {
		_, err := New(opts).LookupFingerprint(sources.WithBackground(t.Context()), sources.Fingerprint{Value: value, DurationSeconds: 20})
		if err != nil {
			t.Fatal(err)
		}
	}
	first, second := <-times, <-times
	if interval := second.Sub(first); interval < 334*time.Millisecond {
		t.Fatalf("两次出站间隔 %s，小于 334ms", interval)
	}
}

func TestLookupFingerprintBackoff(t *testing.T) {
	for _, failure := range []struct{ status, code int }{{429, 14}, {503, 13}, {200, 14}, {200, 13}} {
		for _, exhausted := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d_%d_耗尽_%t", failure.status, failure.code, exhausted), func(t *testing.T) {
				var outbound atomic.Int32
				opts := lookupOptions(t, func(w http.ResponseWriter, r *http.Request) {
					if form := lookupForm(t, r); form.Get("fingerprint") != "retry" || len(form) != 6 {
						t.Errorf("重试请求表单=%v", form)
					}
					n := outbound.Add(1)
					if exhausted || n == 1 {
						w.WriteHeader(failure.status)
						fmt.Fprintf(w, `{"status":"error","error":{"code":%d}}`, failure.code)
						return
					}
					io.WriteString(w, emptyLookup)
				})
				var delays []time.Duration
				opts.After = func(d time.Duration) <-chan time.Time {
					delays = append(delays, d)
					ch := make(chan time.Time, 1)
					ch <- time.Time{}
					return ch
				}
				got, err := New(opts).LookupFingerprint(sources.WithBackground(t.Context()), sources.Fingerprint{Value: "retry", DurationSeconds: 20})
				wantAttempts, wantErr := int32(2), error(nil)
				if exhausted {
					wantAttempts, wantErr = 5, sources.ErrTryLater
				}
				if !errors.Is(err, wantErr) || len(got) != 0 || outbound.Load() != wantAttempts || len(delays) != int(wantAttempts)-1 {
					t.Fatalf("结果=%v 错误=%v 出站=%d 退避=%v", got, err, outbound.Load(), delays)
				}
				for i, delay := range delays {
					base := time.Second << (i + 1)
					if delay < base || delay >= base+base/2 {
						t.Errorf("第 %d 次退避=%s，期望 [%s, %s)", i+1, delay, base, base+base/2)
					}
				}
			})
		}
	}
}

func TestLookupFingerprintErrors(t *testing.T) {
	for _, tc := range []struct {
		code int
		name string
	}{{4, "application"}, {17, "application"}, {6, "user"}, {3, ""}, {8, ""}} {
		t.Run(fmt.Sprint(tc.code), func(t *testing.T) {
			var outbound atomic.Int32
			client := New(lookupOptions(t, func(w http.ResponseWriter, _ *http.Request) {
				if outbound.Add(1) == 1 {
					w.WriteHeader(400)
					fmt.Fprintf(w, `{"status":"error","error":{"code":%d}}`, tc.code)
					return
				}
				io.WriteString(w, emptyLookup)
			}))
			ctx := sources.WithBackground(t.Context())
			fp := sources.Fingerprint{Value: "invalid", DurationSeconds: 20}
			rows, err := client.LookupFingerprint(ctx, fp)
			if len(rows) != 0 || outbound.Load() != 1 {
				t.Fatalf("结果=%v 出站=%d", rows, outbound.Load())
			}
			if tc.name == "" {
				// 无效指纹作为空结果进入程序层的无命中路径。
				if !errors.Is(err, sources.ErrInvalidQuery) {
					t.Errorf("错误=%v，期望 ErrInvalidQuery", err)
				}
				return
			}
			var invalid *sources.InvalidKeyError
			if !errors.Is(err, sources.ErrInvalidKey) || !errors.As(err, &invalid) || invalid.Name != tc.name {
				t.Errorf("无效密钥错误=%+v，期望 %s", err, tc.name)
			}
			if _, err = client.LookupFingerprint(ctx, fp); err != nil || outbound.Load() != 2 {
				t.Fatalf("紧接着查询错误=%v 出站=%d，期望成功且出站两次", err, outbound.Load())
			}
		})
	}
}
