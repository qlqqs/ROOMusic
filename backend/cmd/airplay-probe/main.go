// airplay-probe 在隔离测试网络内按 DNS-SD 名称选择接收端并验证控制握手。
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/netip"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/devices/airplay"
	"github.com/qlqq/roomusic/backend/internal/devices/airplay/hap"
	"github.com/qlqq/roomusic/backend/internal/devices/airplay/rtsp"
)

func discover(ctx context.Context, mode, name string) (airplay.Device, error) {
	registry := airplay.NewRegistry()
	if err := registry.Start(ctx, nil); err != nil {
		return airplay.Device{}, err
	}
	defer registry.Stop()
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	for {
		for _, e := range registry.List() {
			if !e.Online || e.Name != name {
				continue
			}
			if mode == "classic" && e.RAOPAddress == "" || mode == "airplay2" && (e.AirPlayAddress == "" || e.RAOPAddress == "") {
				continue
			}
			return e.Device, nil
		}
		select {
		case <-ctx.Done():
			return airplay.Device{}, fmt.Errorf("查找 %s: %w", name, ctx.Err())
		case <-ticker.C:
		}
	}
}

func probe(ctx context.Context, mode, name string) error {
	d, err := discover(ctx, mode, name)
	if err != nil {
		return err
	}
	fmt.Printf("mDNS name=%s key=%s airplay=%s raop=%s features=%#x\n", d.Name, d.Key, d.AirPlayAddress, d.RAOPAddress, d.Features)
	target, err := netip.ParseAddrPort(d.Address)
	if err != nil {
		return err
	}
	manual, err := airplay.Probe(ctx, target.Addr().String())
	if err != nil {
		return fmt.Errorf("手动 IP 探测: %w", err)
	}
	if manual.Key != d.Key {
		return fmt.Errorf("手动 IP 解析为 %s，期待 %s", manual.Key, d.Key)
	}
	fmt.Printf("manual IP: %s matches mDNS\n", manual.Key)
	if d.UnavailableReason != "" {
		return errors.New(string(d.UnavailableReason))
	}
	addr := d.AirPlayAddress
	if mode == "classic" {
		addr = d.RAOPAddress
	}
	c, err := rtsp.Dial(ctx, addr)
	if err != nil {
		return err
	}
	defer c.Close()
	if mode == "classic" {
		return classic(ctx, c, addr)
	}
	if _, err := hap.PairTransient(ctx, c); err != nil {
		return fmt.Errorf("transient: %w", err)
	}
	fmt.Println("transient /pair-setup M4: SRP receiver proof verified")
	info, err := c.Do(ctx, "GET", "/info", nil, nil)
	if err != nil {
		return err
	}
	if err = info.CheckStatus(); err != nil {
		return err
	}
	fmt.Printf("encrypted GET /info: %d (%d bytes)\n", info.StatusCode, len(info.Body))
	setup, err := airplay.SetupSession(ctx, c, "02:00:00:00:00:01", airplay.Timing{Protocol: "PTP"})
	if err != nil {
		return err
	}
	fmt.Printf("encrypted SETUP: 200, bplist00 eventPort=%d\n", setup.EventPort)
	c.Close()
	return verifyPairing(ctx, addr)
}

// 行为参考 shairport-sync 5.6.3：该镜像接受固定测试 PIN，长期验证返回 M4。
// 加密 SETUP 在上面的 transient 连接验证，长期验证使用独立的新连接。
func verifyPairing(ctx context.Context, address string) error {
	setupConn, err := rtsp.Dial(ctx, address)
	if err != nil {
		return err
	}
	defer setupConn.Close()
	pending, err := hap.BeginPIN(ctx, setupConn)
	if err != nil {
		return err
	}
	creds, err := pending.Finish(ctx, "3939")
	if err != nil {
		return err
	}
	defer clear(creds.ControllerSeed)
	fmt.Println("persistent /pair-setup M6: receiver identity verified")
	setupConn.Close()
	verifyConn, err := rtsp.Dial(ctx, address)
	if err != nil {
		return err
	}
	defer verifyConn.Close()
	if _, err = hap.PairVerify(ctx, verifyConn, creds); err != nil {
		return err
	}
	fmt.Println("persistent /pair-verify: M4 on new TCP connection")
	return nil
}

func classic(ctx context.Context, c *rtsp.Client, addr string) error {
	resp, err := c.Do(ctx, "OPTIONS", "*", nil, nil)
	if err != nil {
		return err
	}
	if err = resp.CheckStatus(); err != nil {
		return err
	}
	methods := strings.FieldsFunc(resp.Header.Get("Public"), func(r rune) bool { return r == ',' || r == ' ' })
	if !slices.Contains(methods, "ANNOUNCE") {
		return errors.New("OPTIONS 响应未声明 ANNOUNCE")
	}
	fmt.Printf("OPTIONS: %d Public=%s\n", resp.StatusCode, resp.Header.Get("Public"))
	peer, err := netip.ParseAddrPort(addr)
	if err != nil {
		return err
	}
	local := c.LocalAddr()
	// 合成 ALAC 流描述采用 Unofficial AirPlay Specification 的 ANNOUNCE 参数。
	sdp := fmt.Sprintf("v=0\r\no=ROOMusic 1 0 IN IP4 %s\r\ns=ROOMusic\r\nc=IN IP4 %s\r\nt=0 0\r\nm=audio 0 RTP/AVP 96\r\na=rtpmap:96 AppleLossless\r\na=fmtp:96 352 0 16 40 10 14 2 255 0 0 44100\r\n", local, peer.Addr())
	resp, err = c.Do(ctx, "ANNOUNCE", "rtsp://"+local.String()+"/1", http.Header{"Content-Type": {"application/sdp"}}, []byte(sdp))
	if err != nil {
		return err
	}
	if err = resp.CheckStatus(); err != nil {
		return err
	}
	fmt.Printf("ANNOUNCE: %d\n", resp.StatusCode)
	return nil
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "advertise-homepod" {
		if err := advertiseHomePod(); err != nil {
			log.Fatal(err)
		}
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "native" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := nativePCM(ctx, os.Args[2]); err != nil {
			log.Fatal(err)
		}
		return
	}
	if len(os.Args) == 4 && os.Args[1] == "takeover" {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		if err := takeOver(ctx, os.Args[2], os.Args[3]); err != nil {
			log.Fatal(err)
		}
		return
	}
	if len(os.Args) != 3 || os.Args[1] != "airplay2" && os.Args[1] != "classic" {
		log.Fatal("用法：airplay-probe airplay2|classic 接收端名称")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()
	if err := probe(ctx, os.Args[1], os.Args[2]); err != nil {
		log.Fatal(err)
	}
}

// 从独立发送进程建立第二个真实流，让第一个发送端通过设备断开检测到接管。
func takeOver(ctx context.Context, mode, name string) error {
	device, err := discover(ctx, mode, name)
	if err != nil {
		return err
	}
	sender, err := airplay.OpenSender(ctx, device, airplay.SenderConfig{SampleRate: 44100, BitDepth: 16})
	if err != nil {
		return err
	}
	defer sender.Close()
	fmt.Printf("second sender: protocol=%s NTP fallback=%v\n", sender.Protocol(), sender.FallbackReason())
	silence := make([]byte, 352*4)
	for ctx.Err() == nil {
		if err := sender.WritePCM(ctx, silence); err != nil {
			return err
		}
	}
	return ctx.Err()
}
