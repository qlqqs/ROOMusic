package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/playback"
	"github.com/qlqq/roomusic/backend/internal/playback/analysis"
)

func TestPostgreSQLAnalysisUpgradeKeepsDoneResultsAndPrioritizesUnanalyzedTracks(t *testing.T) {
	application, rootID, groupID, releaseID := newAnalysisDBFixture(t)
	doneTrack := insertAnalysisTrack(t, application, rootID, releaseID, groupID, "done.flac")
	failedTrack := insertAnalysisTrack(t, application, rootID, releaseID, groupID, "failed.flac")
	pendingTrack := insertAnalysisTrack(t, application, rootID, releaseID, groupID, "pending.flac")
	// 普通排序本会先做成功曲目，避免随机 UUID 恰好符合升级后的优先级。
	mustExec(t, application, `UPDATE tracks SET position=CASE WHEN id=$1::uuid THEN 1 WHEN id=$2::uuid THEN 2 ELSE 3 END WHERE source_root_id=$3::uuid`, doneTrack.id, failedTrack.id, rootID)
	store := catalog.NewStore(application.database)
	ctx := context.Background()
	candidates, err := store.ListAnalysisCandidates(ctx, analysis.Version, 10)
	if err != nil || len(candidates) != 3 {
		t.Fatalf("准备候选：数量=%d，错误=%v", len(candidates), err)
	}
	byID := make(map[string]catalog.TrackAnalysisCandidate)
	for _, candidate := range candidates {
		byID[candidate.TrackID] = candidate
	}
	if err := store.SaveTrackAnalysis(ctx, byID[doneTrack.id], analysis.Version-1, catalog.TrackAnalysisResult{
		Status: "done", IntegratedLUFS: floatPtr(-20), TruePeakDBTP: floatPtr(-3), DurationMS: int32Ptr(1000), Waveform: []byte{1},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTrackAnalysis(ctx, byID[failedTrack.id], analysis.Version-1, catalog.TrackAnalysisResult{Status: "failed", FailureCategory: "ffmpeg_exit"}); err != nil {
		t.Fatal(err)
	}
	mustExec(t, application, `UPDATE track_analyses SET attempts=3 WHERE track_id=$1::uuid`, failedTrack.id)

	member := application.createUser(t, "user")
	response := application.request(t, http.MethodGet, "/api/v1/tracks/"+doneTrack.id+"/analysis", nil, &member, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("读取旧成功结果：HTTP %d，%s", response.Code, response.Body.String())
	}
	body := decodeJSONMap(t, response)
	data, ok := body["analysis"].(map[string]any)
	if body["status"] != "done" || !ok {
		t.Fatalf("旧成功结果不应因升级失效：%#v", body)
	}
	if data["integrated_lufs"] != float64(-20) || data["duration_ms"] != float64(1000) {
		t.Fatalf("旧分析数据未保留：%#v", data)
	}
	for _, field := range []string{"loudness_range_lu", "sample_peak_dbfs"} {
		if value, exists := data[field]; !exists || value != nil {
			t.Fatalf("旧成功结果的 %s 应显式为 null：%#v", field, data)
		}
	}
	response = application.request(t, http.MethodGet, "/api/v1/tracks/"+failedTrack.id+"/analysis", nil, &member, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("读取旧失败结果：HTTP %d，%s", response.Code, response.Body.String())
	}
	body = decodeJSONMap(t, response)
	if body["status"] != "pending" || body["analysis"] != nil {
		t.Fatalf("旧版本的终止失败应重新待分析：%#v", body)
	}

	candidates, err = store.ListAnalysisCandidates(ctx, analysis.Version, 10)
	if err != nil || len(candidates) != 3 {
		t.Fatalf("升级后候选：数量=%d，错误=%v", len(candidates), err)
	}
	for index, want := range []string{failedTrack.id, pendingTrack.id, doneTrack.id} {
		if candidates[index].TrackID != want {
			t.Fatalf("候选[%d]=%s，期望 %s；没有可用结果的曲目应先于旧成功结果", index, candidates[index].TrackID, want)
		}
	}
}

func TestPostgreSQLAnalysisDisplayDurationsRespectThresholdAndSnapshot(t *testing.T) {
	application, rootID, groupID, releaseID := newAnalysisDBFixture(t)
	cases := []struct {
		name            string
		scanned         *float64
		decodedMS       int32
		snapshotChanged bool
		want            float64
		track           analysisDBTrack
	}{
		// 200 秒的 1% 是 2 秒；恰好达到阈值仍保留扫描值，也覆盖百分比高于 1 秒的分支。
		{name: "百分比阈值边界保留扫描值", scanned: floatPtr(202), decodedMS: 200000, want: 202},
		{name: "一秒下限内保留扫描值", scanned: floatPtr(10.5), decodedMS: 10000, want: 10.5},
		{name: "超过阈值取解码值", scanned: floatPtr(8.5), decodedMS: 10000, want: 10},
		{name: "扫描值为空取解码值", decodedMS: 10000, want: 10},
		{name: "快照不符保留扫描值", scanned: floatPtr(8.5), decodedMS: 10000, snapshotChanged: true, want: 8.5},
	}
	for index := range cases {
		cases[index].track = insertAnalysisTrack(t, application, rootID, releaseID, groupID, cases[index].name+".flac")
		mustExec(t, application, `UPDATE tracks SET duration_seconds=$2 WHERE id=$1::uuid`, cases[index].track.id, cases[index].scanned)
	}
	store := catalog.NewStore(application.database)
	ctx := context.Background()
	candidates, err := store.ListAnalysisCandidates(ctx, analysis.Version, 10)
	if err != nil || len(candidates) != len(cases) {
		t.Fatalf("准备时长候选：数量=%d，错误=%v", len(candidates), err)
	}
	byID := make(map[string]catalog.TrackAnalysisCandidate)
	for _, candidate := range candidates {
		byID[candidate.TrackID] = candidate
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// 升级前的成功结果也应立即用于时长校正，无需等待重新分析。
			if err := store.SaveTrackAnalysis(ctx, byID[tc.track.id], analysis.Version-1, catalog.TrackAnalysisResult{
				Status: "done", DurationMS: int32Ptr(tc.decodedMS), Waveform: []byte{1},
			}); err != nil {
				t.Fatal(err)
			}
			if tc.snapshotChanged {
				mustExec(t, application, `UPDATE tracks SET source_size=source_size+1 WHERE id=$1::uuid`, tc.track.id)
			}
			var displayed float64
			var scanned sql.NullFloat64
			if err := application.database.QueryRow(`SELECT dd.duration_seconds,t.duration_seconds FROM track_display_durations dd JOIN tracks t ON t.id=dd.track_id WHERE t.id=$1::uuid`, tc.track.id).Scan(&displayed, &scanned); err != nil {
				t.Fatal(err)
			}
			if displayed != tc.want {
				t.Fatalf("视图时长=%v，期望 %v", displayed, tc.want)
			}
			if scanned.Valid != (tc.scanned != nil) || (tc.scanned != nil && scanned.Float64 != *tc.scanned) {
				t.Fatalf("分析不得改写扫描时长：%+v，原值=%v", scanned, tc.scanned)
			}
			got, err := store.LoadTrackDuration(ctx, tc.track.id)
			if err != nil || got != tc.want {
				t.Fatalf("LoadTrackDuration=%v，期望 %v，错误=%v", got, tc.want, err)
			}
		})
	}
}

