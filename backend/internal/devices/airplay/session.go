package airplay

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/devices/airplay/bplist"
	"github.com/qlqq/roomusic/backend/internal/devices/airplay/rtsp"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// Timing 给出会话声明的时钟类型。NTP 使用发送端已打开的计时端口。
type Timing struct {
	Protocol string
	Port     uint16
	ClockID  uint64
}

type SessionSetup struct {
	URI       string
	UUID      string
	EventPort uint16
}

// SetupSession 在已配对的连接上协商会话并取得接收端事件端口。
// 行为参考 pyatv（MIT）的会话字段与 shairport-sync 的 PTP 接收端响应。
func SetupSession(ctx context.Context, c *rtsp.Client, deviceID string, timing Timing) (SessionSetup, error) {
	var result SessionSetup
	mac, err := net.ParseMAC(deviceID)
	if err != nil || len(mac) != 6 {
		return result, errors.New("SETUP 需要六字节发送端设备标识")
	}
	deviceID = strings.ToUpper(mac.String())
	sessionID := strings.ToUpper(uuid.New())
	var streamID [4]byte
	rand.Read(streamID[:])
	local := c.LocalAddr().String()
	uri := fmt.Sprintf("rtsp://%s/%d", local, binary.BigEndian.Uint32(streamID[:]))
	params := map[string]any{
		"deviceID": deviceID, "macAddress": deviceID, "sessionUUID": sessionID,
		"name": "ROOMusic", "model": "ROOMusic", "sourceVersion": strings.TrimPrefix(rtsp.UserAgent, "AirPlay/"),
		"timingProtocol": timing.Protocol, "isMultiSelectAirPlay": true,
	}
	switch timing.Protocol {
	case "NTP":
		if timing.Port == 0 {
			return result, errors.New("NTP 会话需要计时端口")
		}
		params["timingPort"] = timing.Port
	case "PTP":
		peerInfo := map[string]any{"Addresses": []any{local}, "ID": sessionID, "DeviceType": 0}
		if timing.ClockID != 0 {
			peerInfo["ClockID"] = int64(timing.ClockID)
		}
		params["timingPeerInfo"] = peerInfo
		params["timingPeerList"] = []any{peerInfo}
		params["groupUUID"] = sessionID
		params["groupContainsGroupLeader"] = true
	default:
		return result, errors.New("未知的 AirPlay 计时协议")
	}
	body, err := bplist.Marshal(params)
	if err != nil {
		return result, err
	}
	h := http.Header{"Content-Type": {"application/x-apple-binary-plist"}, "X-Apple-StreamID": {"1"}}
	resp, err := c.Do(ctx, "SETUP", uri, h, body)
	if err != nil {
		return result, err
	}
	if resp.StatusCode != 200 {
		return result, fmt.Errorf("SETUP: %w", &rtsp.StatusError{Code: resp.StatusCode})
	}
	result.URI, result.UUID = uri, sessionID
	var reply map[string]any
	if err = bplist.Unmarshal(resp.Body, &reply); err != nil {
		return result, fmt.Errorf("SETUP 响应: %w", err)
	}
	port, ok := reply["eventPort"].(uint64)
	if !ok || port < 1 || port > 65535 {
		return result, errors.New("SETUP 响应缺少有效的事件端口")
	}
	return SessionSetup{UUID: sessionID, URI: uri, EventPort: uint16(port)}, nil
}
