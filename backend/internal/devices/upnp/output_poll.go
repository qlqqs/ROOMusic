package upnp

import "time"

func (o *deviceOutput) poll() error {
	state, err := o.device.GetTransportInfo(o.ctx)
	if err != nil {
		return err
	}
	if o.seq == nil {
		return nil
	}
	if state == "TRANSITIONING" {
		return nil
	}
	wasPlaying := o.state == "PLAYING"
	if state == "PLAYING" {
		rel, err := o.device.GetPositionInfo(o.ctx)
		if err != nil {
			return err
		}
		now := time.Now()
		if !wasPlaying {
			o.setState(state)
		}
		if wasPlaying && rel <= o.lastRel {
			o.useClock = true
		}
		if !o.useClock {
			o.seconds = rel.Seconds()
			o.started = now
		}
		o.lastRel = rel
		if !wasPlaying && o.zone.Intent == "pause" {
			if err := o.remote("play"); err != nil {
				return err
			}
		}
		return o.report("status", "playing", nil)
	}
	if state != "PAUSED_PLAYBACK" && state != "STOPPED" && state != "NO_MEDIA_PRESENT" {
		return nil
	}
	if wasPlaying {
		o.setState(state)
		if state != "PAUSED_PLAYBACK" {
			ended, err := o.atEnd()
			if err != nil {
				return err
			}
			if ended {
				if o.zone.NextStream != nil {
					return o.push(o.zone.NextStream)
				}
				return o.report("ended", "paused", nil)
			}
			o.needsStream = true
		}
		if o.zone.Intent == "play" {
			if err := o.remote("pause"); err != nil {
				return err
			}
		}
		return o.report("status", "paused", nil)
	}
	return nil
}

// 设备可能已将整首短曲缓存起来；交付完毕时仍需核对实际播放头，区分遥控 Stop。
func (o *deviceOutput) atEnd() (bool, error) {
	info, err := o.manager.zones.UPnPStream(o.actor.ID, o.sessionID, *o.seq)
	if err != nil || !info.Delivered {
		return false, err
	}
	if info.DurationSeconds > 0 {
		return o.position()+1 >= info.DurationSeconds, nil
	}
	stream := o.zone.Stream
	if stream == nil || len(stream.Segments) == 0 {
		return true, nil
	}
	last := stream.Segments[len(stream.Segments)-1]
	if last.Track.DurationSeconds == nil {
		return true, nil
	}
	end := last.StreamStartSeconds + *last.Track.DurationSeconds - last.TrackStartSeconds
	return o.position()+1 >= end, nil
}

func (o *deviceOutput) remote(command string) error {
	result, err := o.manager.zones.CommandFromOutput(o.ctx, o.actor.ID, o.sessionID, command)
	if err == nil {
		o.zone = result.Zone
	}
	return err
}