func TestPostgreSQLAnalysisCorrectsVBRWithoutXingDurationInReleaseHTTP(t *testing.T) {
	ffmpeg := requireTestFFmpeg(t)
	application := newIntegrationTestApplication(t)
	admin := application.createUser(t, "admin")
	rootPath := createLibraryDirectory(t, application, "analysis-vbr")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "anullsrc=r=44100:cl=stereo:d=4",
		"-f", "lavfi", "-i", "anoisesrc=r=44100:d=6:a=0.3",
		"-filter_complex", "[0:a][1:a]concat=n=2:v=0:a=1,aformat=channel_layouts=stereo",
		"-c:a", "libmp3lame", "-q:a", "0", "-write_xing", "0",
		"-metadata", "album=VBR duration", "-metadata", "artist=Test Artist", "-metadata", "title=VBR without Xing",
		filepath.Join(rootPath, "vbr-noxing.mp3"))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("生成无 Xing 头的 VBR MP3：%v，%s", err, output)
	}
	rootID := createRoot(t, application, admin, rootPath, "create-analysis-vbr")
	runScan(t, application, &admin)
	var trackID, releaseID string
	var scannedDuration float64
	if err := application.database.QueryRow(`SELECT t.id::text,m.release_id::text,t.duration_seconds FROM tracks t JOIN media m ON m.id=t.medium_id WHERE t.source_root_id=$1::uuid AND t.relative_path='vbr-noxing.mp3' AND t.source_status='present'`, rootID).Scan(&trackID, &releaseID, &scannedDuration); err != nil {
		t.Fatalf("读取扫描入库的 MP3：%v", err)
	}
	if math.Abs(scannedDuration-10) <= 1 {
		t.Fatalf("样本未产生需要校正的扫描偏差：扫描时长=%v 秒", scannedDuration)
	}
	analyzer, err := analysis.Open(ctx, ffmpeg)
	if err != nil {
		t.Fatalf("打开真实分析器：%v", err)
	}
	// 扫描完成后才启动 worker，避免空库的 1 分钟等待；仍走真实安全打开、分析与保存路径。
	worker := playback.NewAnalysisWorker(application.database, []string{application.allowedRoot}, analyzer,
		func(context.Context) (bool, error) { return false, nil }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		worker.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-workerDone:
		case <-time.After(5 * time.Second):
			t.Error("取消后分析 worker 未退出")
		}
	})
	store := catalog.NewStore(application.database)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	var decodedDuration float64
	for {
		result, err := store.LoadTrackAnalysis(ctx, trackID, analysis.Version)
		if err != nil {
			t.Fatalf("等待真实分析结果：%v", err)
		}
		if result.Status == "failed" {
			t.Fatal("真实 MP3 分析失败")
		}
		if result.Status == "done" {
			if result.Analysis == nil || result.Analysis.DurationMS == nil {
				t.Fatalf("分析完成但没有解码时长：%+v", result)
			}
			decodedDuration = float64(*result.Analysis.DurationMS) / 1000
			if math.Abs(decodedDuration-10) >= 0.1 {
				t.Fatalf("解码时长=%v 秒，期望约 10 秒", decodedDuration)
			}
			if result.Analysis.LoudnessRangeLU == nil || result.Analysis.SamplePeakDBFS == nil {
				t.Fatalf("worker 未保存响度范围或采样峰值：%+v", result.Analysis)
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("等待分析完成超时：%v", ctx.Err())
		case <-ticker.C:
		}
	}
	response := application.request(t, http.MethodGet, "/api/v1/releases/"+releaseID, nil, &admin, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("读取发行详情：HTTP %d，%s", response.Code, response.Body.String())
	}
	var detail catalog.ReleaseDetail
	if err := json.Unmarshal(response.Body.Bytes(), &detail); err != nil {
		t.Fatalf("解码发行详情：%v", err)
	}
	if len(detail.Media) != 1 || len(detail.Media[0].Tracks) != 1 {
		t.Fatalf("发行详情应只有一首样本：%+v", detail.Media)
	}
	track := detail.Media[0].Tracks[0]
	if track.ID != trackID || track.DurationSeconds == nil || *track.DurationSeconds != decodedDuration {
		t.Fatalf("发行详情时长未取解码结果：曲目=%+v，解码时长=%v 秒，扫描时长=%v 秒", track, decodedDuration, scannedDuration)
	}
	var unchangedDuration float64
	if err := application.database.QueryRow(`SELECT duration_seconds FROM tracks WHERE id=$1::uuid`, trackID).Scan(&unchangedDuration); err != nil {
		t.Fatal(err)
	}
	if unchangedDuration != scannedDuration {
		t.Fatalf("分析改写了扫描时长：分析后=%v，原扫描值=%v", unchangedDuration, scannedDuration)
	}
}
