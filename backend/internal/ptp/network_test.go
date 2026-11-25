package ptp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestPTPRunReopensBothPortsAfterLANAddressChange(t *testing.T) {
	links, err := lanLinks()
	if err != nil {
		t.Fatal(err)
	}
	var index int
	var initial netip.Addr
	for index, initial = range links {
		break
	}
	if !initial.IsValid() {
		t.Skip("测试需要局域网 IPv4 组播接口")
	}
	for _, port := range []int{319, 320} {
		conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: port})
		if errors.Is(err, syscall.EADDRINUSE) || errors.Is(err, syscall.EACCES) {
			t.Skipf("无法绑定 PTP 测试端口 %d：%v", port, err)
		}
		if err != nil {
			t.Fatal(err)
		}
		conn.Close()
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()

	oldList, oldInterval := listLinks, linkScanInterval
	t.Cleanup(func() { listLinks, linkScanInterval = oldList, oldInterval })
	linkScanInterval = 100 * time.Millisecond
	var changed atomic.Bool
	listLinks = func() (map[int]netip.Addr, error) {
		if changed.Load() {
			// 同一网卡使用合成的新地址，实际组播仍通过其网卡索引加入。
			return map[int]netip.Addr{index: initial.Next()}, nil
		}
		return map[int]netip.Addr{index: initial}, nil
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	events := make(ptpTestEvents, 16)
	logger := slog.New(slog.NewJSONHandler(events, nil))
	var runErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		runErr = Run(ctx, address, logger)
	}()
	defer func() { cancel(); <-done }()
	waitEvent := func(want string) {
		t.Helper()
		for {
			select {
			case record := <-events:
				var event struct {
					Name  string `json:"event"`
					Error string `json:"error"`
				}
				if err := json.Unmarshal(record, &event); err != nil {
					t.Fatal(err)
				}
				switch event.Name {
				case want:
					return
				case "ptp.unavailable":
					t.Fatalf("等待 %s 时 PTP 端口不可用：%s", want, event.Error)
				}
			case <-done:
				t.Fatalf("等待 %s 时 PTP 服务退出：%v", want, runErr)
			case <-ctx.Done():
				t.Fatalf("没有收到 %s 事件", want)
			}
		}
	}
	waitEvent("ptp.ready")
	changed.Store(true)
	waitEvent("ptp.interfaces.changed")
	waitEvent("ptp.ready")
}

type ptpTestEvents chan []byte

func (events ptpTestEvents) Write(p []byte) (int, error) {
	select {
	case events <- append([]byte(nil), p...):
	default:
	}
	return len(p), nil
}
