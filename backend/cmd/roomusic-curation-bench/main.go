// roomusic-curation-bench 用公开发行数据测量 MusicBrainz 并生成静音曲库。
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/musicbrainz"
)

//go:embed corpus.json
var manifest []byte

type corpusEntry struct {
	ID, Group, Title, Artist, Category, Tags string
	LocalTitle                               string `json:"local_title"`
	Tolerance                                bool
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("用法：roomusic-curation-bench mb-rate [-n 60] | tolerance [发行组编号...] | real-corpus -out 目录 [-ffmpeg 路径]")
	}
	var corpus struct{ Releases []corpusEntry }
	if err := json.Unmarshal(manifest, &corpus); err != nil {
		return err
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	switch args[0] {
	case "mb-rate":
		n := flags.Int("n", 60, "连续读取次数")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *n < 1 || flags.NArg() != 0 {
			return errors.New("mb-rate 需要 -n 正整数")
		}
		return measureRate(ctx, corpus.Releases, *n)
	case "tolerance":
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		return measureTolerance(sources.WithBackground(ctx), corpus.Releases, flags.Args())
	case "real-corpus":
		out := flags.String("out", "", "生成曲库的目录")
		ffmpeg := flags.String("ffmpeg", os.Getenv("ROOMUSIC_FFMPEG_PATH"), "ffmpeg 路径，默认使用 ROOMUSIC_FFMPEG_PATH 或 PATH")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *out == "" || flags.NArg() != 0 {
			return errors.New("real-corpus 需要 -out 目录")
		}
		return generateCorpus(sources.WithBackground(ctx), corpus.Releases, *out, *ffmpeg)
	default:
		return fmt.Errorf("未知子命令 %q", args[0])
	}
}

func writeResult(result any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}

// 读取客户端记录的实际响应码，业务错误码会将 503 和其它上游错误归为同一类。
type rateLog struct{ statuses map[int]int }

func (h rateLog) Enabled(context.Context, slog.Level) bool { return true }
func (h rateLog) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h rateLog) WithGroup(string) slog.Handler            { return h }
func (h rateLog) Handle(_ context.Context, record slog.Record) error {
	record.Attrs(func(a slog.Attr) bool {
		if a.Key == "status" {
			h.statuses[int(a.Value.Int64())]++
			return false
		}
		return true
	})
	return nil
}

func measureRate(ctx context.Context, corpus []corpusEntry, n int) error {
	log := rateLog{statuses: map[int]int{}}
	// 前台调用没有自动重试；不设数据目录，每次都经过生产限速器实际出站。
	client := musicbrainz.New(musicbrainz.Options{Logger: slog.New(log)})
	start := time.Now()
	success := 0
	for i := range n {
		id := corpus[i%len(corpus)].ID
		if _, err := client.Lookup(ctx, "release", id, nil); err == nil {
			success++
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	elapsed := time.Since(start).Seconds()
	return writeResult(map[string]any{
		"requests": n, "success": success, "http_503": log.statuses[503],
		"other_errors": n - success - log.statuses[503], "http_statuses": log.statuses,
		"elapsed_seconds": elapsed, "success_per_second": float64(success) / elapsed,
		"user_agent": sources.UserAgent(),
	})
}
