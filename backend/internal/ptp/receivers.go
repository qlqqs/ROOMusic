package ptp

import (
	"net"
	"strings"
)

type ReceiverTiming struct {
	Required      bool
	ProvidesClock bool
	wakesSender   bool
}

// 行为参考 saundo 的单台 HomePod、Mac 对时记录和 OwnTone 的 Era 100 设备报告。
// 型号来自 DNS-SD；分组成员是否提供时钟另由 tsid 判断。
var receiverTimingProfiles = [...]struct {
	modelPrefix string
	timing      ReceiverTiming
}{
	{"audioaccessory", ReceiverTiming{Required: true, ProvidesClock: true}},
	{"homepod", ReceiverTiming{Required: true, ProvidesClock: true}},
	{"mac", ReceiverTiming{ProvidesClock: true}},
	{"imac", ReceiverTiming{ProvidesClock: true}},
	{"era ", ReceiverTiming{Required: true}},
	{"sonos era ", ReceiverTiming{Required: true}},
	{"shairportsync", ReceiverTiming{wakesSender: true}},
}

// 行为参考 nqptp 发行说明：它用接收端的 EUI-64 短暂参与主钟选举，唤醒发送端。
func (r ReceiverTiming) wakeClockID(deviceID string) uint64 {
	if !r.wakesSender {
		return 0
	}
	mac, err := net.ParseMAC(deviceID)
	if err != nil || len(mac) != 6 {
		return 0
	}
	id := [8]byte{mac[0], mac[1], mac[2], 0xff, 0xfe, mac[3], mac[4], mac[5]}
	return be.Uint64(id[:])
}

func ReceiverProfile(model string) ReceiverTiming {
	model = strings.ToLower(strings.TrimSpace(model))
	for _, row := range receiverTimingProfiles {
		if strings.HasPrefix(model, row.modelPrefix) {
			return row.timing
		}
	}
	return ReceiverTiming{}
}
