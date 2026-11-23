package app

import (
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/playback"
	"github.com/qlqq/roomusic/backend/internal/playback/fingerprint"
)

const (
	b3LowTone  = "sine=frequency=263:sample_rate=11025:duration=12"
	b3HighTone = "sine=frequency=719:sample_rate=11025:duration=16"
)

// b3RequireTool 与其他集成用例一致：没有配置测试数据库（普通的 go test）时跳过；
// 经 scripts/test-integration.sh 运行时工具一定已取出，缺了就失败而不是静默跳过。
func b3RequireTool(t *testing.T, env string) string {
	t.Helper()
	if os.Getenv(postgrestest.Environment) == "" {
		t.Skipf("%s is not configured", postgrestest.Environment)
	}
	if path := os.Getenv(env); path != "" {
		return path
	}
	t.Fatalf("%s 未设置，请通过 scripts/test-integration.sh 运行", env)
	return ""
}

func b3RealFingerprinter(t *testing.T) *fingerprint.Fingerprinter {
	t.Helper()
	tool, err := fingerprint.Open(t.Context(), b3RequireTool(t, "ROOMUSIC_TEST_FFMPEG"), b3RequireTool(t, "ROOMUSIC_TEST_FPCALC"))
	if err != nil {
		t.Fatalf("指纹自检：%v", err)
	}
	return tool
}

func b3GenerateAudio(t *testing.T, path string, inputs ...string) {
	t.Helper()
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error"}
	for _, input := range inputs {
		args = append(args, "-f", "lavfi", "-i", input)
	}
	if len(inputs) == 2 {
		args = append(args, "-filter_complex", "[0:a][1:a]concat=n=2:v=0:a=1")
	}
	args = append(args, "-c:a", "flac", path)
	if out, err := exec.CommandContext(t.Context(), b3RequireTool(t, "ROOMUSIC_TEST_FFMPEG"), args...).CombinedOutput(); err != nil {
		t.Fatalf("生成合成 FLAC：%v\n%s", err, out)
	}
}

func b3Root(t *testing.T, app *integrationTestApplication) string {
	t.Helper()
	id := uuid.New()
	mustExec(t, app, `INSERT INTO library_roots(id,path,status,revision) VALUES($1,$2,'active',1)`, id, app.allowedRoot)
	return id
}

func b3ReleaseTracks(t *testing.T, app *integrationTestApplication, root, title, artist string, count int) (string, []string) {
	t.Helper()
	present := make([]string, count)
	for i := range present {
		present[i] = "present"
	}
	release, _ := insertGroupedRelease(t, app, root, groupedRelease{title: title, artist: artist, tracks: present})
	rows, err := app.database.Query(`SELECT t.id FROM tracks t JOIN media m ON m.id=t.medium_id WHERE m.release_id=$1 ORDER BY t.position`, release)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil || len(ids) != count {
		t.Fatalf("发行有 %d 首，期望 %d，错误=%v", len(ids), count, err)
	}
	return release, ids
}

func b3TrackSnapshot(t *testing.T, app *integrationTestApplication, track, relative string, duration float64) {
	t.Helper()
	info, err := os.Stat(filepath.Join(app.allowedRoot, relative))
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, app, `UPDATE tracks SET relative_path=$2,codec='flac',source_size=$3,source_modified_at=$4,content_fingerprint=$5,duration_seconds=$6 WHERE id=$1`,
		track, relative, info.Size(), info.ModTime(), "fixture-"+track, duration)
}

