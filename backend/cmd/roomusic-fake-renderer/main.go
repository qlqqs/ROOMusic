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

	"github.com/qlqq/roomusic/backend/internal/devices/fakerenderer"
)

func main() {
	var opts fakerenderer.Options
	addr := flag.String("addr", "0.0.0.0:0", "HTTP 监听地址，端口为 0 时自动分配")
	flag.StringVar(&opts.Name, "name", "ROOMusic 假渲染器", "设备名称")
	flag.StringVar(&opts.RelTime, "rel-time", "normal", "播放位置行为 normal 或 zero")
	flag.Parse()
	r, err := fakerenderer.New(opts)
	if err != nil {
		log.Fatal(err)
	}
	l, err := net.Listen("tcp4", *addr)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := r.Serve(ctx, l, func(base string) { fmt.Println(base) }); err != nil {
		log.Fatal(err)
	}
}
