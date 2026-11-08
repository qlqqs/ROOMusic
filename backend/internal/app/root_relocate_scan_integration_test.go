package app

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestPostgreSQLRelocateThenScanPreservesTrackAndReleaseUUIDs(t *testing.T) {
	application := newIntegrationTestApplication(t)
	admin := application.createUser(t, "admin")
	oldPath := createLibraryDirectory(t, application, "original-mount")
	newPath := createLibraryDirectory(t, application, "replacement-mount")
	for number := 1; number <= 2; number++ {
		path := filepath.Join(oldPath, "Album", fmt.Sprintf("%02d.flac", number))
		writeSyntheticTrack(t, path, byte(40+number), "TITLE=合成曲目", "ARTIST=合成艺人", "ALBUM=改路径专辑", fmt.Sprintf("TRACKNUMBER=%d", number))
		mtime := time.Date(2026, time.January, 2, 3, 4, number, 0, time.UTC)
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	rootID := createRoot(t, application, admin, oldPath, "create-synthetic")
	runScan(t, application, &admin)
	before := relocateCatalogIdentities(t, application, rootID)
	if len(before) != 2 {
		t.Fatalf("首次扫描应产生两首曲目：%v", before)
	}
	var releaseID string
	for relative, identity := range before {
		if identity.trackID == "" || identity.releaseID == "" || identity.sourceIdentity != rootID+":physical:v1:"+relative || identity.status != "present" {
			t.Fatalf("首次扫描身份不完整：%s %+v", relative, identity)
		}
		if releaseID != "" && releaseID != identity.releaseID {
			t.Fatal("两首合成曲目应属于同一个发行")
		}
		releaseID = identity.releaseID
	}

	// 只复制本测试生成的目录，保留文件和目录的修改时间，不读取真实音乐资产。
	copyRelocateSyntheticDirectory(t, oldPath, newPath)
	response := relocateRootRequest(t, application, admin, rootID, newPath, 1, "relocate-synthetic")
	requireRelocatedRoot(t, response, rootID, newPath, "active", 2, []string{})
	// 改路径本身不重建或迁移曲目。
	if after := relocateCatalogIdentities(t, application, rootID); !reflect.DeepEqual(after, before) {
		t.Fatalf("改路径事务改变了来源身份：before=%v after=%v", before, after)
	}
	// 原挂载位置不可用，确保后续成功扫描确实读取新路径，而非碰巧重读旧目录。
	if err := os.RemoveAll(oldPath); err != nil {
		t.Fatal(err)
	}
	scanID := startScanFeedback(t, application, &admin)
	report := scanFeedbackReport(t, application, &admin, scanID)
	after := relocateCatalogIdentities(t, application, rootID)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("重扫改变了曲目 UUID、发行 UUID 或来源身份：before=%v after=%v", before, after)
	}
	if missing := countRows(t, application, `SELECT COUNT(*) FROM tracks WHERE source_root_id=$1::uuid AND source_status='missing'`, rootID); missing != 0 {
		t.Fatalf("改路径后出现缺失曲目：%d", missing)
	}
	for _, kind := range []string{"tracks", "albums"} {
		counts, ok := report[kind].(map[string]any)
		if !ok || counts["missing"] != float64(0) {
			t.Fatalf("重扫报告的 %s.missing 应为 0：%v", kind, report)
		}
	}
	if releases := countRows(t, application, `SELECT COUNT(*) FROM releases`); releases != 1 {
		t.Fatalf("改路径重扫产生额外发行：%d", releases)
	}
	assertRelocatedRootState(t, application.database, rootID, newPath, "active", 2)
}

type relocatedCatalogIdentity struct {
	trackID, releaseID, sourceIdentity, status string
}

func relocateCatalogIdentities(t *testing.T, application *integrationTestApplication, rootID string) map[string]relocatedCatalogIdentity {
	t.Helper()
	rows, err := application.database.Query(`SELECT t.relative_path,t.id::text,m.release_id::text,t.source_identity,t.source_status
		FROM tracks t JOIN media m ON m.id=t.medium_id WHERE t.source_root_id=$1::uuid ORDER BY t.relative_path,t.id`, rootID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := map[string]relocatedCatalogIdentity{}
	for rows.Next() {
		var relative string
		var identity relocatedCatalogIdentity
		if err := rows.Scan(&relative, &identity.trackID, &identity.releaseID, &identity.sourceIdentity, &identity.status); err != nil {
			t.Fatal(err)
		}
		if _, exists := result[relative]; exists {
			t.Fatalf("同一相对路径产生重复曲目：%s", relative)
		}
		result[relative] = identity
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func copyRelocateSyntheticDirectory(t *testing.T, source, destination string) {
	t.Helper()
	if err := os.CopyFS(destination, os.DirFS(source)); err != nil {
		t.Fatalf("复制合成目录失败：%v", err)
	}
	if err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		copied := filepath.Join(destination, relative)
		if err := os.Chtimes(copied, info.ModTime(), info.ModTime()); err != nil {
			return err
		}
		copiedInfo, err := os.Stat(copied)
		if err != nil {
			return err
		}
		if !info.ModTime().Equal(copiedInfo.ModTime()) || info.Size() != copiedInfo.Size() && !info.IsDir() {
			t.Fatalf("复制没有保留大小和修改时间：%s", relative)
		}
		return nil
	}); err != nil {
		t.Fatalf("保留合成目录修改时间失败：%v", err)
	}
}
