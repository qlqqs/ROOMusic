package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/ingest"
)

type scanGrowthFingerprinter struct {
	root     registeredRoot
	growing  string
	modified time.Time
}

func (f scanGrowthFingerprinter) Fingerprint(ctx context.Context, relative string) (string, error) {
	print, err := (rootFingerprinter{root: f.root}).Fingerprint(ctx, relative)
	if err != nil {
		return "", err
	}
	// 标签、大小已读入暂存。模拟复制工具此时写入后续字节并保留原 mtime。
	path := filepath.Join(f.root.Path, filepath.FromSlash(f.growing))
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return "", err
	}
	_, writeErr := file.Write(make([]byte, 64))
	return print, errors.Join(writeErr, file.Close(), os.Chtimes(path, f.modified, f.modified))
}

func TestPostgreSQL整理扫描中增长的文件不确认稳定(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "curation-moving-file")
	service := scannerTestApplication(fixture)
	const relative, other = "合成专辑/01.flac", "合成专辑/02.flac"
	writeAudio(t, root, relative, audioFile{seed: 61, title: "合成曲名", album: "合成专辑", track: 1})
	// 另一首曲目需要读帧计算指纹，提供两首均已暂存后的同步点。
	writeAudio(t, root, other, audioFile{seed: 62, zeroMD5: true, title: "第二首", album: "合成专辑", track: 2})
	modified := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	before := setBorrowFullFileTime(t, root, relative, modified)
	setBorrowFullFileTime(t, root, other, modified.Add(-time.Minute))
	scan := func(mode string) Scan {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		run, err := service.StartScan(ctx, StartOptions{Mode: mode})
		if err != nil {
			t.Fatal(err)
		}
		if !run.Started {
			t.Fatalf("未启动扫描：%+v", run)
		}
		if err := service.WaitForScans(ctx); err != nil {
			t.Fatal(err)
		}
		result, err := service.GetScan(ctx, run.Scan.ID)
		if err != nil || result.Status != "succeeded" {
			t.Fatalf("扫描结果 %+v，错误 %v", result, err)
		}
		return result
	}
	quiet := func() {
		t.Helper()
		if _, err := fixture.database.ExecContext(t.Context(), `UPDATE releases SET curation_changed_at=now()-interval '60 seconds' WHERE source_root_id=$1`, root.ID); err != nil {
			t.Fatal(err)
		}
	}

	scan(ScanModeIncremental)
	quiet()
	setBorrowFullFileTime(t, root, other, modified)
	service.fingerprinter = func(root registeredRoot) ingest.Fingerprinter {
		return scanGrowthFingerprinter{root: root, growing: relative, modified: modified}
	}
	scan(ScanModeFull)
	after, err := os.Stat(filepath.Join(root.Path, filepath.FromSlash(relative)))
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() != before.Size()+64 || !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("复制夹具大小 %d → %d，mtime %v → %v", before.Size(), after.Size(), before.ModTime(), after.ModTime())
	}
	assertDatabaseCount(t, fixture, `SELECT count(*) FROM releases WHERE source_root_id=$1 AND NOT curation_confirmed
		AND curation_changed_at>now()-interval '60 seconds'`, 1, root.ID)
	assertDatabaseCount(t, fixture, `SELECT count(*) FROM curation_stable_releases s JOIN releases r USING(id) WHERE r.source_root_id=$1`, 0, root.ID)

	service.fingerprinter = nil
	quiet()
	type observation struct {
		id, signature, changed string
		confirmed, stable      bool
	}
	readObservation := func() observation {
		t.Helper()
		var o observation
		err := fixture.database.QueryRowContext(t.Context(), `SELECT r.id,encode(r.curation_member_signature,'hex'),
			r.curation_changed_at::text,r.curation_confirmed,s.id IS NOT NULL
			FROM releases r LEFT JOIN curation_stable_releases s ON s.id=r.id WHERE r.source_root_id=$1`, root.ID).
			Scan(&o.id, &o.signature, &o.changed, &o.confirmed, &o.stable)
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	last := readObservation()
	result := scan(ScanModeIncremental)
	current := readObservation()
	if current.id != last.id || current.signature != last.signature || !current.confirmed {
		t.Fatalf("文件已停止增长，确认扫描应沿用最后观测的成员签名：扫描前 %+v，扫描后 %+v，诊断 %+v", last, current, result.Diagnostics)
	}
	if !current.stable || current.changed != last.changed {
		t.Fatalf("成员签名相同且静默已满 60 秒，应在这次确认扫描后放行：扫描前 %+v，扫描后 %+v", last, current)
	}
}
