// roomusic-insights 接收匿名实例心跳并公开聚合统计。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/qlqq/roomusic/backend/internal/insights/collector"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:18110", "HTTP 监听地址")
	data := flag.String("data", "./data", "统计数据目录")
	proxy := flag.String("trusted-proxy", "", "覆盖 X-Real-IP 的反向代理对端地址")
	flag.Parse()
	var trusted []netip.Addr
	if *proxy != "" {
		peer, err := netip.ParseAddr(*proxy)
		if err != nil {
			fmt.Fprintln(os.Stderr, "反向代理地址无效")
			os.Exit(2)
		}
		trusted = append(trusted, peer.Unmap())
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *addr, *data, trusted); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, addr, dir string, trusted []netip.Addr) error {
	store, err := collector.Open(dir)
	if err != nil {
		return errors.New("无法打开统计数据")
	}
	// net/http 的默认错误日志可能包含对端地址。
	srv := &http.Server{Addr: addr, Handler: collector.NewHandler(store, trusted...), ReadTimeout: 10 * time.Second, ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, ErrorLog: log.New(io.Discard, "", 0)}
	done := make(chan error, 1)
	go func() { done <- srv.ListenAndServe() }()
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := srv.Shutdown(shutdown); err != nil {
				return errors.New("统计服务关闭超时")
			}
			return nil
		case <-done:
			return errors.New("统计服务监听失败")
		case now := <-ticker.C:
			if err := store.Prune(now); err != nil {
				fmt.Fprintln(os.Stderr, "统计数据清理失败")
			}
		}
	}
}
