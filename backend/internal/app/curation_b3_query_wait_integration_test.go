package app

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/playback/fingerprint"
)

func TestPostgreSQLB3FingerprintSourceWaitAndSwitch(t *testing.T) {
	tool := b3RealFingerprinter(t)
	for _, tc := range []struct {
		name, failing string
		disableBefore bool
		disableOnWait bool
	}{
		{name: "已关闭", disableBefore: true},
		{name: "等待期间关闭", failing: "acoustid", disableOnWait: true},
		{name: "AcoustID 恢复后再识别", failing: "acoustid"},
		{name: "候选详情归 MusicBrainz", failing: "musicbrainz"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			remote := b3NewRemoteRelease()
			var outage atomic.Bool
			outage.Store(tc.failing != "")
			var acoust, details, names atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fail := func(id string) bool {
					if tc.failing != id || !outage.Load() {
						return false
					}
					w.Header().Set("Retry-After", "245")
					w.WriteHeader(http.StatusServiceUnavailable)
					return true
				}
				switch r.URL.Path {
				case "/v2/lookup":
					acoust.Add(1)
					if fail("acoustid") {
						return
					}
					index := 0
					if b3LookupForm(t, r).Get("duration") == "16" {
						index = 1
					}
					fmt.Fprintf(w, `{"status":"ok","results":[{"id":"fixture","score":0.96,"recordings":[%s]}]}`, b3AcoustHit(remote, index))
				case "/mb/release/" + remote.ID:
					details.Add(1)
					if !fail("musicbrainz") {
						remote.writeMusicBrainz(w)
					}
				case "/mb/release":
					names.Add(1)
					io.WriteString(w, `{"count":0,"releases":[]}`)
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(server.Close)
			app, clock, admin := b3ProgramApp(t, tool, server)
			release, tracks := b3ReleaseTracks(t, app, b3Root(t, app), "合成正式专辑", "合成正式艺人", 2)
			for i, tone := range []string{b3LowTone, b3HighTone} {
				file := fmt.Sprintf("wait-%d.flac", i)
				b3GenerateAudio(t, filepath.Join(app.allowedRoot, file), tone)
				b3TrackSnapshot(t, app, tracks[i], file, []float64{12, 16}[i])
			}
			b3SetHTTPKey(t, app, &admin, "application", b3AppKey)
			b3SetHTTPKey(t, app, &admin, "user", b3UserKey)
			disable := func() {
				t.Helper()
				resp := app.request(t, http.MethodPut, curationBase+"/sources/acoustid", map[string]bool{"enabled": false}, &admin, nil)
				if resp.Code != http.StatusOK {
					t.Fatalf("禁用 AcoustID HTTP %d：%s", resp.Code, resp.Body)
				}
			}
			checkFallback := func(wantAcoust int32) {
				t.Helper()
				state := b3WorkState(t, app, "release", release)
				b3RequireCount(t, app, 0, `SELECT count(*) FROM curation_work_states WHERE subject='fingerprint' AND queue_release_id=$1 AND needs_work`, release)
				if state.Attempts != 1 || state.RetryCount != 0 || state.RetrySource != "" ||
					acoust.Load() != wantAcoust || names.Load() == 0 || details.Load() != 0 {
					t.Fatalf("禁用后应释放依赖并执行名字查询，状态 %s、失败计次 %d，请求 AcoustID=%d 名字=%d 详情=%d",
						state.Status, state.RetryCount, acoust.Load(), names.Load(), details.Load())
				}
				b3RequireReleaseResult(t, app, release, "unmatched", "search", "unavailable", "")
			}
			if tc.disableBefore {
				disable()
			}
			b3Enable(t, app, admin)
			clock.advance(t, time.Minute)
			if tc.disableBefore {
				checkFallback(0)
				return
			}
			wait := b3WorkState(t, app, "release", release)
			var p curation.Progress
			getJSON(t, app, &admin, curationBase+"/progress", &p)
			q := p.Queues[0]
			if wait.Status != "try_later" || wait.RetrySource != tc.failing || wait.NextAttemptAt == nil ||
				wait.RetryCount != 0 || wait.Attempts != 0 || q.Source == nil || *q.Source != tc.failing ||
				q.RetryAt == nil || !q.RetryAt.Equal(*wait.NextAttemptAt) {
				t.Fatalf("应等待真实故障源 %s，状态 %s、来源 %s、期限 %v、失败计次 %d，队列 %+v",
					tc.failing, wait.Status, wait.RetrySource, wait.NextAttemptAt, wait.RetryCount, q)
			}
			acoustBefore, detailsBefore, namesBefore := acoust.Load(), details.Load(), names.Load()
			if tc.failing == "acoustid" && namesBefore != 0 {
				t.Errorf("AcoustID 暂时不可用时开始前至进入等待阶段应无名字查询，实际 %d 次", namesBefore)
			}
			if tc.failing == "acoustid" && (acoustBefore != 1 || detailsBefore != 0) ||
				tc.failing == "musicbrainz" && (acoustBefore != 2 || detailsBefore != 1) {
				t.Fatalf("初次故障时请求 AcoustID=%d 详情=%d", acoustBefore, detailsBefore)
			}
			if tc.disableOnWait {
				disable()
				clock.advance(t, time.Second)
				checkFallback(acoustBefore)
				return
			}
			outage.Store(false)
			clock.advance(t, wait.NextAttemptAt.Sub(clock.options().Now())-time.Second)
			if acoust.Load() != acoustBefore || details.Load() != detailsBefore || names.Load() != namesBefore {
				t.Fatalf("到期前不应追加查询，AcoustID=%d 详情=%d 名字=%d", acoust.Load(), details.Load(), names.Load())
			}
			clock.advance(t, time.Second+time.Microsecond)
			b3RequireReleaseResult(t, app, release, "matched", "acoustid", "used", "")
			if names.Load() != namesBefore {
				t.Fatalf("成功识别期间名字查询应不增加，等待时 %d 次，结束时 %d 次", namesBefore, names.Load())
			}
		})
	}
}

