package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type scaleHTTPRequest func(string, string, []byte, map[string]string) (*httptest.ResponseRecorder, float64, float64)

type scaleOnlineLoad struct {
	mu         sync.Mutex
	report     scaleOnlineLoadReport
	foreground map[string][]float64
	track      string
}

func newScaleOnlineLoad() *scaleOnlineLoad {
	return &scaleOnlineLoad{report: scaleOnlineLoadReport{ReadIntervalMS: float64(scaleReadProbeInterval / time.Millisecond), ScanM3IntervalMS: float64(scaleProbeInterval / time.Millisecond), RequestBudgetMS: float64(scaleRequestBudget / time.Millisecond), Cycles: []scaleProbeCycle{}}, foreground: map[string][]float64{}}
}
func (l *scaleOnlineLoad) record(cycle scaleProbeCycle) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.report.Cycles = append(l.report.Cycles, cycle)
	l.foreground["scan_start"] = append(l.foreground["scan_start"], cycle.ScanStartMS)
	if cycle.ScanStatus == "succeeded" {
		l.foreground["scan_complete"] = append(l.foreground["scan_complete"], cycle.ScanCompleteMS)
	}
	if cycle.CorrectionMS > 0 {
		l.foreground["probe_correction"] = append(l.foreground["probe_correction"], cycle.CorrectionMS)
	}
	if scaleArtistPhaseCycle(cycle) {
		l.report.ArtistPhaseCycles++
	}
}
func scaleArtistPhaseCycle(c scaleProbeCycle) bool {
	return c.SearchStatus == "ready" && c.ArtistStatus == "building" && c.CorrectionSearchStatus == "ready" && c.CorrectionArtistStatus == "building" && c.AfterCorrectionSearchStatus == "ready" && c.AfterCorrectionArtistStatus == "building" && c.DirectoryVerified && c.CorrectionVerified
}
func (l *scaleOnlineLoad) copyTo(report *scaleReport) {
	l.mu.Lock()
	defer l.mu.Unlock()
	report.OnlineLoad = l.report
	report.OnlineLoad.Cycles = append([]scaleProbeCycle(nil), l.report.Cycles...)
	for key, values := range l.foreground {
		if len(values) > 0 {
			report.Foreground[key] = scaleStats(values[0], append([]float64(nil), values...), 0)
		}
	}
}
func (l *scaleOnlineLoad) probeTrack() string { l.mu.Lock(); defer l.mu.Unlock(); return l.track }
func scaleReadProbePhase(ctx context.Context, db *sql.DB) (search, artist, phase string, err error) {
	err = db.QueryRowContext(ctx, `SELECT
 (SELECT status FROM catalog_projection_state WHERE capability='search'),
 (SELECT status FROM catalog_projection_state WHERE capability='artists'),
 COALESCE((SELECT p.phase FROM catalog_projection_phases p JOIN catalog_projection_state s ON s.build_id=p.build_id
 WHERE s.capability='artists' AND p.completed<p.total ORDER BY CASE p.subject_kind WHEN 'release' THEN 0 ELSE 1 END,p.phase LIMIT 1),'')`).Scan(&search, &artist, &phase)
	return
}