func TestPostgreSQLB3AcousticFingerprintCache(t *testing.T) {
	tool := b3RealFingerprinter(t)
	app := newIntegrationTestApplication(t)
	root := b3Root(t, app)
	_, tracks := b3ReleaseTracks(t, app, root, "缓存合成专辑", "合成艺人", 6)
	for _, sample := range []struct {
		filename string
		tones    []string
	}{
		{"first.flac", []string{b3LowTone}},
		{"second.flac", []string{b3HighTone}},
		{"image.flac", []string{b3LowTone, b3HighTone}},
		{"short.flac", []string{"sine=frequency=263:sample_rate=11025:duration=8"}},
		{"short-fraction.flac", []string{"sine=frequency=263:sample_rate=11025:duration=9.75"}},
	} {
		b3GenerateAudio(t, filepath.Join(app.allowedRoot, sample.filename), sample.tones...)
	}
	first, second, cue, short, corrupt, shortFraction := tracks[0], tracks[1], tracks[2], tracks[3], tracks[4], tracks[5]
	b3TrackSnapshot(t, app, first, "first.flac", 12)
	b3TrackSnapshot(t, app, second, "second.flac", 16)
	b3TrackSnapshot(t, app, cue, "image.flac", 28)
	b3TrackSnapshot(t, app, short, "short.flac", 8)
	b3TrackSnapshot(t, app, shortFraction, "short-fraction.flac", 9.75)
	mustExec(t, app, `UPDATE tracks SET source_kind='cue_virtual',relative_path='disc.cue#1',cue_sheet_path='disc.cue',
 cue_parent_relative_path='image.flac',cue_index_frames=0,cue_end_frames=900 WHERE id=$1`, cue)
	cache := playback.NewFingerprints(app.database, []string{app.allowedRoot}, tool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	get := func(id string, duration int) playback.TrackFingerprint {
		t.Helper()
		got, status, err := cache.Fingerprint(t.Context(), id)
		if err != nil || status != playback.FingerprintOK || got.Value == "" || got.DurationSeconds != duration {
			t.Fatalf("指纹为空=%t 时长=%d 状态=%v 错误=%v，期望时长 %d", got.Value == "", got.DurationSeconds, status, err, duration)
		}
		return got
	}
	computed := func(track string) time.Time {
		t.Helper()
		var when time.Time
		if err := app.database.QueryRow(`SELECT computed_at FROM track_acoustic_fingerprints WHERE track_id=$1`, track).Scan(&when); err != nil {
			t.Fatal(err)
		}
		return when
	}
	firstPrint, secondPrint := get(first, 12), get(second, 16)
	if firstPrint.Value == secondPrint.Value {
		t.Fatal("合成音频的两段必须能通过指纹区分")
	}
	if got := get(cue, 12); got != firstPrint {
		t.Fatal("CUE 首段与单独生成的首段指纹不同")
	}
	for _, row := range []struct {
		id   string
		want playback.TrackFingerprint
	}{{first, firstPrint}, {cue, firstPrint}} {
		when := computed(row.id)
		if got := get(row.id, row.want.DurationSeconds); got != row.want || !computed(row.id).Equal(when) {
			t.Fatal("缓存命中时指纹或计算时间变化")
		}
	}
	when := computed(first)
	mustExec(t, app, `UPDATE tracks SET content_fingerprint='changed-content' WHERE id=$1`, first)
	if get(first, 12) != firstPrint || !computed(first).After(when) {
		t.Fatal("内容指纹改变后未重算")
	}
	when = computed(cue)
	mustExec(t, app, `UPDATE tracks SET cue_index_frames=900,cue_end_frames=2100 WHERE id=$1`, cue)
	if get(cue, 16) != secondPrint || !computed(cue).After(when) {
		t.Fatal("CUE 帧改变后应重算且仅取第二段")
	}
	t.Run("算法版本", func(t *testing.T) {
		when := computed(first)
		mustExec(t, app, `UPDATE track_acoustic_fingerprints SET pipeline_version=$2 WHERE track_id=$1`, first, fingerprint.Version+1)
		if get(first, 12) != firstPrint || !computed(first).After(when) || countRows(t, app,
			`SELECT count(*) FROM track_acoustic_fingerprints WHERE track_id=$1 AND pipeline_version=$2`, first, fingerprint.Version) != 1 {
			t.Fatal("算法版本改变后未重算")
		}
	})
	for _, row := range []struct {
		id, name string
	}{{short, "8秒"}, {shortFraction, "9.75秒"}} {
		t.Run(row.name, func(t *testing.T) {
			got, status, err := cache.Fingerprint(t.Context(), row.id)
			category := programString(t, app, `SELECT COALESCE(failure_category,'') FROM track_acoustic_fingerprints WHERE track_id=$1`, row.id)
			if status != playback.FingerprintPermanent || err != nil || got.Value != "" || category != "too_short" {
				t.Errorf("短轨状态=%v 指纹为空=%t 类别=%q 错误=%v，期望 too_short", status, got.Value == "", category, err)
			}
		})
	}
	t.Run("解码失败冷却与重试上限", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(app.allowedRoot, "corrupt.flac"), []byte("invalid synthetic FLAC"), 0o600); err != nil {
			t.Fatal(err)
		}
		b3TrackSnapshot(t, app, corrupt, "corrupt.flac", 30)
		assertFailure := func(attempts int, want playback.FingerprintStatus, delay time.Duration) {
			t.Helper()
			got, status, err := cache.Fingerprint(t.Context(), corrupt)
			if err != nil || status != want || got.RetryAfter != delay || countRows(t, app,
				`SELECT count(*) FROM track_acoustic_fingerprints WHERE track_id=$1 AND attempts=$2 AND failure_category='decode_failed'`, corrupt, attempts) != 1 {
				t.Fatalf("解码失败，结果=%+v 状态=%v 错误=%v，期望次数=%d 状态=%v 退避=%v", got, status, err, attempts, want, delay)
			}
		}
		expire := func() {
			t.Helper()
			mustExec(t, app, `UPDATE track_acoustic_fingerprints SET computed_at=now()-INTERVAL '25 hours' WHERE track_id=$1`, corrupt)
		}
		assertFailure(1, playback.FingerprintTransient, 24*time.Hour)
		when := computed(corrupt)
		assertFailure(1, playback.FingerprintPermanent, 0)
		if !computed(corrupt).Equal(when) {
			t.Fatal("冷却期内重算")
		}
		expire()
		assertFailure(2, playback.FingerprintTransient, 24*time.Hour)
		expire()
		assertFailure(3, playback.FingerprintPermanent, 0)
		expire()
		assertFailure(3, playback.FingerprintPermanent, 0)
	})
	// CUE 区间与整轨时长均由真实目录快照解析。
	source, err := catalog.NewStore(app.database).LoadFingerprintSource(t.Context(), cue, fingerprint.Version)
	if err != nil || source.DurationSeconds != 16 || source.Cached == nil {
		t.Fatalf("CUE 快照时长=%d 缓存存在=%t 错误=%v", source.DurationSeconds, source.Cached != nil, err)
	}
}
