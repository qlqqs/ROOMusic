package slimproto

import (
	"context"
	"encoding/binary"
	"io"
	"log/slog"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/qlqq/roomusic/backend/internal/zones"
)

type groupTestZones struct {
	mu      sync.Mutex
	zone    zones.Zone
	roster  []zones.GroupMember
	streams map[int64]zones.GroupStreamInfo
	reports []zones.ReportOutputInput
}

func (*groupTestZones) ManagedOutputVolume(string, string) (float64, bool) { return 1, true }

func (*groupTestZones) ManagedStream(string, string, int64) (zones.UPnPStreamInfo, error) {
	return zones.UPnPStreamInfo{}, zones.ErrStreamGone
}

func (z *groupTestZones) ManagedGroupStream(_, _ string, seq int64) (zones.GroupStreamInfo, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	info, ok := z.streams[seq]
	if !ok {
		return zones.GroupStreamInfo{}, zones.ErrStreamGone
	}
	info.Members = slices.Clone(info.Members)
	return info, nil
}

func (z *groupTestZones) GroupMembers(string, string) []zones.GroupMember {
	z.mu.Lock()
	defer z.mu.Unlock()
	return slices.Clone(z.roster)
}

func (z *groupTestZones) ReportGroupMember(_ context.Context, _, _, id, state string) error {
	z.mu.Lock()
	defer z.mu.Unlock()
	for i := range z.roster {
		if z.roster[i].DeviceID == id {
			z.roster[i].State = state
			return nil
		}
	}
	return zones.ErrNotFound
}

func (z *groupTestZones) ReportOutput(_ context.Context, _, _ string, in zones.ReportOutputInput) (zones.OutputResult, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	z.reports = append(z.reports, in)
	return zones.OutputResult{Zone: z.zone}, nil
}

func (*groupTestZones) SubscribeOutput(context.Context, string, string) (*zones.Subscription, error) {
	return nil, zones.ErrOutputUnknown
}

type groupTestPeer struct {
	player   *Player
	jiffies  uint32
	released int
	rejoins  int
	busy     bool
}

type groupTestRun struct {
	t       *testing.T
	zones   *groupTestZones
	hub     *zones.Hub
	peers   map[string]*groupTestPeer
	epoch   time.Time
	exit    chan zones.ReleaseOutputInput
	offline bool
}

func newGroupTestRun(t *testing.T, members ...zones.GroupMember) *groupTestRun {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	f := &groupTestRun{
		t: t, hub: zones.NewHub(), epoch: time.Now(),
		zones: &groupTestZones{roster: members, streams: make(map[int64]zones.GroupStreamInfo)},
		peers: make(map[string]*groupTestPeer), exit: make(chan zones.ReleaseOutputInput, 1),
	}
	for i, member := range members {
		playerCtx, stop := context.WithCancel(ctx)
		f.peers[member.DeviceID] = &groupTestPeer{
			jiffies: uint32(1000 + 5000*i),
			player: &Player{
				local: netip.MustParseAddr("192.0.2.1"), volume: 1,
				ctx: playerCtx, cancel: stop, queue: make(chan writeRequest, 64),
			},
		}
	}
	output := Output{
		Zones: f.zones, UserID: "listener", DeviceID: "leader", ZoneID: "zone", SessionID: "session",
		BaseURL: "http://192.0.2.1:8080", Player: f.peers["leader"].player,
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Offline: func() { f.offline = true },
		Member: func(_ context.Context, member zones.GroupMember, rejoin bool) (MemberLease, error) {
			peer := f.peers[member.DeviceID]
			if rejoin {
				peer.rejoins++
				if peer.busy {
					return MemberLease{}, &zones.DeviceBusyError{Occupant: zones.DeviceOccupant{UserID: "other-listener"}}
				}
			}
			return MemberLease{Player: peer.player, Done: peer.player.Done(), Release: func() { peer.released++ }}, nil
		},
	}
	sub := f.hub.Subscribe(output.UserID)
	go func() { f.exit <- output.Run(ctx, sub) }()
	t.Cleanup(func() {
		cancel()
		synctest.Wait()
	})
	return f
}

func newTwoPlayerGroup(t *testing.T) *groupTestRun {
	t.Helper()
	return newGroupTestRun(t,
		zones.GroupMember{DeviceID: "leader", State: "active", Volume: 1},
		zones.GroupMember{DeviceID: "member", State: "waiting", Volume: 1},
	)
}

