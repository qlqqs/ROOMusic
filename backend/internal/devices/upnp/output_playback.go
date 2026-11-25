package upnp

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/qlqq/roomusic/backend/internal/zones"
)

func (o *deviceOutput) run(sub *zones.Subscription) {
	defer func() { o.finish(sub) }()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	failures := 0
	for o.ctx.Err() == nil {
		before := o.state
		var err error
		var poll bool
		var subscriptionClosed bool
		select {
		case <-o.ctx.Done():
			return
		case <-sub.Done:
			subscriptionClosed = true
		case msg, ok := <-sub.Events:
			if !ok {
				subscriptionClosed = true
				break
			}
			if !o.receive(msg) {
				continue
			}
			err = o.apply()
		case <-timer.C:
			poll = true
			d, ok := o.registry.Lookup(o.device.UDN)
			if !ok || !d.Online {
				return
			}
			o.device = d.Device
			// 上一轮控制动作失败时先重试，避免把未执行的指令识别为遥控操作。
			err = o.apply()
			if err == nil {
				err = o.poll()
			}
			timer.Reset(o.interval())
		}
		if subscriptionClosed {
			next, err := o.resubscribe()
			if err != nil {
				return
			}
			sub = next
			continue
		}
		if errors.Is(err, zones.ErrOutputSuperseded) || errors.Is(err, zones.ErrOutputUnknown) {
			return
		}
		// 换流事件已经排队。
		if errors.Is(err, zones.ErrStreamGone) {
			continue
		}
		if err != nil {
			if o.ctx.Err() != nil {
				return
			}
			failures++
			o.manager.logger.Warn("渲染器控制失败", "event", "upnp.output.failed", "udn", o.device.UDN, "count", failures, "error", err)
			if failures >= 3 {
				o.registry.offline(o.device.UDN)
				return
			}
		} else if poll || before != o.state {
			failures = 0
			if !poll {
				timer.Reset(o.interval())
			}
		}
	}
}

func (o *deviceOutput) resubscribe() (*zones.Subscription, error) {
	if err := o.ctx.Err(); err != nil {
		return nil, err
	}
	kind := deviceKind(o.udn)
	o.manager.logger.Warn("托管输出订阅已关闭，正在重新订阅", "event", "devices.output.resubscribing", "module", kind, "zone_id", o.zoneID)
	return o.manager.zones.SubscribeOutput(o.ctx, o.actor.ID, o.sessionID)
}

func (o *deviceOutput) interval() time.Duration {
	if o.state == "PLAYING" {
		return time.Second
	}
	return 5 * time.Second
}

func (r *Registry) offline(udn string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rec := r.records[udn]; rec != nil {
		rec.advertisedUntil, rec.manualUntil = time.Time{}, time.Time{}
		rec.refreshed = time.Time{}
	}
}

func (o *deviceOutput) finish(sub *zones.Subscription) {
	sub.Close()
	o.cancel()
	if o.path != "" {
		// 关闭开关时先停止渲染器，再关闭发现网络；进程退出也要尽力停止。
		d := o.device
		d.discoveryContext = nil
		if err := d.Stop(context.Background()); err != nil {
			o.manager.logger.Warn("停止渲染器失败", "module", "upnp", "udn", d.UDN, "error", err)
		}
	}
	o.release(zones.ReleaseOutputInput{StreamSeq: o.seq, StreamSeconds: o.position()})
}

func (o *deviceOutput) release(position zones.ReleaseOutputInput) {
	o.releaseErr = o.manager.zones.ReleaseOutput(context.Background(), o.actor.ID, o.sessionID, position)
	if o.releaseErr != nil {
		o.manager.logger.Error("释放播放区输出失败", "module", deviceKind(o.udn), "zone_id", o.zoneID, "error", o.releaseErr)
	}
	o.manager.mu.Lock()
	delete(o.manager.owners, o.udn)
	close(o.done)
	o.manager.mu.Unlock()
}

func (o *deviceOutput) receive(msg zones.Message) bool {
	switch msg.Kind {
	case "snapshot":
		var snapshot zones.Snapshot
		if json.Unmarshal(msg.Data, &snapshot) != nil {
			return false
		}
		for _, z := range snapshot.Zones {
			if z.ZoneID == o.zoneID {
				o.zone = z
				return true
			}
		}
	case "zone":
		var update struct{ Zone zones.Zone }
		if json.Unmarshal(msg.Data, &update) == nil && update.Zone.ZoneID == o.zoneID && update.Zone.Revision >= o.zone.Revision {
			o.zone = update.Zone
			return true
		}
	case "device":
		var update struct{ Device zones.Device }
		if json.Unmarshal(msg.Data, &update) == nil && update.Device.DeviceID == o.deviceID {
			return true
		}
	}
	return false
}

