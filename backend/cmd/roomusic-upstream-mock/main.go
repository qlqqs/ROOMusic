package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/qlqq/roomusic/backend/internal/upstreammock"
)

func main() {
	var opts upstreammock.Options
	opts.Scenarios = map[string]string{}
	addr := flag.String("addr", "127.0.0.1:18090", "HTTP 监听地址")
	flag.StringVar(&opts.DataDir, "data", "", "合成数据目录")
	flag.StringVar(&opts.ScriptFile, "script", "", "模型脚本 JSON 文件")
	flag.Func("scenario", "初始情景 service=name，可重复", func(value string) error {
		service, name, ok := strings.Cut(value, "=")
		if !ok {
			return fmt.Errorf("情景参数需要 service=name")
		}
		opts.Scenarios[service] = name
		return nil
	})
	flag.Parse()
	handler, err := upstreammock.New(opts)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }}
	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("外部接口 mock 已监听 %s", listener.Addr())
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	select {
	case err := <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			log.Fatal(err)
		}
	}
}