// 扫描器每轮真实解析同一合成文件；以本轮观察和succeeded核验，避免改音频造成播放source_changed。
func (l *scaleOnlineLoad) cycle(ctx context.Context, db *sql.DB, request scaleHTTPRequest, start time.Time, number int, rootID string) (cycle scaleProbeCycle, err error) {
	cycle.Number, cycle.StartedMS, cycle.Label = number, float64(time.Since(start))/float64(time.Millisecond), scaleProbeLabel(number)
	defer func() { cycle.CompletedMS = float64(time.Since(start)) / float64(time.Millisecond) }()
	cycle.SearchStatus, cycle.ArtistStatus, cycle.ArtistPhase, err = scaleReadProbePhase(ctx, db)
	if err != nil {
		return
	}
	began := time.Now()
	response, ms, _ := request(http.MethodPost, "/api/v1/scans", []byte(`{}`), nil)
	cycle.ScanStartMS = ms
	if response.Code != http.StatusAccepted {
		return cycle, fmt.Errorf("probe scan cycle=%d: %d %s", number, response.Code, response.Body)
	}
	var scan struct {
		ID string `json:"id"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &scan); err != nil {
		return
	}
	if scan.ID == "" {
		return cycle, fmt.Errorf("probe scan未返回ID")
	}
	cycle.ScanID = scan.ID
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err = db.QueryRowContext(ctx, `SELECT status FROM scan_runs WHERE id=$1::uuid`, scan.ID).Scan(&cycle.ScanStatus); err != nil {
			return
		}
		if cycle.ScanStatus == "succeeded" {
			break
		}
		if cycle.ScanStatus != "running" && cycle.ScanStatus != "pending" {
			return cycle, fmt.Errorf("probe scan cycle=%d status=%s", number, cycle.ScanStatus)
		}
		select {
		case <-ctx.Done():
			return cycle, ctx.Err()
		case <-ticker.C:
		}
	}
	cycle.ScanCompleteMS = float64(time.Since(began)) / float64(time.Millisecond)
	var title, album, artist string
	var observed bool
	err = db.QueryRowContext(ctx, `SELECT t.id::text,m.release_id::text,t.title,r.title,t.artist,t.observed_at>=s.started_at AND EXISTS(SELECT 1 FROM track_observations o WHERE o.track_id=t.id AND o.scan_run_id=s.id)
 FROM tracks t JOIN media m ON m.id=t.medium_id JOIN releases r ON r.id=m.release_id CROSS JOIN scan_runs s
 WHERE t.source_root_id=$1::uuid AND t.relative_path='probe.flac' AND t.source_status='present'
 AND r.source_root_id=$1::uuid AND s.id=$2::uuid`, rootID, scan.ID).Scan(&cycle.TrackID, &cycle.ReleaseID, &title, &album, &artist, &observed)
	if err != nil {
		return
	}
	if !scaleProbeDirectoryMatches(title, album, artist, observed) {
		return cycle, fmt.Errorf("扫描succeeded但探针目录/本轮来源观察/观察时间不正确 cycle=%d", number)
	}
	l.mu.Lock()
	l.track = cycle.TrackID
	l.mu.Unlock()
	cycle.DirectoryVerified = true
	cycle.CorrectionSearchStatus, cycle.CorrectionArtistStatus, cycle.CorrectionArtistPhase, err = scaleReadProbePhase(ctx, db)
	if err != nil {
		return
	}
	path := "/api/v1/releases/" + cycle.ReleaseID
	response, _, _ = request(http.MethodGet, path, nil, nil)
	if response.Code != http.StatusOK {
		return cycle, fmt.Errorf("probe revision: %d %s", response.Code, response.Body)
	}
	var before struct {
		Revision int64 `json:"revision"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &before); err != nil {
		return
	}
	cycle.ExpectedRevision = before.Revision
	body, marshalErr := json.Marshal(map[string]any{"expected_revision": before.Revision, "changes": map[string]any{"label": map[string]string{"mode": "set", "value": cycle.Label}}})
	if marshalErr != nil {
		return cycle, marshalErr
	}
	response, cycle.CorrectionMS, _ = request(http.MethodPatch, path+"/corrections", body, map[string]string{"Idempotency-Key": uuid.New()})
	if response.Code != http.StatusOK {
		return cycle, fmt.Errorf("probe correction cycle=%d: %d %s", number, response.Code, response.Body)
	}
	response, _, _ = request(http.MethodGet, path, nil, nil)
	if response.Code != http.StatusOK {
		return cycle, fmt.Errorf("probe correction readback: %d %s", response.Code, response.Body)
	}
	var after struct {
		Revision int64  `json:"revision"`
		Label    string `json:"label"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &after); err != nil {
		return
	}
	cycle.AppliedRevision = after.Revision
	if !scaleProbeCorrectionMatches(before.Revision, after.Revision, cycle.Label, after.Label) {
		return cycle, fmt.Errorf("probe correction未真实修改label/revision cycle=%d", number)
	}
	cycle.CorrectionVerified = true
	// 写后重新取状态；GET revision与PATCH之间发布不能冒充艺人回填期覆盖。
	cycle.AfterCorrectionSearchStatus, cycle.AfterCorrectionArtistStatus, cycle.AfterCorrectionArtistPhase, err = scaleReadProbePhase(ctx, db)
	return
}
func (l *scaleOnlineLoad) run(ctx context.Context, finished <-chan struct{}, db *sql.DB, request scaleHTTPRequest, start time.Time, rootID string) error {
	for number := 0; ; number++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-finished:
			return nil
		default:
		}
		cycle, err := l.cycle(ctx, db, request, start, number, rootID)
		l.record(cycle)
		if err != nil {
			return err
		}
		timer := time.NewTimer(time.Until(scaleNextProbe(start, time.Now())))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-finished:
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func scaleProbeDirectoryMatches(title, album, artist string, observedThisScan bool) bool {
	return title == "在线探针" && album == "在线探针专辑" && artist == "探针艺人" && observedThisScan
}
func scaleProbeCorrectionMatches(before, after int64, expected, actual string) bool {
	return before > 0 && after == before+1 && expected == actual
}

type scaleM3Acceptance struct {
	Requests, Successful int
	AllSucceeded, Pass   bool
	Latency              scaleLatency
}
type scaleScanAcceptance struct {
	Requests, Accepted, Succeeded, Verified int
	Pass                                    bool
}
type scaleReadAvailability struct {
	Requests, ServiceUnavailable, OtherFailures int
	Pass                                        bool
}
type scaleOnlineAcceptanceReport struct {
	Evaluated     bool
	M3            scaleM3Acceptance
	Scans         scaleScanAcceptance
	Audio, Search scaleReadAvailability
}

// M3取全部真实PATCH响应（含9900与周期探针），而不是平均各子集的p95。
func scaleEvaluateOnline(requests []scaleDiagnosticHTTP, cycles []scaleProbeCycle, complete bool) scaleOnlineAcceptanceReport {
	result := scaleOnlineAcceptanceReport{Evaluated: complete}
	var corrections []float64
	recordRead := func(stat *scaleReadAvailability, status int) {
		stat.Requests++
		if status == 503 {
			stat.ServiceUnavailable++
		} else if status < 200 || status >= 300 {
			stat.OtherFailures++
		}
	}
	for _, request := range requests {
		switch request.Kind {
		case "correction":
			result.M3.Requests++
			if request.Status == 200 {
				result.M3.Successful++
			}
			corrections = append(corrections, request.ElapsedMS)
		case "scan_start":
			result.Scans.Requests++
			if request.Status == 202 {
				result.Scans.Accepted++
			}
		case "audio":
			recordRead(&result.Audio, request.Status)
		case "suggestions", "search":
			recordRead(&result.Search, request.Status)
		}
	}
	result.M3.Latency.TargetMS = scaleM3P95TargetMS
	if len(corrections) > 0 {
		result.M3.Latency = scaleStats(corrections[0], corrections, scaleM3P95TargetMS)
	}
	result.M3.AllSucceeded = result.M3.Requests > 0 && result.M3.Successful == result.M3.Requests
	result.M3.Pass = complete && result.M3.AllSucceeded && result.M3.Latency.Pass
	for _, cycle := range cycles {
		if cycle.ScanStatus == "succeeded" {
			result.Scans.Succeeded++
		}
		if cycle.ScanStatus == "succeeded" && cycle.DirectoryVerified {
			result.Scans.Verified++
		}
	}
	result.Scans.Pass = complete && result.Scans.Requests > 0 && result.Scans.Accepted == result.Scans.Requests && result.Scans.Verified == result.Scans.Requests && len(cycles) == result.Scans.Requests
	for _, stat := range []*scaleReadAvailability{&result.Audio, &result.Search} {
		stat.Pass = complete && stat.Requests > 0 && stat.ServiceUnavailable == 0 && stat.OtherFailures == 0
	}
	return result
}