func (f *groupTestRun) stream(seq int64, members ...string) {
	f.t.Helper()
	stream := zones.GroupStreamInfo{DurationSeconds: 30}
	for _, id := range members {
		path := "/upnp/streams/" + id
		if seq == 2 {
			path += "-next"
		}
		stream.Members = append(stream.Members, zones.GroupMemberStream{DeviceID: id, Path: path})
	}
	f.zones.mu.Lock()
	f.zones.streams[seq] = stream
	f.zones.mu.Unlock()
}

func (f *groupTestRun) markDelivered(seq int64, id string) {
	f.t.Helper()
	f.zones.mu.Lock()
	defer f.zones.mu.Unlock()
	for i, reader := range f.zones.streams[seq].Members {
		if reader.DeviceID == id {
			f.zones.streams[seq].Members[i].Delivered = true
			return
		}
	}
	f.t.Fatalf("流 %d 缺少成员 %s", seq, id)
}

func (f *groupTestRun) setZone(intent string, seq, next int64) {
	f.t.Helper()
	f.zones.mu.Lock()
	z := zones.Zone{ZoneID: "zone", Revision: f.zones.zone.Revision + 1, Intent: intent,
		Group: &zones.ZoneGroup{}, Stream: &zones.Stream{Seq: seq},
		Settings: zones.ZoneSettings{
			SampleRate: zones.DefaultSampleRateDocument(), Downmix: zones.DefaultDownmixDocument(),
			PEQ: zones.DefaultPEQDocument(), Convolution: zones.DefaultConvolutionDocument(),
		},
	}
	if next != 0 {
		z.NextStream = &zones.Stream{Seq: next}
	}
	f.zones.zone = z
	f.zones.mu.Unlock()
	if err := f.hub.Publish("listener", "zone", struct{ Zone zones.Zone }{z}); err != nil {
		f.t.Fatal(err)
	}
	synctest.Wait()
}

func (f *groupTestRun) stat(id, event string, elapsedMS uint32) {
	f.t.Helper()
	p := f.peers[id]
	now := time.Now()
	p.player.stat(Status{
		Event: event, Jiffies: p.jiffies + uint32(now.Sub(f.epoch).Milliseconds()),
		ElapsedMS: elapsedMS, ElapsedSeconds: elapsedMS / 1000,
	}, now)
	synctest.Wait()
}

type groupTestCommand struct {
	cmd  byte
	auto byte
	ms   uint32
	path string
}

func (f *groupTestRun) expect(id string, want ...groupTestCommand) {
	f.t.Helper()
	var got []groupTestCommand
	queue := f.peers[id].player.queue
	for len(queue) != 0 {
		request := <-queue
		if request.written != nil {
			close(request.written)
		}
		frame := request.frame
		if string(frame[2:6]) != "strm" || frame[6] == 't' {
			continue
		}
		cmd := groupTestCommand{cmd: frame[6], ms: binary.BigEndian.Uint32(frame[20:24])}
		if cmd.cmd == 's' {
			cmd.auto = frame[7]
			fields := strings.Fields(string(frame[30:]))
			if len(fields) < 3 || fields[0] != "GET" {
				f.t.Fatalf("%s 的 strm s 缺少取流请求：%q", id, frame[30:])
			}
			cmd.path = fields[1]
		}
		got = append(got, cmd)
	}
	synctest.Wait()
	if !reflect.DeepEqual(got, want) {
		f.t.Fatalf("%s 的控制帧 = %+v，期望 %+v", id, got, want)
	}
}

func (f *groupTestRun) expectState(id, want string) {
	f.t.Helper()
	roster := f.zones.GroupMembers("", "")
	for _, member := range roster {
		if member.DeviceID == id && member.State == want {
			return
		}
	}
	f.t.Fatalf("设备 %s 的状态期望为 %s，组成员 = %+v", id, want, roster)
}

func (f *groupTestRun) open(members ...string) {
	f.t.Helper()
	f.stream(1, members...)
	f.setZone("play", 1, 0)
	for _, id := range members {
		f.expect(id, groupTestCommand{cmd: 'q'}, groupTestCommand{cmd: 's', auto: '0', path: "/upnp/streams/" + id})
		f.stat(id, "STMc", 0)
	}
}

