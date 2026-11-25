package slimproto

import (
	"context"
	"math"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// 用已播帧数和已请求帧数分别模拟补偿器的两个时间域。
type groupTestDrift struct {
	requested, played         int64
	sourcePoint, pendingPoint int64
	adjustments               []int64
}

func (d *groupTestDrift) Adjust(frames int64) {
	d.requested += frames
	d.adjustments = append(d.adjustments, frames)
}

func (d *groupTestDrift) Source(output int64) int64 {
	d.sourcePoint = output
	return output + d.played
}

func (d *groupTestDrift) Pending(output int64) int64 {
	d.pendingPoint = output
	return d.requested - d.played
}

func (f *groupTestRun) comparePlayPoints(leaderMS, memberMS uint32) {
	f.t.Helper()
	f.stat("leader", "STMt", leaderMS)
	f.stat("member", "STMt", memberMS)
	time.Sleep(time.Second)
	synctest.Wait()
	f.expect("leader")
}

func TestSlimProtoGroupCompensatesConstantDrift(t *testing.T) {
	for _, tc := range []struct {
		name string
		sign int
	}{{"慢钟", 1}, {"快钟", -1}} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newTwoPlayerGroup(t)
				d := &groupTestDrift{}
				f.peers["member"].drift = d
				f.playing("leader", "member")

				// 500 ppm 每两秒累积 1 ms；每三秒让上次请求的帧经过播放头。
				for second := 1; second <= 12; second++ {
					if second%3 == 0 {
						d.played = d.requested
					}
					leaderMS := second * 1000
					memberMS := leaderMS - tc.sign*(second/2)
					f.comparePlayPoints(uint32(leaderMS), uint32(memberMS))
					f.expect("member")
					wantPoint := int64(memberMS) * 48
					if d.sourcePoint != wantPoint || d.pendingPoint != wantPoint {
						t.Fatalf("第 %d 秒 Source/Pending 查询帧 = %d/%d，期望 %d", second, d.sourcePoint, d.pendingPoint, wantPoint)
					}
					var want []int64
					if second%2 == 0 {
						want = []int64{int64(tc.sign) * 48}
					}
					if !slices.Equal(d.adjustments, want) {
						t.Fatalf("第 %d 秒 Adjust = %v，期望 %v", second, d.adjustments, want)
					}
					d.adjustments = nil
				}
			})
		})
	}
}

func TestSlimProtoGroupDriftFallback(t *testing.T) {
	for _, tc := range []struct {
		name       string
		lagMS      int
		requested  int64
		played     int64
		wantAdjust int64
		command    []groupTestCommand
	}{
		// 已播的 2 ms 先换回源时间轴。尚待生效的 8 ms 只影响帧请求，不改变协议兜底的门槛。
		{"落后正好20ms", 22, 480, 96, 576, nil},
		{"超前正好20ms", -22, -480, -96, -576, nil},
		{"落后21ms", 23, 480, 96, -384, []groupTestCommand{{cmd: 'a', ms: 21}}},
		{"超前21ms", -23, -480, -96, 384, []groupTestCommand{{cmd: 'p', ms: 21}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newTwoPlayerGroup(t)
				d := &groupTestDrift{}
				f.peers["member"].drift = d
				f.playing("leader", "member")
				d.requested, d.played = tc.requested, tc.played
				f.comparePlayPoints(100, uint32(100-tc.lagMS))
				f.expect("member", tc.command...)
				if !slices.Equal(d.adjustments, []int64{tc.wantAdjust}) {
					t.Fatalf("Adjust = %v，期望 [%d]", d.adjustments, tc.wantAdjust)
				}
			})
		})
	}
}

func TestSlimProtoGroupDriftDeadband(t *testing.T) {
	for _, tc := range []struct {
		name     string
		residual int64
		want     []int64
	}{
		{"落后不到半毫秒", 23, nil},
		{"落后半毫秒", 24, nil},
		{"落后超过半毫秒", 25, []int64{25}},
		{"超前不到半毫秒", -23, nil},
		{"超前半毫秒", -24, nil},
		{"超前超过半毫秒", -25, []int64{-25}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newTwoPlayerGroup(t)
				d := &groupTestDrift{}
				f.peers["member"].drift = d
				f.playing("leader", "member")
				// 原始落后 1 ms（48 帧），减去在途调整才得到实际欠量。
				d.requested = 48 - tc.residual
				f.comparePlayPoints(100, 99)
				f.expect("member")
				if !slices.Equal(d.adjustments, tc.want) {
					t.Fatalf("Adjust = %v，期望 %v", d.adjustments, tc.want)
				}
			})
		})
	}
}

func TestSlimProtoGroupFallbackWithoutDrift(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newTwoPlayerGroup(t)
		f.playing("leader", "member")
		f.comparePlayPoints(100, 109)
		f.expect("member")
		f.comparePlayPoints(1100, 1110)
		f.expect("member", groupTestCommand{cmd: 'p', ms: 10})
		f.comparePlayPoints(2100, 2091)
		f.expect("member")
		f.comparePlayPoints(3100, 3090)
		f.expect("member", groupTestCommand{cmd: 'a', ms: 10})
	})
}

type groupTestRate struct {
	groupTestDrift
	rates       []float64
	ahead       int64
	waitForRate <-chan struct{}
}

func (d *groupTestRate) SetRate(ppm float64) {
	d.rates = append(d.rates, ppm)
	if d.waitForRate != nil {
		<-d.waitForRate
	}
}