func (o *deviceOutput) apply() error {
	if o.device.Capabilities.Volume == "number" {
		if volume, ok := o.manager.zones.ManagedOutputVolume(o.actor.ID, o.sessionID); ok {
			desired := int(math.Round(volume * 100))
			if desired != o.volume {
				if err := o.device.SetVolume(o.ctx, desired); err != nil {
					return err
				}
				o.volume = desired
			}
		}
	}
	z := o.zone
	if z.Stream == nil {
		if o.path != "" {
			if o.state != "STOPPED" {
				if err := o.device.Stop(o.ctx); err != nil {
					return err
				}
			}
			o.seq, o.path = nil, ""
			o.setState("STOPPED")
			o.needsStream = false
		}
		return nil
	}
	if z.Intent == "play" {
		if o.seq == nil || *o.seq != z.Stream.Seq {
			return o.push(z.Stream)
		}
		if o.needsStream {
			fault := "stream_lost"
			return o.report("fault", "paused", &fault)
		}
		if o.state != "PLAYING" {
			if err := o.device.Play(o.ctx); err != nil {
				return err
			}
			o.setState("PLAYING")
			return o.report("status", "playing", nil)
		}
	} else if o.state == "PLAYING" {
		if err := o.device.Pause(o.ctx); err != nil {
			return err
		}
		if o.device.Capabilities.Pause {
			o.setState("PAUSED_PLAYBACK")
		} else {
			o.setState("STOPPED")
			o.needsStream = true
		}
		return o.report("status", "paused", nil)
	}
	return nil
}

func (o *deviceOutput) push(stream *zones.Stream) error {
	info, err := o.manager.zones.UPnPStream(o.actor.ID, o.sessionID, stream.Seq)
	if err != nil {
		return err
	}
	if info.Path == o.path {
		// HTTP 重连已从当前播放头重新开流，设备的 RelTime 可能仍从旧 URI 起算。
		o.seq = &stream.Seq
		o.needsStream = false
		o.useClock = true
		o.seconds = 0
		if o.state == "PLAYING" {
			o.started = time.Now()
		}
		return nil
	}
	state, err := o.device.GetTransportInfo(o.ctx)
	if err != nil {
		return err
	}
	if state == "PLAYING" || state == "TRANSITIONING" {
		if err := o.device.Stop(o.ctx); err != nil {
			return err
		}
	}
	uri := o.base + info.Path
	meta := TrackMetadata{URI: uri, MIMEType: stream.MimeType}
	if len(stream.Segments) > 0 {
		t := stream.Segments[0].Track
		meta.Title, meta.Artist, meta.Album = t.Title, t.Artist, t.Release.Title
		if t.Release.Artwork != nil {
			meta.ArtURI = uri + "/artwork"
		}
	}
	if err := o.device.SetAVTransportURI(o.ctx, uri, BuildDIDL(meta)); err != nil {
		return err
	}
	o.path, o.seq = info.Path, &stream.Seq
	o.state = "STOPPED"
	o.seconds, o.lastRel = 0, 0
	o.started = time.Time{}
	o.useClock, o.needsStream = false, false
	if err := o.device.Play(o.ctx); err != nil {
		return err
	}
	o.setState("PLAYING")
	return o.report("status", "playing", nil)
}

func (o *deviceOutput) position() float64 {
	if !o.started.IsZero() {
		return o.seconds + time.Since(o.started).Seconds()
	}
	return o.seconds
}

func (o *deviceOutput) setState(state string) {
	o.seconds = o.position()
	o.started = time.Time{}
	if state == "PLAYING" {
		o.started = time.Now()
	}
	o.state = state
}

func (o *deviceOutput) report(kind, status string, fault *string) error {
	r, err := o.manager.zones.ReportOutput(o.ctx, o.actor.ID, o.sessionID, zones.ReportOutputInput{Kind: kind, Status: status, Fault: fault, StreamSeq: o.seq, StreamSeconds: o.position()})
	if err == nil {
		o.zone = r.Zone
	}
	return err
}
