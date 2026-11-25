package upnp

import (
	"context"
	"errors"
	"io"
	"net"
	"syscall"
	"time"

	"github.com/qlqq/roomusic/backend/internal/devices/airplay"
	"github.com/qlqq/roomusic/backend/internal/devices/airplay/rtsp"
	"github.com/qlqq/roomusic/backend/internal/ptp"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

// 一次只向渲染器预读一个 RTP 包。暂停时保留尚未发送的块，换流则丢弃旧块。
type pcmBlock struct {
	bytes []byte
	err   error
}
type pcmSource struct {
	body   io.ReadCloser
	blocks chan pcmBlock
	done   chan struct{}
	stop   context.CancelFunc
}

func readPCM(parent context.Context, body io.ReadCloser, blockBytes int) *pcmSource {
	ctx, cancel := context.WithCancel(parent)
	source := &pcmSource{body: body, blocks: make(chan pcmBlock), done: make(chan struct{}), stop: cancel}
	go func() {
		defer close(source.done)
		for {
			data := make([]byte, blockBytes)
			n, err := io.ReadFull(body, data)
			if errors.Is(err, io.ErrUnexpectedEOF) {
				err = io.EOF
			}
			select {
			case source.blocks <- pcmBlock{bytes: data[:n], err: err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	return source
}
func (p *pcmSource) close(ended bool) {
	p.stop()
	// 已读至自然流尾的会话继续按播放头派发事件，晋升接续流时由 zones 关闭。
	if !ended {
		p.body.Close()
	}
	<-p.done
}

type airplayOutput struct {
	*deviceOutput
	clock          *ptp.Client
	receiver       airplay.Device
	credentials    *airplay.CredentialStore
	registry       deviceRegistry
	sender         *airplay.Sender
	source         *pcmSource
	drained        chan error
	stopDrain      context.CancelFunc
	rate, bits     int
	startFrame     uint64
	accepted       uint64
	audibleSeq     *int64
	audibleStart   uint64
	awaitingFrames bool
	paused, eof    bool
	metadataEntry  string
	volumeSent     *float64
}

func (p *airplayOutput) run(sub *zones.Subscription) {
	err := p.serve(sub)
	seq, seconds := p.heardPosition()
	position := zones.ReleaseOutputInput{StreamSeq: seq, StreamSeconds: seconds}
	if err != nil && p.ctx.Err() == nil {
		p.manager.logger.Warn("AirPlay 输出中断", "event", "airplay.output.disconnected", "udn", p.receiver.Key, "error", err)
		row, online := p.registry.Lookup(p.receiver.Key)
		if p.seq != nil && p.zone.Intent == "play" && online && row.Online && connectionLost(err) {
			// 接收端被其他发送端接管时，控制连接会关闭；由当前持有者发布设备端暂停。
			if _, pauseErr := p.manager.zones.CommandFromOutput(p.ctx, p.actor.ID, p.sessionID, "pause"); pauseErr != nil {
				p.manager.logger.Warn("AirPlay 设备端暂停回报失败", "zone_id", p.zoneID, "error", pauseErr)
			}
		}
	}
	p.cancel()
	p.closeSource()
	p.closeSender()
	p.release(position)
}

func connectionLost(err error) bool {
	var status *rtsp.StatusError
	if errors.As(err, &status) {
		return status.Code == 454
	}
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE)
}

func (p *airplayOutput) serve(sub *zones.Subscription) error {
	defer func() { sub.Close() }()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		var packets <-chan pcmBlock
		if !p.paused && !p.eof && p.source != nil {
			packets = p.source.blocks
		}
		var disconnected <-chan struct{}
		var fallbacks <-chan error
		if p.sender != nil {
			disconnected = p.sender.Done()
			fallbacks = p.sender.Fallbacks()
		}
		var subscriptionClosed bool
		select {
		case <-p.ctx.Done():
			return nil
		case cause := <-fallbacks:
			p.manager.logger.Warn("AirPlay 输出已改用 NTP 计时", "event", "airplay.output.fallback", "module", "airplay", "zone_id", p.zoneID, "udn", p.receiver.Key, "error", cause)
		case <-disconnected:
			if err := p.sender.Err(); err != nil {
				return err
			}
			return io.EOF
		case <-ticker.C:
			row, ok := p.registry.Lookup(p.receiver.Key)
			if !ok || !row.Online {
				return errors.New("AirPlay 接收端已离线")
			}
			if p.seq != nil && p.sender != nil {
				if err := p.reportPosition("status"); err != nil {
					return err
				}
			}
		case <-sub.Done:
			subscriptionClosed = true
		case msg, ok := <-sub.Events:
			if !ok {
				subscriptionClosed = true
				break
			}
			if p.receive(msg) {
				if err := p.applyState(); err != nil && !errors.Is(err, zones.ErrStreamGone) {
					return err
				}
			}
		case block := <-packets:
			if len(block.bytes) > 0 {
				if len(block.bytes)%(2*p.bits/8) != 0 {
					return errors.New("渲染 PCM 尾块未对齐音频帧")
				}
				if err := p.sender.WritePCM(p.ctx, block.bytes); err != nil {
					return err
				}
				p.accepted += uint64(len(block.bytes) / (2 * p.bits / 8))
				if err := p.reportFirstFrame(); err != nil {
					return err
				}
			}
			if block.err != nil {
				if !errors.Is(block.err, io.EOF) {
					return block.err
				}
				p.eof = true
				if err := p.applyState(); err != nil && !errors.Is(err, zones.ErrStreamGone) {
					return err
				}
			}
		case err := <-p.drained:
			p.stopDrain()
			p.stopDrain, p.drained = nil, nil
			if err != nil {
				return err
			}
			// 很短的接续流可能在两次定时回报之间播完，先晋升已出声的流再收尾。
			if err = p.reportFirstFrame(); err != nil {
				return err
			}
			if p.zone.NextStream != nil && p.zone.NextStream.Seq != *p.seq {
				if err = p.startStream(p.zone.NextStream, true); err != nil && !errors.Is(err, zones.ErrStreamGone) {
					return err
				}
			} else {
				if err = p.reportPosition("ended"); err != nil {
					return err
				}
				if err = p.applyState(); err != nil && !errors.Is(err, zones.ErrStreamGone) {
					return err
				}
			}
		}
		if subscriptionClosed {
			next, err := p.resubscribe()
			if err != nil {
				return err
			}
			sub = next
		}
	}
}

func (p *airplayOutput) closeSource() {
	p.cancelDrain()
	if p.source != nil {
		p.source.close(p.eof)
		p.source = nil
	}
}
func (p *airplayOutput) cancelDrain() {
	if p.stopDrain != nil {
		p.stopDrain()
		<-p.drained
		p.stopDrain, p.drained = nil, nil
	}
}
func (p *airplayOutput) closeSender() {
	if p.sender != nil {
		p.sender.Close()
		p.sender = nil
	}
	p.volumeSent, p.metadataEntry = nil, ""
	p.paused = false
}

func (p *airplayOutput) applyState() error {
	z := p.zone
	if z.Stream == nil {
		p.closeSource()
		p.closeSender()
		p.seq, p.audibleSeq = nil, nil
		return nil
	}
	if z.Intent == "pause" {
		if p.sender != nil && !p.paused {
			p.cancelDrain()
			if err := p.sender.Pause(p.ctx); err != nil {
				return err
			}
			p.paused = true
			if err := p.reportPosition("status"); err != nil {
				return err
			}
		}
		return p.updateControls()
	}
	if p.seq == nil || (*p.seq != z.Stream.Seq && (z.NextStream == nil || *p.seq != z.NextStream.Seq)) {
		return p.startStream(z.Stream, false)
	}
	if p.paused {
		if err := p.sender.Resume(p.ctx); err != nil {
			return err
		}
		p.paused = false
		p.volumeSent, p.metadataEntry = nil, ""
		if err := p.reportPosition("status"); err != nil {
			return err
		}
	}
	if p.eof {
		if z.NextStream != nil && z.NextStream.Seq != *p.seq {
			return p.startStream(z.NextStream, true)
		}
		if p.stopDrain == nil && p.sender != nil {
			ctx, cancel := context.WithCancel(p.ctx)
			done := make(chan error, 1)
			p.stopDrain, p.drained = cancel, done
			go func(sender *airplay.Sender) { done <- sender.Drain(ctx) }(p.sender)
		}
	}
	return p.updateControls()
}

func (p *airplayOutput) startStream(stream *zones.Stream, continuation bool) error {
	if stream.Format == nil {
		return nil
	}
	rate, bits := stream.Format.SampleRate, stream.Format.BitDepth
	if bits == nil || (*bits != 16 && *bits != 24) || (rate != 44100 && rate != 48000) || stream.Format.Channels != 2 {
		return errors.New("AirPlay 渲染流格式无效")
	}
	reader, err := p.manager.zones.OpenAirPlayPCM(p.actor.ID, p.sessionID, stream.Seq)
	if err != nil {
		return err
	}
	p.closeSource()
	if p.sender != nil && (p.rate != rate || p.bits != *bits) {
		p.closeSender()
	}
	if p.sender == nil {
		p.sender, err = airplay.OpenSender(p.ctx, p.receiver, airplay.SenderConfig{SampleRate: rate, BitDepth: *bits, Credentials: p.credentials, PTP: p.clock, RequiredPTP: p.receiver.PTPRequired})
		if err == nil {
			p.manager.logger.Info("AirPlay 输出已连接", "event", "airplay.output.connected", "udn", p.receiver.Key, "protocol", p.sender.Protocol(), "timing", p.sender.TimingProtocol(), "sample_rate", rate, "bit_depth", *bits, "fallback", p.sender.FallbackReason())
			p.accepted = 0
		}
	} else if !continuation {
		err = p.sender.Flush(p.ctx)
		p.accepted = p.sender.FramesSent()
	}
	if err != nil {
		reader.Close()
		return err
	}
	if p.paused {
		if err = p.sender.Resume(p.ctx); err != nil {
			reader.Close()
			return err
		}
		p.paused = false
	}
	if continuation && p.seq != nil && p.accepted > 0 {
		p.audibleSeq, p.audibleStart = p.seq, p.startFrame
	} else {
		p.audibleSeq = nil
	}
	p.awaitingFrames = true
	p.eof = false
	p.rate, p.bits = rate, *bits
	p.seq = &stream.Seq
	p.startFrame = p.accepted
	p.source = readPCM(p.ctx, reader, 352*2*p.bits/8)
	if err := p.reportPosition("status"); err != nil {
		return err
	}
	return p.updateControls()
}

// 跨渲染流边界时，发送器已开始写下一首，接收端仍可能在播上一首。
func (p *airplayOutput) heardPosition() (*int64, float64) {
	if p.sender == nil || p.seq == nil {
		return nil, 0
	}
	played := p.sender.PlayedFrames()
	seq, origin := p.seq, p.startFrame
	if played <= p.startFrame && p.audibleSeq != nil {
		seq, origin = p.audibleSeq, p.audibleStart
	}
	if played <= origin {
		return seq, 0
	}
	return seq, float64(played-origin) / float64(p.rate)
}

func (p *airplayOutput) reportFirstFrame() error {
	if !p.awaitingFrames || p.sender.PlayedFrames() <= p.startFrame {
		return nil
	}
	if err := p.reportPosition("status"); err != nil {
		return err
	}
	return p.updateControls()
}

func (p *airplayOutput) reportPosition(kind string) error {
	seq, seconds := p.heardPosition()
	status := "playing"
	if p.paused {
		status = "paused"
	} else if p.awaitingFrames && seq == p.seq && seconds == 0 {
		status = "buffering"
	}
	if seq == p.seq && seconds > 0 {
		p.awaitingFrames = false
		p.audibleSeq = nil
	}
	r, err := p.manager.zones.ReportOutput(p.ctx, p.actor.ID, p.sessionID, zones.ReportOutputInput{Kind: kind, Status: status, StreamSeq: seq, StreamSeconds: seconds})
	if err == nil {
		p.zone = r.Zone
	}
	return err
}

func (p *airplayOutput) updateControls() error {
	if p.sender == nil {
		return nil
	}
	if level, ok := p.manager.zones.ManagedOutputVolume(p.actor.ID, p.sessionID); ok && (p.volumeSent == nil || *p.volumeSent != level) {
		if err := p.sender.SetVolume(p.ctx, level, level == 0); err != nil {
			return err
		}
		p.volumeSent = &level
	}
	current := p.zone.Current
	if current == nil || current.EntryID == p.metadataEntry {
		return nil
	}
	meta := airplay.Metadata{Title: current.Track.Title, Artist: current.Track.Artist, Album: current.Track.Release.Title}
	if art := current.Track.Release.Artwork; art != nil && p.manager.artwork != nil {
		mime, bytes, err := p.manager.artwork(p.ctx, art.ResourceID, false)
		if err == nil && (mime == "image/jpeg" || mime == "image/png") {
			meta.ArtworkType, meta.Artwork = mime, bytes
		}
	}
	if err := p.sender.SetMetadata(p.ctx, meta); err != nil {
		return err
	}
	p.metadataEntry = current.EntryID
	return nil
}
