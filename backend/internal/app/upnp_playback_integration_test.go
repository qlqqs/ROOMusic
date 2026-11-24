package app

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestPostgreSQLUPnPPlayback(t *testing.T) {
	for _, mode := range []string{"normal", "zero"} {
		t.Run(mode, func(t *testing.T) {
			r := newUPnPTestRenderer(t, mode)
			f := newUPnPFixture(t, true)
			f.enable(t)
			f.add(t, r)
			member := f.createUser(t, "user")
			d := f.device(t, &member, r)
			tracks := f.tracks(t, 18, 18, 18)
			for i, track := range tracks[1:] {
				mustExec(t, f.integrationTestApplication,
					"UPDATE tracks SET medium_id=(SELECT medium_id FROM tracks WHERE id=$1),position=$2 WHERE id=$3",
					tracks[0], i+2, track)
			}
			f.command(t, &member, d, map[string]any{"command": "set_settings", "settings": map[string]any{
				"sample_rate": map[string]any{"strategy": "custom", "custom_rate": 48000, "filter": "linear_precise"},
			}})
			f.play(t, &member, d, tracks)
			f.wait(t, 12*time.Second, "真实音频与进度", func() bool {
				s := r.State()
				z := f.zone(t, &member, d)
				return s.SampleRate == 48000 && s.BytesRead > 0 && z.Status == "playing" && z.Position.Seconds >= 2
			})
			initial := r.State()
			if initial.Volume != 50 || !strings.Contains(initial.Metadata, "&#") || !strings.Contains(initial.URI, "/upnp/streams/") ||
				!slices.Equal(r.transportActions(), []string{"SetAVTransportURI", "Play"}) {
				t.Fatalf("首流状态 = %+v，SOAP = %v", initial, r.transportActions())
			}
			if (initial.RelTime == "00:00:00") != (mode == "zero") {
				t.Fatalf("设备 RelTime = %s，模式 %s", initial.RelTime, mode)
			}

			f.command(t, &member, d, map[string]any{"command": "pause"})
			f.wait(t, 6*time.Second, "应用暂停设备", func() bool {
				return r.State().TransportState == "PAUSED_PLAYBACK" && f.zone(t, &member, d).Status == "paused"
			})
			paused := f.zone(t, &member, d).Position.Seconds
			time.Sleep(250 * time.Millisecond)
			if got := f.zone(t, &member, d).Position.Seconds; got != paused {
				t.Fatalf("暂停时播放头从 %f 移到了 %f", paused, got)
			}
			f.command(t, &member, d, map[string]any{"command": "play"})
			f.wait(t, 6*time.Second, "应用恢复播放", func() bool {
				z := f.zone(t, &member, d)
				return r.State().TransportState == "PLAYING" && z.Status == "playing" && z.Position.Seconds >= paused
			})
			if r.State().URI != initial.URI {
				t.Fatal("设备原生暂停后继续时 URI 改变")
			}

			f.command(t, &member, d, map[string]any{"command": "seek", "position_seconds": 8})
			f.wait(t, 8*time.Second, "跳转后推送新音频", func() bool {
				s := r.State()
				return s.URI != initial.URI && s.BytesRead > 0 && f.zone(t, &member, d).Position.Seconds >= 8
			})
			sought := r.State()
			f.wait(t, 8*time.Second, "超过曲目一半时写播放历史", func() bool {
				var count int
				err := f.database.QueryRow("SELECT count(*) FROM play_history WHERE user_id=$1 AND track_id=$2", member.id, tracks[0]).Scan(&count)
				if err != nil {
					t.Fatal(err)
				}
				return count == 1
			})
			f.wait(t, 16*time.Second, "连续流跨到下一曲目", func() bool {
				z := f.zone(t, &member, d)
				return z.Current != nil && z.Current.Track.TrackID == tracks[1]
			})
			if r.State().URI != sought.URI {
				t.Fatal("跨曲目时替换了连续流 URI")
			}

			r.remote(t, "pause")
			f.wait(t, 6*time.Second, "设备暂停反映到播放区", func() bool {
				z := f.zone(t, &member, d)
				return z.Intent == "pause" && z.Status == "paused" && z.Event != nil && z.Event.Source == "device" && z.Event.Kind == "pause"
			})
			r.remote(t, "play")
			f.wait(t, 8*time.Second, "设备恢复反映到播放区", func() bool {
				z := f.zone(t, &member, d)
				return z.Intent == "play" && z.Status == "playing" && z.Event != nil && z.Event.Source == "device" && z.Event.Kind == "play"
			})
			r.remote(t, "stop")
			f.wait(t, 6*time.Second, "设备提前停止反映到播放区", func() bool {
				z := f.zone(t, &member, d)
				return z.Intent == "pause" && z.Status == "paused" && z.Event != nil && z.Event.Source == "device" && z.Event.Kind == "pause"
			})
			f.command(t, &member, d, map[string]any{"command": "next"})
			f.wait(t, 8*time.Second, "下一首推送第三首的音频", func() bool {
				z := f.zone(t, &member, d)
				s := r.State()
				return z.Current != nil && z.Current.Track.TrackID == tracks[2] && z.Status == "playing" &&
					s.TransportState == "PLAYING" && s.URI != sought.URI && s.BytesRead > 0
			})
		})
	}
}

