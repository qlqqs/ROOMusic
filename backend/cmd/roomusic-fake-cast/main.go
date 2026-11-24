package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/qlqq/roomusic/backend/internal/devices/fakecast"
)

func main() {
	var opts fakecast.Options
	addr := flag.String("addr", "0.0.0.0:8009", "CASTV2 的 TLS 监听地址，端口为 0 时自动分配")
	httpAddr := flag.String("http-addr", "127.0.0.1:0", "状态接口的 HTTP 监听地址")
	flag.StringVar(&opts.Name, "name", "ROOMusic 假 Cast 接收端", "设备名称")
	flag.StringVar(&opts.ID, "id", "", "公告中的设备标识，默认随机分配")
	flag.BoolVar(&opts.FixedVolume, "fixed-volume", false, "模拟固定音量的接收端")
	flag.Parse()
	tlsListener, err := net.Listen("tcp4", *addr)
	if err != nil {
		log.Fatal(err)
	}
	httpListener, err := net.Listen("tcp4", *httpAddr)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := fakecast.New(opts).Serve(ctx, tlsListener, httpListener, func(address, base string) {
		fmt.Printf("CASTV2 %s\nHTTP %s\n", address, base)
	}); err != nil {
		log.Fatal(err)
	}
}
