package zones_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/qlqq/roomusic/backend/internal/zones"
	"github.com/qlqq/roomusic/backend/internal/zones/httpapi"
)

type groupFilter struct {
	run func(context.Context, io.ReadCloser, io.Writer) error
}

func (*groupFilter) Adjust(int64)              {}
func (*groupFilter) Source(output int64) int64 { return output }
func (*groupFilter) Pending(int64) int64       { return 0 }
func (f *groupFilter) Filter(ctx context.Context, src io.ReadCloser, dst io.Writer) error {
	return f.run(ctx, src, dst)
}

func TestZonesGroupFilterIsolatesMemberFailure(t *testing.T) {
	const audio = "synthetic FLAC"
	broken := &groupFilter{run: func(context.Context, io.ReadCloser, io.Writer) error {
		return errors.New("测试注入：成员解码失败")
	}}
	healthy := &groupFilter{run: func(_ context.Context, src io.ReadCloser, dst io.Writer) error {
		_, err := io.Copy(dst, io.MultiReader(strings.NewReader("filtered "), src))
		return err
	}}
	filters := []*groupFilter{broken, healthy}
	built := 0
	options := zones.Options{NewMemberStreamFilter: func(format zones.StreamFormat) zones.MemberStreamFilter {
		if built >= len(filters) {
			t.Fatal("创建了多余的成员过滤器")
		}
		f := filters[built]
		built++
		return f
	}}
	s, info := zones.NewTestManagedGroup(t, options, strings.NewReader(audio), "leader", "broken", "healthy")
	members := info().Members
	if built != 2 || members[0].Drift != nil || members[1].Drift != broken || members[2].Drift != healthy {
		t.Fatalf("成员过滤器分配错误：%+v", members)
	}

	for i, want := range []string{audio, "", "filtered " + audio} {
		key := strings.TrimPrefix(members[i].Path, "/upnp/streams/")
		body, err := s.OpenUPnPStream(t.Context(), key, true)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(body.Body)
		if string(data) != want || (err != nil) != (i == 1) {
			t.Fatalf("设备 %s 读到 %q，错误 %v", members[i].DeviceID, data, err)
		}
		if i == 1 {
			// 在模拟 HTTP 清理之前确认过滤器错误本身已经剔除了成员。
			if !info().Members[i].Dropped {
				t.Fatal("过滤器报错后未剔除该成员")
			}
		} else {
			body.MarkDelivered()
		}
		body.FinishDelivery()
	}
	for i, member := range info().Members {
		if member.Dropped != (i == 1) || member.Delivered != (i != 1) {
			t.Fatalf("过滤器报错后的设备状态错误：%+v", member)
		}
	}
}

func TestUPnPStreamGroupFilterStopsOnHTTPDisconnect(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		source, producer := io.Pipe()
		exited := make(chan error, 1)
		filter := &groupFilter{run: func(ctx context.Context, src io.ReadCloser, dst io.Writer) error {
			if _, err := io.WriteString(dst, "prefix"); err != nil {
				return err
			}
			_, err := io.Copy(dst, src)
			exited <- ctx.Err()
			return err
		}}
		s, info := zones.NewTestManagedGroup(t, zones.Options{
			NewMemberStreamFilter: func(zones.StreamFormat) zones.MemberStreamFilter { return filter },
		}, source, "leader", "member")
		// 源在 HTTP 断开后仍保持打开，其他成员可以继续取流。
		t.Cleanup(func() {
			producer.Close()
			source.Close()
		})
		mux := http.NewServeMux()
		httpapi.New(nil, s).Register(mux)
		ctx, disconnect := context.WithCancel(t.Context())
		defer disconnect()
		request := httptest.NewRequestWithContext(ctx, http.MethodGet, info().Members[1].Path, nil)
		request.RemoteAddr = "127.0.0.1:1234"
		response := httptest.NewRecorder()
		done := make(chan struct{})
		go func() {
			defer close(done)
			mux.ServeHTTP(response, request)
		}()
		synctest.Wait()
		if response.Code != http.StatusOK || response.Body.String() != "prefix" || !response.Flushed {
			t.Fatalf("成员未进入 HTTP 流式读取：%d %q", response.Code, response.Body.String())
		}
		disconnect()
		synctest.Wait()
		select {
		case <-done:
		default:
			t.Fatal("请求取消后 HTTP handler 未退出")
		}
		select {
		case err := <-exited:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("过滤器退出时上下文错误 = %v", err)
			}
		default:
			t.Fatal("HTTP 断开后过滤器仍等待源输入")
		}
		members := info().Members
		if !members[1].Dropped || members[1].Delivered || members[0].Dropped {
			t.Fatalf("提前断开后的设备状态错误：%+v", members)
		}
	})
}