func (f *groupTestRun) playing(members ...string) {
	f.t.Helper()
	f.open(members...)
	for _, id := range members {
		f.stat(id, "STMl", 0)
	}
	time.Sleep(250 * time.Millisecond)
	synctest.Wait()
	for _, id := range members {
		f.expect(id, groupTestCommand{cmd: 'u', ms: f.peers[id].jiffies + 550})
	}
	time.Sleep(350 * time.Millisecond)
	for _, id := range members {
		f.stat(id, "STMs", 0)
	}
}

func (f *groupTestRun) assertNoReport(kind string) {
	f.t.Helper()
	f.zones.mu.Lock()
	defer f.zones.mu.Unlock()
	for _, report := range f.zones.reports {
		if report.Kind == kind {
			f.t.Fatalf("收到意外的 %s 回报：%+v", kind, report)
		}
	}
}

func (f *groupTestRun) lastReport() zones.ReportOutputInput {
	f.t.Helper()
	f.zones.mu.Lock()
	defer f.zones.mu.Unlock()
	if len(f.zones.reports) == 0 {
		f.t.Fatal("未收到播放区回报")
	}
	return f.zones.reports[len(f.zones.reports)-1]
}

func TestSlimProtoGroupWaitsForAllBufferReady(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newGroupTestRun(t,
			zones.GroupMember{DeviceID: "leader", State: "active", Volume: 1, SyncDelayMS: -20},
			zones.GroupMember{DeviceID: "member", State: "waiting", Volume: 1, SyncDelayMS: 35},
		)
		f.open("leader", "member")
		f.stat("leader", "STMl", 0)
		time.Sleep(500 * time.Millisecond)
		synctest.Wait()
		f.expect("leader")
		f.expect("member")
		f.stat("member", "STMl", 0)
		// 统一出声时刻为 835 ms，成员分别减去自己的固定延迟再换算到设备时钟。
		f.expect("leader", groupTestCommand{cmd: 'u', ms: 1855})
		f.expect("member", groupTestCommand{cmd: 'u', ms: 6800})
		f.expectState("member", "active")
	})
}

func TestSlimProtoGroupDropsBufferTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newTwoPlayerGroup(t)
		f.open("leader", "member")
		f.stat("leader", "STMl", 0)
		time.Sleep(4 * time.Second)
		f.stat("leader", "STMt", 0)
		f.stat("member", "STMt", 0)
		time.Sleep(3750 * time.Millisecond)
		synctest.Wait()
		f.expect("leader")
		f.expect("member")
		f.expectState("member", "joining")
		time.Sleep(250 * time.Millisecond)
		synctest.Wait()
		f.expect("leader", groupTestCommand{cmd: 'u', ms: 9300})
		f.expect("member", groupTestCommand{cmd: 'q'})
		f.expectState("member", "dropped")
		if f.peers["member"].released != 1 {
			t.Fatal("缓冲超时的成员租约尚未归还")
		}
		f.assertNoReport("fault")
	})
}

func TestSlimProtoGroupDefersStartUntilJiffiesWrap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newTwoPlayerGroup(t)
		f.peers["member"].jiffies = 0xffffff00
		f.open("leader", "member")
		f.stat("leader", "STMl", 0)
		f.stat("member", "STMl", 0)
		time.Sleep(250 * time.Millisecond)
		synctest.Wait()
		f.expect("leader")
		f.expect("member")
		f.stat("member", "STMt", 0)
		time.Sleep(250 * time.Millisecond)
		f.stat("member", "STMt", 0)
		f.expect("leader", groupTestCommand{cmd: 'u', ms: 1800})
		f.expect("member", groupTestCommand{cmd: 'u', ms: 544})
	})
}

func TestSlimProtoGroupCorrectsLagAtMostOncePerSecond(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newTwoPlayerGroup(t)
		f.playing("leader", "member")
		f.stat("leader", "STMt", 50)
		f.stat("member", "STMt", 41)
		time.Sleep(650 * time.Millisecond)
		synctest.Wait()
		f.expect("member")
		f.stat("leader", "STMt", 700)
		f.stat("member", "STMt", 690)
		time.Sleep(time.Second)
		synctest.Wait()
		f.expect("member", groupTestCommand{cmd: 'a', ms: 10})
		f.expect("leader")

		time.Sleep(250 * time.Millisecond)
		f.stat("leader", "STMt", 1950)
		f.stat("member", "STMt", 1925)
		time.Sleep(500 * time.Millisecond)
		synctest.Wait()
		f.expect("member")
		time.Sleep(250 * time.Millisecond)
		synctest.Wait()
		f.expect("member", groupTestCommand{cmd: 'a', ms: 25})
		time.Sleep(time.Second)
		synctest.Wait()
		f.expect("member")
	})
}

