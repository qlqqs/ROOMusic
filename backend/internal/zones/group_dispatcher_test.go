package zones

import (
	"bytes"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestZonesGroupDispatcherDropsOnlySlowReader(t *testing.T) {
	d := newGroupDispatcher([]string{"fast", "slow"}, StreamFormat{SampleRate: 96000, Channels: 2, BitDepth: 16})
	t.Cleanup(d.close)
	fast, slow := d.readers[0], d.readers[1]
	fast.taken, slow.taken = true, true
	source, writer := io.Pipe()
	t.Cleanup(func() {
		source.Close()
		writer.Close()
	})
	pumped := make(chan struct{})
	go func() {
		defer close(pumped)
		d.pump(source)
	}()

	finished := make(chan error, 1)
	go func() {
		want, got := make([]byte, groupBlockSize), make([]byte, groupBlockSize)
		// 每块由快读者读完才送下一块，慢读者的累计积压则会越过上限。
		for offset := int64(0); offset <= d.limit+groupBlockSize; offset += groupBlockSize {
			for i := range want {
				want[i] = byte(int64(i) + offset/groupBlockSize)
			}
			if _, err := writer.Write(want); err != nil {
				finished <- err
				return
			}
			if _, err := io.ReadFull(fast, got); err != nil {
				finished <- err
				return
			}
			if !bytes.Equal(got, want) {
				finished <- errors.New("快读者收到的流字节不匹配")
				return
			}
		}
		writer.Close()
		_, err := fast.Read(got)
		finished <- err
	}()

	select {
	case err := <-finished:
		if err != io.EOF {
			t.Fatalf("快读者读至 EOF：%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("慢读者阻塞了源或快读者")
	}
	select {
	case <-pumped:
	case <-time.After(5 * time.Second):
		t.Fatal("源结束后 pump 仍未退出")
	}
	select {
	case id := <-d.drops:
		if id != slow.id {
			t.Fatalf("剔除 %q，期望 %q", id, slow.id)
		}
	default:
		t.Fatal("未报告慢读者被剔除")
	}
	if n, err := slow.Read(make([]byte, 1)); n != 0 || !errors.Is(err, ErrStreamGone) {
		t.Fatalf("慢读者被剔除后读取 = %d, %v", n, err)
	}
	if len(d.drops) != 0 {
		t.Fatal("快读者也被剔除")
	}
	select {
	case <-fast.done:
		t.Fatal("快读者的 Done 被关闭")
	default:
	}
}

func TestZonesGroupDispatcherExpiresUnconnectedReader(t *testing.T) {
	d := newGroupDispatcher([]string{"connected", "absent"}, StreamFormat{})
	t.Cleanup(d.close)
	connected, absent := d.readers[0], d.readers[1]
	connected.taken = true
	d.expire()
	d.expire()

	select {
	case id := <-d.drops:
		if id != absent.id {
			t.Fatalf("超时剔除 %q，期望 %q", id, absent.id)
		}
	default:
		t.Fatal("未报告未连接读者超时")
	}
	select {
	case id := <-d.drops:
		t.Fatalf("重复报告超时：%q", id)
	default:
	}
	select {
	case <-absent.done:
	default:
		t.Fatal("未关闭超时读者的 Done")
	}
	if n, err := absent.Read(make([]byte, 1)); n != 0 || !errors.Is(err, ErrStreamGone) {
		t.Fatalf("超时读者读取 = %d, %v", n, err)
	}
	const audio = "synthetic audio"
	d.pump(strings.NewReader(audio))
	if got, err := io.ReadAll(connected); err != nil || string(got) != audio {
		t.Fatalf("已连接读者读取 = %q, %v", got, err)
	}
}

func TestZonesGroupDispatcherCloseEndsReaders(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newGroupDispatcher([]string{"leader", "member"}, StreamFormat{})
		results := make(chan error, len(d.readers))
		for _, reader := range d.readers {
			reader.taken = true
			go func() {
				_, err := reader.Read(make([]byte, 1))
				results <- err
			}()
		}
		synctest.Wait()
		if len(results) != 0 {
			t.Fatal("关闭前读者已结束")
		}
		d.close()
		for _, reader := range d.readers {
			body := UPnPStreamBody{reader: reader, delivered: &reader.delivered}
			body.FinishDelivery()
		}
		if len(d.drops) != 0 {
			t.Fatalf("分发器关闭后结束 HTTP 交付不应报告剔除，收到 %q", <-d.drops)
		}
		for range d.readers {
			err := <-results
			if err != io.EOF {
				t.Fatalf("关闭后读取错误 = %v，期望 EOF", err)
			}
		}
	})
}

type groupTestRenderSession struct {
	RenderSession
	source io.Reader
}

func (s groupTestRenderSession) Body() io.Reader { return s.source }
func (groupTestRenderSession) HLS() HLSMedia     { return nil }
func (groupTestRenderSession) Format() StreamFormat {
	return StreamFormat{SampleRate: 48000, Channels: 2, BitDepth: 16}
}
func (groupTestRenderSession) Close() {}

// NewTestManagedGroup 让 HTTP 层测试使用真实的托管输出和分发器。
func NewTestManagedGroup(t *testing.T, options Options, source io.Reader, ids ...string) (*Service, func() GroupStreamInfo) {
	t.Helper()
	s := NewService(nil, options)
	t.Cleanup(s.Close)
	userID, sessionID := uuid.New(), uuid.New()
	h := &renderHandle{
		groupIDs: ids,
		session:  groupTestRenderSession{source: source},
		stream:   &Stream{Seq: 1, MimeType: "audio/flac"},
		done:     make(chan struct{}),
	}
	z := &zoneRuntime{device: PersistedDevice{DeviceID: ids[0], Kind: "slimproto"}, currentHandle: h}
	out := &outputSession{id: sessionID, userID: userID, zone: z, managed: true, state: "active"}
	z.output = out
	s.outputs[sessionID] = out
	t.Cleanup(func() {
		z.mu.Lock()
		h.close()
		z.mu.Unlock()
	})

	return s, func() GroupStreamInfo {
		t.Helper()
		info, err := s.ManagedGroupStream(userID, sessionID, 1)
		if err != nil || len(info.Members) != len(ids) {
			t.Fatalf("组流信息 = %+v, %v", info, err)
		}
		return info
	}
}

func TestZonesManagedGroupStreamReadersAndDelivery(t *testing.T) {
	ids := []string{"leader", "member-1", "member-2", "member-3"}
	audio := bytes.Repeat([]byte("fLaC\x00\xffgroup"), 9000)
	s, streamInfo := NewTestManagedGroup(t, Options{}, bytes.NewReader(audio), ids...)
	members := streamInfo().Members
	seen := make(map[string]bool)
	readers := make([]UPnPStreamBody, len(members))
	for i, member := range members {
		key, ok := strings.CutPrefix(member.Path, "/upnp/streams/")
		if member.DeviceID != ids[i] || member.Delivered || member.Dropped || !ok || key == "" || seen[key] {
			t.Fatalf("成员 %d 的初始流信息无效：%+v", i, member)
		}
		seen[key] = true
		head, err := s.OpenUPnPStream(t.Context(), key, false)
		if err != nil || head.Body != nil || head.MimeType != "audio/flac" {
			t.Fatalf("成员 %s HEAD：%+v, %v", member.DeviceID, head, err)
		}
		reader, err := s.OpenUPnPStream(t.Context(), key, true)
		if err != nil {
			t.Fatal(err)
		}
		readers[i] = reader
		if _, err := s.OpenUPnPStream(t.Context(), key, true); !errors.Is(err, ErrStreamGone) {
			t.Fatalf("成员 %s 二次 GET = %v，期望 %v", member.DeviceID, err, ErrStreamGone)
		}
	}
	if got := streamInfo().Members; !slices.Equal(got, members) {
		t.Fatalf("重复查询更改了流信息：%+v", got)
	}

	type readResult struct {
		index int
		audio []byte
		err   error
	}
	results := make(chan readResult, len(readers))
	for i, reader := range readers {
		go func() {
			data, err := io.ReadAll(reader.Body)
			results <- readResult{i, data, err}
		}()
	}
	for range readers {
		select {
		case result := <-results:
			if result.err != nil || !bytes.Equal(result.audio, audio) {
				t.Fatalf("成员 %s 读取 %d 字节（期望 %d）：%v", ids[result.index], len(result.audio), len(audio), result.err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("并发读者未读至 EOF")
		}
	}
	for i := len(readers) - 1; i >= 0; i-- {
		readers[i].MarkDelivered()
		readers[i].FinishDelivery()
		for j, member := range streamInfo().Members {
			if member.Delivered != (j >= i) || member.Dropped {
				t.Fatalf("成员 %s 交付完成后，成员 %s 的状态为 %+v", ids[i], ids[j], member)
			}
		}
	}
}
