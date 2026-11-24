package main

import (
	"context"
	"net"
	"os/signal"
	"syscall"

	"github.com/qlqq/roomusic/backend/internal/devices/mdns"
)

// 模拟一条只支持 PTP 的 HomePod 公告，用来验证管理员和播放区的设备状态。
func advertiseHomePod() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	socket, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353})
	if err != nil {
		return err
	}
	local := socket.LocalAddr().(*net.UDPAddr).AddrPort().Addr().Unmap()
	socket.Close()
	return mdns.Advertise(ctx, mdns.Entry{
		Service: "_airplay._tcp.local.", Instance: "ROOMusic-Synthetic-HomePod",
		Host: "roomusic-homepod.local.", IP: local, Port: 7000,
		TXT: map[string]string{"deviceid": "02:00:00:00:00:99", "model": "AudioAccessory5,1", "features": "0x0,0x10000"},
	})
}