func TestPostgreSQLUPnPOutputOfflineAndEnded(t *testing.T) {
	for _, scenario := range []string{"offline", "ended"} {
		t.Run(scenario, func(t *testing.T) {
			r := newUPnPTestRenderer(t, "normal")
			f := newUPnPFixture(t, true)
			f.enable(t)
			f.add(t, r)
			member := f.createUser(t, "user")
			d := f.device(t, &member, r)
			seconds := 5
			if scenario == "offline" {
				seconds = 30
			}
			f.play(t, &member, d, f.tracks(t, seconds))
			f.wait(t, 8*time.Second, "设备开始播放", func() bool {
				return r.State().BytesRead > 0 && f.zone(t, &member, d).Status == "playing"
			})
			if scenario == "offline" {
				r.pollFails.Store(true)
				f.wait(t, 8*time.Second, "三次轮询失败后下线", func() bool {
					device, ok := deviceFor(f.snapshot(t, &member), r.udn)
					return !ok || !device.Online
				})
				f.wait(t, 5*time.Second, "设备停止并释放输出", func() bool { return r.State().TransportState == "STOPPED" })
				if got := r.pollsFailed.Load(); got != 3 {
					t.Fatalf("设备离线前失败了 %d 次", got)
				}
				r.pollFails.Store(false)
				f.add(t, r)
				f.wait(t, 5*time.Second, "恢复可见后原播放区已暂停", func() bool {
					z := f.zone(t, &member, d)
					return z.Intent == "pause" && z.Status == "paused" && z.Stream == nil &&
						f.device(t, &member, r).OutputSessionID == nil
				})
			} else {
				f.wait(t, 12*time.Second, "队列结束并退出播放", func() bool {
					z := f.zone(t, &member, d)
					return r.State().TransportState == "STOPPED" && z.Intent == "pause" && z.Status == "paused" && z.Stream == nil && z.Position.Seconds == 0 && !z.HasNext
				})
			}
		})
	}
}

func TestPostgreSQLUPnPUnreadableVolumeDoesNotMuteDevice(t *testing.T) {
	r := newUPnPTestRenderer(t, "normal")
	r.getVolumeFails.Store(true)
	f := newUPnPFixture(t, true)
	f.enable(t)
	f.add(t, r)
	member := f.createUser(t, "user")
	d := f.device(t, &member, r)
	if r.State().Volume != 50 {
		t.Fatalf("设备初始音量 = %d，期望 50", r.State().Volume)
	}
	f.play(t, &member, d, f.tracks(t, 8))
	f.wait(t, 8*time.Second, "首次启动写入音频", func() bool { return r.State().BytesRead > 0 })
	if got := r.State().Volume; got != 50 {
		t.Fatalf("GetVolume 失败后登记音量为 %.2f，首次播放把设备实际音量从 50 改成了 %d", d.Volume, got)
	}
}
