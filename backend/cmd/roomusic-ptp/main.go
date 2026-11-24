package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/qlqq/roomusic/backend/internal/ptp"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := ptp.Run(ctx, os.Getenv("ROOMUSIC_PTP_ADDR"), log); err != nil {
		log.Error("PTP 服务退出", "error", err)
		os.Exit(1)
	}
}
