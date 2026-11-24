package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"net"
	"net/netip"
	"time"

	"github.com/qlqq/roomusic/backend/internal/devices/airplay"
)

// 固定旧镜像的接收端容器名由本次隔离网络解析。它只验证原生实时音频路径。
func nativePCM(ctx context.Context, host string) error {
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", host)
	if err != nil {
		return err
	}
	if len(ips) == 0 {
		return fmt.Errorf("未找到接收端 %s", host)
	}
	device := airplay.Device{Info: airplay.Info{AirPlayAddress: netip.AddrPortFrom(ips[0].Unmap(), 7000).String()}}
	s, err := airplay.OpenSender(ctx, device, airplay.SenderConfig{SampleRate: 44100, BitDepth: 16})
	if err != nil {
		return err
	}
	defer s.Close()
	if s.Protocol() != "airplay2" || s.FallbackReason() != nil {
		return fmt.Errorf("期望原生 AirPlay 2 协商，实际 %s", s.Protocol())
	}
	const frames = 352
	data := make([]byte, frames*4)
	for packet := range 500 {
		for n := range frames {
			phase := 2 * math.Pi * float64((packet*frames+n)%98) / 98
			binary.LittleEndian.PutUint16(data[n*4:], uint16(int16(math.Round(12000*math.Sin(phase)))))
			binary.LittleEndian.PutUint16(data[n*4+2:], uint16(int16(math.Round(12000*math.Cos(phase)))))
		}
		if err = s.WritePCM(ctx, data); err != nil {
			return err
		}
	}
	drain, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	if err = s.Drain(drain); err != nil {
		return err
	}
	fmt.Printf("native NTP session: protocol=%s sample_rate=44100 bit_depth=16 frames=%d\n", s.Protocol(), s.FramesSent())
	return nil
}
