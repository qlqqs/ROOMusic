package zones_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/dsp/chain"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/playback/render"
	"github.com/qlqq/roomusic/backend/internal/playback/render/ffmpeg"
	"github.com/qlqq/roomusic/backend/internal/zones"
	"github.com/qlqq/roomusic/backend/internal/zones/zonestest"
)

type convolutionLoadingTrackSource func(context.Context, string, string) (render.Track, error)

func (f convolutionLoadingTrackSource) Prepare(ctx context.Context, userID, trackID string) (render.Track, error) {
	return f(ctx, userID, trackID)
}

type convolutionLoadingIRSource func(context.Context, string, string, chain.Format) (render.LoadedIR, error)

func (f convolutionLoadingIRSource) Load(ctx context.Context, userID, irID string, format chain.Format) (render.LoadedIR, error) {
	return f(ctx, userID, irID, format)
}

func TestPostgreSQLZonesConvolutionLoadingDoesNotBlockCommandsOrClose(t *testing.T) {
	f := newControlFixture(t)
	ffmpegPath := os.Getenv("ROOMUSIC_TEST_FFMPEG")
	if ffmpegPath == "" {
		t.Skip("请用 scripts/ffmpeg-pinned.sh 设置 ROOMUSIC_TEST_FFMPEG")
	}
	probeCtx, cancelProbe := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelProbe()
	tool, err := ffmpeg.Open(probeCtx, ffmpegPath)
	if err != nil {
		t.Fatal(err)
	}
	cancelProbe()

	user := f.user(t, "user")
	slow := f.device(t, user, false)
	other := f.device(t, user, false)
	irID := uuid.New()
	// 预置已选资源；本例只测装载生命周期，不重复资源归属校验测试。
	f.exec(t, `UPDATE playback_zones SET convolution=jsonb_build_object(
 'enabled',true,'ir_id',$2::text,'before_peq',false) WHERE id=$1::uuid`, slow.Zone.ZoneID, irID)

	// 假渲染器仅生成 10 ms 合成 WAV 来源，不替代下方被测的真实 Renderer。
	wavSession, err := zonestest.NewRenderer(zonestest.Options{}).Open(context.Background(), zones.RenderSpec{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(wavSession.Close)
	wav := make([]byte, 44+480*2*2)
	if _, err := io.ReadFull(wavSession.Body(), wav); err != nil {
		t.Fatal(err)
	}
	wavSession.Close()
	wavPath := filepath.Join(t.TempDir(), "source.wav")
	if err := os.WriteFile(wavPath, wav, 0600); err != nil {
		t.Fatal(err)
	}
	source := convolutionLoadingTrackSource(func(ctx context.Context, userID, trackID string) (render.Track, error) {
		if userID != user.id || trackID != f.tracks[0] {
			return render.Track{}, &render.SourceError{Reason: "not_found"}
		}
		if err := ctx.Err(); err != nil {
			return render.Track{}, err
		}
		file, err := os.Open(wavPath)
		return render.Track{File: file, SourceKind: "file", Codec: "pcm_s16le", SampleRate: 48000,
			Channels: 2, BitDepth: 16, DurationSeconds: 0.01}, err
	})
	type loadCall struct {
		ctx          context.Context
		userID, irID string
		format       chain.Format
	}
	entered := make(chan loadCall, 1)
	release := make(chan struct{})
	returned := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	irs := convolutionLoadingIRSource(func(ctx context.Context, userID, irID string, format chain.Format) (render.LoadedIR, error) {
		defer close(returned)
		entered <- loadCall{ctx, userID, irID, format}
		// 即使已收到取消也不返回，区分 Close 发出取消与等待装载退出。
		<-release
		return render.LoadedIR{}, ctx.Err()
	})
	r := render.New(render.Options{FFmpeg: tool, Sources: source, IRs: irs, Clock: f.clock.current})
	t.Cleanup(r.Close)
	outputService(t, f, r)
	commandCtx, cancelCommands := context.WithCancel(context.Background())
	// 必须先解开装载，再回收 Service/Renderer，失败路径也不能卡在清理阶段。
	t.Cleanup(func() {
		cancelCommands()
		unblock()
	})
	caps := outputCapabilities()
	f.output(t, user, slow.Device.DeviceID, true, &caps)
	f.output(t, user, other.Device.DeviceID, true, &caps)

	const wait = 5 * time.Second
	type commandResult struct {
		result zones.CommandResult
		err    error
	}
	command := func(zoneID string, input zones.Command) <-chan commandResult {
		done := make(chan commandResult, 1)
		go func() {
			result, err := f.service.Command(commandCtx, user.id, zoneID, input)
			done <- commandResult{result, err}
		}()
		return done
	}
	awaitCommand := func(name string, done <-chan commandResult) zones.CommandResult {
		t.Helper()
		select {
		case got := <-done:
			if got.err != nil {
				t.Fatalf("%s 失败：%v", name, got.err)
			}
			return got.result
		case <-time.After(wait):
			t.Fatalf("IR 装载未返回时，%s 被阻塞", name)
			return zones.CommandResult{}
		}
	}
	assertLoading := func() {
		t.Helper()
		select {
		case <-returned:
			t.Fatal("IR 装载提前返回，未覆盖准备期阻塞")
		default:
		}
	}

	started := command(slow.Zone.ZoneID, zones.Command{Command: "play_tracks", TrackIDs: f.tracks[:1]})
	var loading loadCall
	select {
	case loading = <-entered:
	case <-time.After(wait):
		t.Fatal("真实渲染器未进入 IR 异步装载")
	}
	if loading.userID != user.id || loading.irID != irID || loading.format.SampleRate != 48000 || loading.format.Channels != 2 {
		t.Fatalf("IR 装载参数错误：成员=%s，IR=%s，格式=%+v", loading.userID, loading.irID, loading.format)
	}
	playing := awaitCommand("起播命令", started).Zone
	if playing.Stream == nil || playing.Stream.Format == nil || playing.Intent != "play" || playing.Fault != nil {
		t.Fatalf("准备期没有发布真实流格式：%+v", playing)
	}
	assertLoading()

	queued := awaitCommand("同成员其他播放区入队命令", command(other.Zone.ZoneID,
		zones.Command{Command: "enqueue", TrackIDs: f.tracks[1:2]})).Zone
	if queued.ZoneID != other.Zone.ZoneID || queued.QueueLength != 1 || queued.QueueRevision <= other.Zone.QueueRevision {
		t.Fatalf("其他播放区命令未生效：%+v", queued)
	}
	assertLoading()
	if err := loading.ctx.Err(); err != nil {
		t.Fatalf("其他播放区命令意外取消了 IR 装载：%v", err)
	}

	// stop 在播放区锁内调用真实 RenderSession.Close；装载仍不允许返回。
	stopped := awaitCommand("停止命令/会话 Close", command(slow.Zone.ZoneID, zones.Command{Command: "stop"})).Zone
	if stopped.Stream != nil || stopped.Intent != "pause" {
		t.Fatalf("停止未关闭准备中的流：%+v", stopped)
	}
	select {
	case <-loading.ctx.Done():
		if !errors.Is(loading.ctx.Err(), context.Canceled) {
			t.Fatalf("IR 装载收到的取消错误：%v", loading.ctx.Err())
		}
	case <-time.After(wait):
		t.Fatal("会话 Close 未将取消送达 IR 装载上下文")
	}
	assertLoading()

	closed := make(chan struct{})
	go func() {
		f.service.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(wait):
		t.Fatal("Service.Close 等待了尚未返回的 IR 装载")
	}
	assertLoading()
	unblock()
	select {
	case <-returned:
	case <-time.After(wait):
		t.Fatal("解除阻塞后 IR 装载未退出")
	}
}