func (d *groupTestRate) RenderState(output int64) (int64, int64) {
	return output + d.ahead, d.Pending(output)
}

func (f *groupTestRun) learnRate(ppm, spacing int) (*groupTestRate, float64) {
	f.t.Helper()
	d := &groupTestRate{}
	f.peers["member"].drift = d
	f.playing("leader", "member")
	if !slices.Equal(d.rates, []float64{0}) {
		f.t.Fatalf("首次开流的 SetRate = %v，期望 [0]", d.rates)
	}
	// 从同一播放点重复轮询不能增加样本数。一秒采样先满足 15 点，
	// 两秒采样先满足 20 秒；两种情况下均应等到两个条件都满足。
	last := max(20, (15-1)*spacing)
	for second := 0; second <= last; second++ {
		if second%spacing == 0 {
			ms := 1000 + second*1000
			lagMS := 3 + int(math.Round(float64(second*ppm)/1000))
			// 模拟先前的补偿已播出。恒定的 3 ms 起播偏移只应影响截距。
			d.played = d.requested
			f.comparePlayPoints(uint32(ms), uint32(ms-lagMS))
		} else {
			time.Sleep(time.Second)
			synctest.Wait()
			f.expect("leader")
		}
		f.expect("member")
		if second < last && len(d.rates) != 1 {
			f.t.Fatalf("第 %d 秒过早设置速率 %v", second, d.rates)
		}
	}
	// STMt 只提供整数毫秒，20 秒窗口的拟合允许 15 ppm 量化误差。
	if len(d.rates) != 2 || math.Abs(d.rates[1]-float64(ppm)) > 15 {
		f.t.Fatalf("第 %d 秒的 SetRate = %v，期望 %d ±15 ppm", last, d.rates, ppm)
	}
	return d, d.rates[1]
}

func TestSlimProtoGroupDriftRateAcrossStreams(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newTwoPlayerGroup(t)
		d, ppm := f.learnRate(250, 1)

		// 将前馈目标设在 48 帧附近，渲染头取整的误差小于一帧。
		d.ahead = int64(math.Round(48e6 / ppm))
		d.played = 0
		for i, residual := range []int64{-25, -23, 23, 25} {
			d.adjustments = nil
			d.requested = 48 - residual
			ms := uint32(22+i) * 1000
			f.comparePlayPoints(ms, ms)
			f.expect("member")
			var want []int64
			if residual < -24 || residual > 24 {
				want = []int64{residual}
			}
			if !slices.Equal(d.adjustments, want) {
				t.Fatalf("前馈后残差 %d 帧，Adjust = %v，期望 %v", residual, d.adjustments, want)
			}
		}

		f.comparePlayPoints(26000, 25970)
		f.expect("member", groupTestCommand{cmd: 'a', ms: 30})
		for second := 27; second <= 46; second++ {
			ms := uint32(second * 1000)
			f.comparePlayPoints(ms, ms)
			f.expect("member")
		}
		if len(d.rates) != 2 {
			t.Fatalf("硬修正后积累的新样本跨度只有 19 秒，SetRate = %v", d.rates)
		}

		// 在新补偿器的 SetRate 中停住协调器，分别观察放行前后的控制队列。
		gate := make(chan struct{})
		release := sync.OnceFunc(func() { close(gate) })
		defer release()
		next := &groupTestRate{waitForRate: gate}
		f.peers["member"].drift = next
		f.stream(2, "leader", "member")
		f.setZone("play", 1, 2)
		for _, id := range []string{"leader", "member"} {
			f.markDelivered(1, id)
			f.stat(id, "STMd", 46000)
		}
		if !slices.Equal(next.rates, []float64{ppm}) {
			t.Fatalf("硬修正后接续开流 SetRate = %v，期望保留 %.3f ppm", next.rates, ppm)
		}
		f.expect("member")
		release()
		synctest.Wait()
		f.expect("member", groupTestCommand{cmd: 's', auto: '1', path: "/upnp/streams/member-next"})
	})
}

func TestSlimProtoGroupDriftRateClearedOnRejoin(t *testing.T) {
	for _, how := range []string{"断线重连", "退组再入组"} {
		t.Run(how, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newTwoPlayerGroup(t)
				f.learnRate(-250, 2)
				peer := f.peers["member"]
				if how == "断线重连" {
					peer.player.cancel()
					synctest.Wait()
					f.expectState("member", "offline")
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					peer.player = &Player{local: peer.player.local, volume: 1, ctx: ctx, cancel: cancel, queue: make(chan writeRequest, 64)}
				} else {
					f.zones.mu.Lock()
					member := f.zones.roster[1]
					f.zones.roster = f.zones.roster[:1]
					f.zones.mu.Unlock()
					f.setZone("play", 1, 0)
					f.expect("member", groupTestCommand{cmd: 'q'})
					f.zones.mu.Lock()
					member.State = "waiting"
					f.zones.roster = append(f.zones.roster, member)
					f.zones.mu.Unlock()
				}
				next := &groupTestRate{}
				peer.drift = next
				f.stream(2, "leader", "member")
				f.setZone("play", 2, 0)
				f.expect("member", groupTestCommand{cmd: 'q'}, groupTestCommand{cmd: 's', auto: '0', path: "/upnp/streams/member-next"})
				if !slices.Equal(next.rates, []float64{0}) {
					t.Fatalf("重新入组后 SetRate = %v，期望 [0]", next.rates)
				}
			})
		})
	}
}