func TestPostgreSQLB3LocalFingerprintRetriesRealAudio(t *testing.T) {
	tool := b3RealFingerprinter(t)
	remote := b3NewRemoteRelease()
	var acoust, firstTrackLookups atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/lookup":
			acoust.Add(1)
			var index int
			switch duration := b3LookupForm(t, r).Get("duration"); duration {
			case "12":
				firstTrackLookups.Add(1)
			case "16":
				index = 1
			default:
				t.Errorf("短轨不应对 AcoustID 出站，收到时长 %q", duration)
				http.NotFound(w, r)
				return
			}
			fmt.Fprintf(w, `{"status":"ok","results":[{"id":"fixture","score":0.96,"recordings":[%s]}]}`, b3AcoustHit(remote, index))
		case "/mb/release/" + remote.ID:
			remote.writeMusicBrainz(w)
		case "/mb/release":
			io.WriteString(w, `{"count":0,"releases":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	app, clock, admin := b3ProgramApp(t, tool, server)
	root := b3Root(t, app)
	release, tracks := b3ReleaseTracks(t, app, root, "合成正式专辑", "合成正式艺人", 2)
	_, shortTracks := b3ReleaseTracks(t, app, root, "合成短轨专辑", "合成短轨艺人", 2)
	for i, track := range shortTracks {
		file := fmt.Sprintf("short-%d.flac", i)
		b3GenerateAudio(t, filepath.Join(app.allowedRoot, file), "sine=frequency=263:sample_rate=11025:duration=9.75")
		b3TrackSnapshot(t, app, track, file, 9.75)
	}
	if err := os.WriteFile(filepath.Join(app.allowedRoot, "broken.flac"), []byte("invalid synthetic FLAC"), 0o600); err != nil {
		t.Fatal(err)
	}
	b3TrackSnapshot(t, app, tracks[0], "broken.flac", 12)
	b3GenerateAudio(t, filepath.Join(app.allowedRoot, "good.flac"), b3HighTone)
	b3TrackSnapshot(t, app, tracks[1], "good.flac", 16)
	// 模拟旧版刚写入、仍在旧 24 小时冷却期内的失败诊断。
	mustExec(t, app, `INSERT INTO track_acoustic_fingerprints
		(track_id,pipeline_version,content_fingerprint,source_size,source_modified_at,status,failure_category,attempts)
		SELECT id,$2,content_fingerprint,source_size,source_modified_at,'failed','decode_failed',1 FROM tracks WHERE id=$1`,
		tracks[0], fingerprint.Version)
	b3SetHTTPKey(t, app, &admin, "application", b3AppKey)
	b3SetHTTPKey(t, app, &admin, "user", b3UserKey)
	b3Enable(t, app, admin)

	var previous time.Time
	for i, advance := range []time.Duration{time.Minute, 5 * time.Second} {
		clock.advance(t, advance)
		state := b3WorkState(t, app, "release", release)
		if state.Status != "try_later" || state.RetrySource != "local_fingerprint" || state.RetryCount != i+1 ||
			state.Generation != 0 || state.Attempts != 0 || state.NextAttemptAt == nil ||
			!state.NextAttemptAt.Equal(clock.options().Now().Add(time.Duration(5<<i)*time.Second)) {
			t.Errorf("第 %d 轮真实失败应只计一次，状态 %+v", i+1, state)
		}
		var computed time.Time
		var attempts int
		err := app.database.QueryRow(`SELECT f.computed_at,f.attempts FROM track_acoustic_fingerprints f JOIN tracks t ON t.id=f.track_id
			WHERE t.id=$1 AND f.failure_category='decode_failed' AND
			(f.content_fingerprint,f.source_size,f.source_modified_at)=(t.content_fingerprint,t.source_size,t.source_modified_at)`, tracks[0]).Scan(&computed, &attempts)
		if err != nil || attempts != i+2 || !computed.After(previous) {
			t.Errorf("第 %d 轮同一文件快照应完成一次真实解码，累计 %d，计算时间 %s → %s，错误 %v",
				i+1, attempts, previous, computed, err)
		}
		previous = computed
		for _, track := range shortTracks {
			b3RequireCount(t, app, 1, `SELECT count(*) FROM track_acoustic_fingerprints f JOIN tracks t ON t.id=f.track_id
				WHERE t.id=$1 AND f.failure_category='too_short' AND f.attempts=1 AND
				(f.content_fingerprint,f.source_size,f.source_modified_at)=(t.content_fingerprint,t.source_size,t.source_modified_at)`, track)
		}
		if firstTrackLookups.Load() != 0 || acoust.Load() > 1 {
			t.Errorf("损坏首轨应无出站，正常样本至多一次。首轨 %d 次，全部 %d 次", firstTrackLookups.Load(), acoust.Load())
		}
	}
	b3GenerateAudio(t, filepath.Join(app.allowedRoot, "fixed.flac"), b3LowTone)
	b3TrackSnapshot(t, app, tracks[0], "fixed.flac", 12)
	mustExec(t, app, `UPDATE tracks SET content_fingerprint='fixed-audio' WHERE id=$1`, tracks[0])
	mustExec(t, app, `INSERT INTO scan_runs(id,status,started_at,finished_at) VALUES(gen_random_uuid(),'succeeded',now(),now())`)
	clock.advance(t, time.Second)
	b3RequireReleaseResult(t, app, release, "matched", "acoustid", "used", "")
	state := b3WorkState(t, app, "release", release)
	if state.RetryCount != 0 || state.NextAttemptAt != nil || acoust.Load() != 2 {
		t.Errorf("修复后应使用新快照识别成功，状态 %+v，AcoustID 请求 %d", state, acoust.Load())
	}
	for _, track := range tracks {
		b3RequireCount(t, app, 1, `SELECT count(*) FROM track_acoustic_fingerprints f JOIN tracks t ON t.id=f.track_id
			WHERE t.id=$1 AND f.status='done' AND f.attempts=1 AND
			(f.content_fingerprint,f.source_size,f.source_modified_at)=(t.content_fingerprint,t.source_size,t.source_modified_at)`, track)
	}
}
