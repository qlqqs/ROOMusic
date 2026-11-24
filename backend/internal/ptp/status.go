// Package ptp 实现 AirPlay 使用的局域网双步时钟和本机只读状态接口。
package ptp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"time"

	"github.com/qlqq/roomusic/backend/internal/devices/lan"
)

const DefaultAddress = "127.0.0.1:55440"

type Status struct {
	Available     bool   `json:"available"`
	Role          string `json:"role,omitempty"`
	ClockID       uint64 `json:"clock_id"`
	GrandmasterID uint64 `json:"grandmaster_id"`
	OffsetNS      int64  `json:"offset_ns"`
	Reason        string `json:"reason,omitempty"`
}

// 接收端有时在 RECORD 后才发送 Sync。此时可以开始协商，交付音频仍需 Available。
func (s Status) CanNegotiate() bool {
	return s.ClockID != 0 && (s.Available || s.Role == "follower" && s.Reason == "clock_unlocked")
}

type Client struct{ Address string }

func loopback(raw string) (string, error) {
	if raw == "" {
		raw = DefaultAddress
	}
	ap, err := netip.ParseAddrPort(raw)
	if err != nil || !ap.Addr().Unmap().IsLoopback() || ap.Port() == 0 {
		return "", errors.New("PTP 状态地址必须为回环 IP 和有效端口")
	}
	return ap.String(), nil
}

var localHTTP = &http.Client{
	Timeout:       time.Second,
	Transport:     &http.Transport{MaxIdleConnsPerHost: 4, IdleConnTimeout: time.Minute},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func (c Client) Status(ctx context.Context) (Status, error) {
	return c.read(ctx, "")
}

func (c Client) PeerStatus(ctx context.Context, peer netip.Addr) (Status, error) {
	if !lan.IsLANAddress(peer) {
		return Status{}, errors.New("PTP 接收端必须为局域网 IPv4 地址")
	}
	return c.read(ctx, "?peer="+peer.String())
}

func (c Client) read(ctx context.Context, query string) (Status, error) {
	addr, err := loopback(c.Address)
	if err != nil {
		return Status{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/status"+query, nil)
	if err != nil {
		return Status{}, err
	}
	resp, err := localHTTP.Do(req)
	if err != nil {
		return Status{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Status{}, fmt.Errorf("PTP 状态服务返回 %d", resp.StatusCode)
	}
	var status Status
	err = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&status)
	if err == nil && status.Available && (status.ClockID == 0 || status.GrandmasterID == 0 || status.Role != "master" && status.Role != "follower") {
		err = errors.New("PTP 状态缺少有效的时钟标识或角色")
	}
	return status, err
}