func TestSlimProtoGroupSkipsStalePlayPoint(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newTwoPlayerGroup(t)
		f.playing("leader", "member")
		f.stat("leader", "STMt", 50)
		f.stat("member", "STMt", 50)
		time.Sleep(3200 * time.Millisecond)
		// 若沿用陈旧的主设备播放点，成员会被判为落后 20 ms。
		f.stat("member", "STMt", 3230)
		time.Sleep(450 * time.Millisecond)
		synctest.Wait()
		f.expect("leader")
		f.expect("member")
		f.stat("leader", "STMt", 3700)
		time.Sleep(time.Second)
		synctest.Wait()
		f.expect("member", groupTestCommand{cmd: 'a', ms: 20})
	})
}

func TestSlimProtoGroupPauseAndResume(t *testing.T) {
	for _, tc := range []struct {
		name          string
		memberPaused  bool
		wait          time.Duration
		memberDelayMS int
		wantRestart   bool
		wantLeaderMS  uint32
		wantMemberMS  uint32
	}{
		{
			name: "统一时刻", memberPaused: true, wait: 500 * time.Millisecond,
			wantLeaderMS: 2400, wantMemberMS: 7400,
		},
		{name: "成员未确认暂停则重新开流", wantRestart: true},
		{
			name: "长时间暂停后继续仍应用同步微调", memberPaused: true, wait: 3200 * time.Millisecond,
			memberDelayMS: 35, wantLeaderMS: 5135, wantMemberMS: 10100,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newTwoPlayerGroup(t)
				f.playing("leader", "member")
				f.zones.mu.Lock()
				f.zones.roster[1].SyncDelayMS = tc.memberDelayMS
				f.zones.mu.Unlock()
				f.setZone("pause", 1, 0)
				f.expect("leader", groupTestCommand{cmd: 'p'})
				f.expect("member", groupTestCommand{cmd: 'p'})
				f.stat("leader", "STMp", 50)
				if tc.memberPaused {
					f.stat("member", "STMp", 80)
				}
				time.Sleep(tc.wait)
				f.setZone("play", 1, 0)
				if tc.wantRestart {
					f.expect("leader")
					f.expect("member")
					report := f.lastReport()
					if report.Kind != "fault" || report.Status != "paused" ||
						report.Fault == nil || *report.Fault != "stream_lost" ||
						report.StreamSeq == nil || *report.StreamSeq != 1 || report.StreamSeconds != .05 {
						t.Fatalf("重开流回报 = %+v，期望从主设备的暂停位置重开", report)
					}
					return
				}
				f.expect("leader", groupTestCommand{cmd: 'u', ms: tc.wantLeaderMS})
				f.expect("member", groupTestCommand{cmd: 'u', ms: tc.wantMemberMS})
				f.assertNoReport("fault")
			})
		})
	}
}

func TestSlimProtoGroupNextStreamWaitsForDeliveryAndDecode(t *testing.T) {
	for _, last := range []string{"Delivered", "STMd"} {
		t.Run(last, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newTwoPlayerGroup(t)
				f.playing("leader", "member")
				f.stream(2, "leader", "member")
				f.setZone("play", 1, 2)
				f.markDelivered(1, "leader")
				f.stat("leader", "STMd", 50)
				if last == "Delivered" {
					f.stat("member", "STMd", 50)
				} else {
					f.markDelivered(1, "member")
				}
				time.Sleep(250 * time.Millisecond)
				synctest.Wait()
				f.expect("leader")
				f.expect("member")
				if last == "Delivered" {
					f.markDelivered(1, "member")
				} else {
					f.stat("member", "STMd", 50)
				}
				time.Sleep(250 * time.Millisecond)
				synctest.Wait()
				f.expect("leader", groupTestCommand{cmd: 's', auto: '1', path: "/upnp/streams/leader-next"})
				f.expect("member", groupTestCommand{cmd: 's', auto: '1', path: "/upnp/streams/member-next"})
			})
		})
	}
}

func TestSlimProtoGroupIsolatesMemberFailure(t *testing.T) {
	for _, ev := range []string{"STMn", "DSCO", "STMo", "断开连接"} {
		t.Run(ev, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newTwoPlayerGroup(t)
				f.playing("leader", "member")
				want := "dropped"
				switch ev {
				case "断开连接":
					f.peers["member"].player.cancel()
					want = "offline"
				case "DSCO":
					f.peers["member"].player.notify(playerEvent{Status: Status{Event: ev, ErrorCode: 1}, at: time.Now()})
				default:
					f.stat("member", ev, 50)
				}
				synctest.Wait()
				if want == "dropped" {
					f.expect("member", groupTestCommand{cmd: 'q'})
				}
				f.expectState("member", want)
				f.expect("leader")
				f.stat("leader", "STMt", 50)
				time.Sleep(time.Second)
				synctest.Wait()
				f.expect("leader")
				f.assertNoReport("fault")
				report := f.lastReport()
				if report.Status != "playing" || report.StreamSeconds <= .05 {
					t.Fatalf("成员故障后的主设备回报 = %+v", report)
				}
			})
		})
	}
}

func TestSlimProtoGroupEndsAfterAllUnderrun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newTwoPlayerGroup(t)
		f.playing("leader", "member")
		f.stat("leader", "STMu", 75)
		f.assertNoReport("ended")
		f.stat("member", "STMu", 84)
		report := f.lastReport()
		if report.Kind != "ended" || report.Status != "paused" ||
			report.StreamSeq == nil || *report.StreamSeq != 1 || report.StreamSeconds != .075 {
			t.Fatalf("全组播放结束回报 = %+v", report)
		}
	})
}

func TestSlimProtoGroupStopsWhenLeaderDisconnects(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newTwoPlayerGroup(t)
		f.playing("leader", "member")
		f.stat("leader", "STMt", 125)
		f.peers["leader"].player.cancel()
		synctest.Wait()
		f.expect("member", groupTestCommand{cmd: 'q'})
		select {
		case release := <-f.exit:
			if !f.offline || release.StreamSeq == nil || *release.StreamSeq != 1 || release.StreamSeconds != .125 {
				t.Fatalf("主设备掉线后释放位置 = %+v，离线回调 = %t", release, f.offline)
			}
		default:
			t.Fatal("主设备断开后编组输出仍在运行")
		}
		if f.peers["member"].released != 1 {
			t.Fatal("主设备断开后成员租约尚未归还")
		}
	})
}

func TestSlimProtoGroupRejoinsOnlyAvailableMembers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newGroupTestRun(t,
			zones.GroupMember{DeviceID: "leader", State: "active", Volume: 1},
			zones.GroupMember{DeviceID: "available", State: "waiting", Volume: 1},
			zones.GroupMember{DeviceID: "occupied", State: "waiting", Volume: 1},
		)
		f.playing("leader", "available", "occupied")
		for _, id := range []string{"available", "occupied"} {
			f.stat(id, "STMo", 50)
			f.expect(id, groupTestCommand{cmd: 'q'})
			f.expectState(id, "dropped")
		}
		f.peers["occupied"].busy = true
		f.stream(2, "leader", "available", "occupied")
		f.setZone("play", 2, 0)
		for _, id := range []string{"leader", "available"} {
			f.expect(id, groupTestCommand{cmd: 'q'}, groupTestCommand{cmd: 's', auto: '0', path: "/upnp/streams/" + id + "-next"})
			f.stat(id, "STMc", 0)
			f.stat(id, "STMl", 0)
		}
		time.Sleep(400 * time.Millisecond)
		synctest.Wait()
		f.expect("leader", groupTestCommand{cmd: 'u', ms: 2300})
		f.expect("available", groupTestCommand{cmd: 'u', ms: 7300})
		f.expect("occupied")
		f.expectState("available", "active")
		f.expectState("occupied", "busy")
		for _, id := range []string{"available", "occupied"} {
			if got := f.peers[id].rejoins; got != 1 {
				t.Errorf("设备 %s 重新认领 %d 次，期望 1 次", id, got)
			}
		}
	})
}
