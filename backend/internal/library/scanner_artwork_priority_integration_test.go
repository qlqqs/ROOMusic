package library

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/artwork"
	"github.com/qlqq/roomusic/backend/internal/library/parser"
	"github.com/qlqq/roomusic/backend/internal/library/parser/parsertest"
)

// writeArtworkPriorityFLAC 复用合成音频与图片块，专辑、碟号和音频指纹均由样本明确指定。
func writeArtworkPriorityFLAC(t *testing.T, root registeredRoot, relative, album string, disc int, seed byte, picture []byte) {
	t.Helper()
	comments := []string{"TITLE=合成曲目", "ARTIST=合成艺人", "ALBUM=" + album, "TRACKNUMBER=1", fmt.Sprintf("DISCNUMBER=%d", disc)}
	if len(picture) > 0 {
		block := parsertest.FLACPicture(3, "image/jpeg", 1, 1, picture)
		comments = append(comments, "METADATA_BLOCK_PICTURE="+base64.StdEncoding.EncodeToString(block))
	}
	writeScanFixtureFile(t, root, relative, parsertest.FLACWithAudio(t, [16]byte{seed}, []byte{seed}, comments...))
}

// assertArtworkPriorityBinding 按曲目查所属发行，并核对来源、MIME 与受管文件字节，避免只计数而漏掉串图。
// want 为空时，发行必须存在且没有封面关系。
func assertArtworkPriorityBinding(t *testing.T, fixture *libraryFixture, service *Service, root registeredRoot, relative, kind, source string, want []byte) string {
	t.Helper()
	var releaseID, gotKind string
	var gotSource, mime, key sql.NullString
	if err := fixture.database.QueryRow(`SELECT r.id::text,r.candidate_kind,a.source_type,a.mime_type,a.storage_key
		FROM tracks t JOIN media m ON m.id=t.medium_id JOIN releases r ON r.id=m.release_id
		LEFT JOIN release_artworks a ON a.release_id=r.id
		WHERE t.source_root_id=$1::uuid AND t.relative_path=$2 AND t.source_status='present'`, root.ID, relative).
		Scan(&releaseID, &gotKind, &gotSource, &mime, &key); err != nil {
		t.Fatalf("读取曲目所属发行及封面失败：%s：%v", relative, err)
	}
	if kind != "" && gotKind != kind {
		t.Fatalf("%s 的候选类型为 %s，期望 %s", relative, gotKind, kind)
	}
	if len(want) == 0 {
		if gotSource.Valid || mime.Valid || key.Valid {
			t.Fatalf("%s 不应绑定封面：%s/%s/%s", relative, gotSource.String, mime.String, key.String)
		}
		return releaseID
	}
	if gotSource.String != source || mime.String != "image/jpeg" || !key.Valid {
		t.Fatalf("%s 封面来源或格式不符：%s/%s，期望 %s/image/jpeg", relative, gotSource.String, mime.String, source)
	}
	data, err := os.ReadFile(filepath.Join(service.dataDirectory, "artwork", key.String))
	if err != nil || !bytes.Equal(data, want) {
		t.Fatalf("%s 的受管封面字节不符：读取错误=%v，实际=%x，期望=%x", relative, err, data, want)
	}
	return releaseID
}

// AC2：原目录只命中 front 子串时，内嵌封面优先。
func TestPostgreSQLArtworkPriority内嵌优先于front子串(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "artwork-priority-front")
	service, logs := newIncrementalTestService(t, fixture)
	embedded := jpegArtworkOfWidth(9)
	writeArtworkPriorityFLAC(t, root, "Album/01.flac", "专辑", 1, 1, embedded)
	writeScanFixtureFile(t, root, "Album/frontier.jpg", jpegArtworkOfWidth(3))
	runIncrementalTestScan(t, service, logs, root)
	assertArtworkPriorityBinding(t, fixture, service, root, "Album/01.flac", "", "embedded", embedded)
}

// AC3 第二条：专辑只用自己的封面，旁边没有封面的专辑也不能继承艺人目录图片。
func TestPostgreSQLArtworkPriority艺人目录封面不向下继承(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "artwork-priority-artist")
	service, logs := newIncrementalTestService(t, fixture)
	cover := jpegArtworkOfWidth(5)
	writeArtworkPriorityFLAC(t, root, "Artist/AlbumA/01.flac", "专辑甲", 1, 1, nil)
	writeArtworkPriorityFLAC(t, root, "Artist/AlbumB/01.flac", "专辑乙", 1, 2, nil)
	writeScanFixtureFile(t, root, "Artist/cover.jpg", jpegArtworkOfWidth(3))
	writeScanFixtureFile(t, root, "Artist/AlbumA/cover.jpg", cover)
	runIncrementalTestScan(t, service, logs, root)
	assertArtworkPriorityBinding(t, fixture, service, root, "Artist/AlbumA/01.flac", "", "folder", cover)
	assertArtworkPriorityBinding(t, fixture, service, root, "Artist/AlbumB/01.flac", "", "", nil)
	assertArtworks(t, fixture, root, "folder:image/jpeg:5")
}

// AC4：同目录多专辑各用自己的内嵌图；缺少内嵌图的发行不使用共享 cover.jpg。
// 文件名不带连续编号，避免已有的连续性归组规则把两张专辑合并。
func TestPostgreSQLArtworkPriority同目录多专辑不串图(t *testing.T) {
	for _, tc := range []struct {
		name   string
		second []byte
	}{
		{"两张专辑各有内嵌封面", jpegArtworkOfWidth(8)},
		{"第二张专辑没有内嵌封面", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newLibraryFixture(t)
			root := insertScannerRoot(t, fixture, "artwork-priority-split")
			service, logs := newIncrementalTestService(t, fixture)
			first := jpegArtworkOfWidth(4)
			writeArtworkPriorityFLAC(t, root, "Mixed/a.flac", "专辑甲", 1, 1, first)
			writeArtworkPriorityFLAC(t, root, "Mixed/b.flac", "专辑乙", 1, 2, tc.second)
			writeScanFixtureFile(t, root, "Mixed/cover.jpg", jpegArtworkOfWidth(12))
			runIncrementalTestScan(t, service, logs, root)
			firstID := assertArtworkPriorityBinding(t, fixture, service, root, "Mixed/a.flac", "same_dir_split", "embedded", first)
			secondID := assertArtworkPriorityBinding(t, fixture, service, root, "Mixed/b.flac", "same_dir_split", "embedded", tc.second)
			if firstID == secondID {
				t.Fatal("同目录的两张专辑被错误合并")
			}
			assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM releases WHERE source_root_id=$1::uuid`, 2, root.ID)
		})
	}
}

// AC5 第一条：共享整理范围的 Box 叶子各保留自己的内嵌图，父目录图片只用于最后兜底。
func TestPostgreSQLArtworkPriorityBox叶子各用内嵌封面(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "artwork-priority-box")
	service, logs := newIncrementalTestService(t, fixture)
	first, second := jpegArtworkOfWidth(4), jpegArtworkOfWidth(8)
	writeArtworkPriorityFLAC(t, root, "Box/Disc 1/01.flac", "专辑甲", 1, 1, first)
	writeArtworkPriorityFLAC(t, root, "Box/Disc 3/01.flac", "专辑乙", 3, 2, second)
	writeScanFixtureFile(t, root, "Box/cover.jpg", jpegArtworkOfWidth(12))
	runIncrementalTestScan(t, service, logs, root)
	firstID := assertArtworkPriorityBinding(t, fixture, service, root, "Box/Disc 1/01.flac", "box_leaf", "embedded", first)
	secondID := assertArtworkPriorityBinding(t, fixture, service, root, "Box/Disc 3/01.flac", "box_leaf", "embedded", second)
	if firstID == secondID {
		t.Fatal("专辑标签冲突的 Box 叶子不应合并")
	}
}

// Box 父目录自身有音频时，即使存在精确名封面，碟目录叶子也不能继承。
func TestPostgreSQLArtworkPriorityBox父目录直接音频阻止兜底(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "artwork-priority-box-direct")
	service, logs := newIncrementalTestService(t, fixture)
	cover := jpegArtworkOfWidth(7)
	writeArtworkPriorityFLAC(t, root, "Box/Disc 1/first.flac", "专辑甲", 1, 1, nil)
	writeArtworkPriorityFLAC(t, root, "Box/Disc 3/second.flac", "专辑乙", 3, 2, nil)
	writeArtworkPriorityFLAC(t, root, "Box/direct.flac", "父目录专辑", 1, 3, nil)
	writeScanFixtureFile(t, root, "Box/cover.jpg", cover)
	runIncrementalTestScan(t, service, logs, root)
	assertArtworkPriorityBinding(t, fixture, service, root, "Box/Disc 1/first.flac", "box_leaf", "", nil)
	assertArtworkPriorityBinding(t, fixture, service, root, "Box/Disc 3/second.flac", "box_leaf", "", nil)
	assertArtworkPriorityBinding(t, fixture, service, root, "Box/direct.flac", "ordinary_directory", "folder", cover)
}

// 碟目录音频缓存命中时仍按本轮全根布局重算独占性，移除或恢复既有父目录封面。
func TestPostgreSQLArtworkPriorityBox父目录独占变化重选封面(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "artwork-priority-box-exclusive")
	service, logs := newIncrementalTestService(t, fixture)
	cover := jpegArtworkOfWidth(7)
	writeArtworkPriorityFLAC(t, root, "Box/Disc 1/first.flac", "专辑甲", 1, 1, nil)
	writeArtworkPriorityFLAC(t, root, "Box/Disc 3/second.flac", "专辑乙", 3, 2, nil)
	writeScanFixtureFile(t, root, "Box/cover.jpg", cover)
	runIncrementalTestScan(t, service, logs, root)
	leaves := map[string]string{}
	for _, relative := range []string{"Box/Disc 1/first.flac", "Box/Disc 3/second.flac"} {
		leaves[relative] = assertArtworkPriorityBinding(t, fixture, service, root, relative, "box_leaf", "folder", cover)
	}

	writeArtworkPriorityFLAC(t, root, "Box/普通专辑/other.flac", "普通专辑", 1, 3, nil)
	_, added := runIncrementalTestScan(t, service, logs, root)
	if added.FilesParsed != 1 || added.ParseCacheHits != 2 {
		t.Fatalf("新增兄弟专辑应仅解析新音频，两个叶子命中缓存：%+v", added)
	}
	assertArtworkPriorityBinding(t, fixture, service, root, "Box/普通专辑/other.flac", "ordinary_directory", "", nil)
	for relative, releaseID := range leaves {
		if got := assertArtworkPriorityBinding(t, fixture, service, root, relative, "box_leaf", "", nil); got != releaseID {
			t.Errorf("新增兄弟专辑改变叶子发行身份：%s：前=%s，后=%s", relative, releaseID, got)
		}
	}

	if err := os.RemoveAll(filepath.Join(root.Path, "Box/普通专辑")); err != nil {
		t.Fatal(err)
	}
	_, removed := runIncrementalTestScan(t, service, logs, root)
	if removed.FilesParsed != 0 || removed.ParseCacheHits != 2 {
		t.Fatalf("删除兄弟专辑后两个叶子仍应命中缓存：%+v", removed)
	}
	for relative, releaseID := range leaves {
		if got := assertArtworkPriorityBinding(t, fixture, service, root, relative, "box_leaf", "folder", cover); got != releaseID {
			t.Errorf("删除兄弟专辑改变叶子发行身份：%s：前=%s，后=%s", relative, releaseID, got)
		}
	}
}

// 同目录同专辑拆成格式版本后，各发行仍使用自己音频里的内嵌封面。
func TestPostgreSQLArtworkPriority格式版本各用内嵌封面(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "artwork-priority-formats")
	service, logs := newIncrementalTestService(t, fixture)
	flac, mp3 := jpegArtworkOfWidth(4), jpegArtworkOfWidth(8)
	writeArtworkPriorityFLAC(t, root, "Album/01.flac", "Album", 1, 1, flac)
	writeIncrementalTestMP3(t, root, "Album/01.mp3", 2, 1, mp3)
	runIncrementalTestScan(t, service, logs, root)
	flacID := assertArtworkPriorityBinding(t, fixture, service, root, "Album/01.flac", "format_split", "embedded", flac)
	mp3ID := assertArtworkPriorityBinding(t, fixture, service, root, "Album/01.mp3", "format_split", "embedded", mp3)
	if flacID == mp3ID {
		t.Fatal("两种格式应属于各自的发行版本")
	}
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM releases WHERE source_root_id=$1::uuid`, 2, root.ID)
}

// 不同专辑的碟目录保持 Box 叶子；格式拆分后仍能用 Box 父目录的精确名封面兜底。
func TestPostgreSQLArtworkPriority格式拆分Box叶子使用父目录封面(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "artwork-priority-box-formats")
	service, logs := newIncrementalTestService(t, fixture)
	cover := jpegArtworkOfWidth(7)
	writeArtworkPriorityFLAC(t, root, "Box/Disc 1/01.flac", "Album", 1, 1, nil)
	writeIncrementalTestMP3(t, root, "Box/Disc 1/01.mp3", 2, 1, nil)
	writeArtworkPriorityFLAC(t, root, "Box/Disc 3/01.flac", "另一专辑", 3, 3, nil)
	writeScanFixtureFile(t, root, "Box/cover.jpg", cover)
	runIncrementalTestScan(t, service, logs, root)
	flacID := assertArtworkPriorityBinding(t, fixture, service, root, "Box/Disc 1/01.flac", "format_split", "folder", cover)
	mp3ID := assertArtworkPriorityBinding(t, fixture, service, root, "Box/Disc 1/01.mp3", "format_split", "folder", cover)
	if flacID == mp3ID {
		t.Fatal("Box 叶子的两种格式应属于各自的发行版本")
	}
	assertArtworkPriorityBinding(t, fixture, service, root, "Box/Disc 3/01.flac", "box_leaf", "folder", cover)
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM releases WHERE source_root_id=$1::uuid`, 3, root.ID)
}

// 注册根里的图片不属于任何一个 Box 叶子，普通叶子与格式拆分叶子都不能继承。
func TestPostgreSQLArtworkPriorityBox叶子不使用注册根封面(t *testing.T) {
	for _, split := range []bool{false, true} {
		name, kind := "普通Box叶子", "box_leaf"
		if split {
			name, kind = "格式拆分Box叶子", "format_split"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newLibraryFixture(t)
			root := insertScannerRoot(t, fixture, "artwork-priority-root-box")
			service, logs := newIncrementalTestService(t, fixture)
			writeArtworkPriorityFLAC(t, root, "Disc 1/01.flac", "Album", 1, 1, nil)
			if split {
				writeIncrementalTestMP3(t, root, "Disc 1/01.mp3", 2, 1, nil)
			}
			writeScanFixtureFile(t, root, "cover.jpg", jpegArtworkOfWidth(6))
			runIncrementalTestScan(t, service, logs, root)
			flacID := assertArtworkPriorityBinding(t, fixture, service, root, "Disc 1/01.flac", kind, "", nil)
			if split {
				mp3ID := assertArtworkPriorityBinding(t, fixture, service, root, "Disc 1/01.mp3", kind, "", nil)
				if flacID == mp3ID {
					t.Fatal("注册根下的两种格式应属于各自的发行版本")
				}
			}
			assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM release_artworks a JOIN releases r ON r.id=a.release_id WHERE r.source_root_id=$1::uuid`, 0, root.ID)
		})
	}
}

// AC6：高档候选损坏时继续向下找；最终找到有效图片的扫描不产生封面失败诊断。
func TestPostgreSQLArtworkPriority损坏封面逐档回退(t *testing.T) {
	for _, tc := range []struct {
		name, path, source string
		embedded, folder   []byte
	}{
		{"精确名损坏后使用内嵌", "Album/cover.jpg", "embedded", jpegArtworkOfWidth(6), []byte("损坏的图片")},
		{"内嵌无效后使用附属目录", "Album/scans/front.jpg", "folder", []byte("损坏的内嵌图片"), jpegArtworkOfWidth(7)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newLibraryFixture(t)
			root := insertScannerRoot(t, fixture, "artwork-priority-fallback")
			service, logs := newIncrementalTestService(t, fixture)
			writeArtworkPriorityFLAC(t, root, "Album/01.flac", "专辑", 1, 1, tc.embedded)
			writeScanFixtureFile(t, root, tc.path, tc.folder)
			scan, _ := runIncrementalTestScan(t, service, logs, root)
			want := tc.embedded
			if tc.source == "folder" {
				want = tc.folder
			}
			assertArtworkPriorityBinding(t, fixture, service, root, "Album/01.flac", "", tc.source, want)
			assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM scan_diagnostics WHERE scan_run_id=$1::uuid`, 0, scan.ID)
		})
	}
}

// artworkPriorityM4A 沿用解析器修复测试的 atom 布局，可写入类型码为 0 的 covr 与 iTunes 自由格式标签。
func artworkPriorityM4A(picture []byte, freeformTags ...[2]string) []byte {
	atom := func(kind string, payload []byte) []byte {
		result := binary.BigEndian.AppendUint32(nil, uint32(8+len(payload)))
		result = append(result, kind...)
		return append(result, payload...)
	}
	entry := make([]byte, 28)
	binary.BigEndian.PutUint16(entry[16:18], 2)
	binary.BigEndian.PutUint16(entry[18:20], 16)
	binary.BigEndian.PutUint32(entry[24:28], 44100<<16)
	stsd := atom("stsd", append([]byte{0, 0, 0, 0, 0, 0, 0, 1}, atom("mp4a", entry)...))
	mdhd := make([]byte, 20)
	binary.BigEndian.PutUint32(mdhd[12:16], 44100)
	binary.BigEndian.PutUint32(mdhd[16:20], 88200)
	track := atom("trak", atom("mdia", append(atom("mdhd", mdhd), atom("minf", atom("stbl", stsd))...)))
	var items []byte
	if len(picture) > 0 {
		items = atom("covr", atom("data", append(make([]byte, 8), picture...)))
	}
	for _, tag := range freeformTags {
		mean := atom("mean", append(make([]byte, 4), "com.apple.iTunes"...))
		name := atom("name", append(make([]byte, 4), tag[0]...))
		dataHeader := make([]byte, 8)
		binary.BigEndian.PutUint32(dataHeader, 1)
		data := atom("data", append(dataHeader, tag[1]...))
		items = append(items, atom("----", bytes.Join([][]byte{mean, name, data}, nil))...)
	}
	metadata := atom("udta", atom("meta", append(make([]byte, 4), atom("ilst", items)...)))
	return bytes.Join([][]byte{
		atom("ftyp", []byte("M4A \x00\x00\x00\x00")),
		atom("moov", append(track, metadata...)), atom("mdat", []byte{1, 2, 3, 4}),
	}, nil)
}

// AC10：先确认样本携带非标准声明，再经完整扫描验证探测出的 JPEG MIME 和原字节实际入库。
func TestPostgreSQLArtworkPriority声明MIME不阻止JPEG保存(t *testing.T) {
	for _, tc := range []struct{ name, extension, declared string }{
		{"APIC声明image_jpg", ".mp3", "image/jpg"},
		{"M4A类型码零", ".m4a", "application/octet-stream"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newLibraryFixture(t)
			root := insertScannerRoot(t, fixture, "artwork-priority-mime")
			service, logs := newIncrementalTestService(t, fixture)
			picture := jpegArtworkOfWidth(9)
			relative := "Album/01" + tc.extension
			if tc.extension == ".mp3" {
				writeIncrementalTestMP3(t, root, relative, 1, 1, picture)
				data, err := os.ReadFile(filepath.Join(root.Path, relative))
				if err != nil {
					t.Fatal(err)
				}
				// 两个 MIME 字符串等长，替换不改变 ID3 标签和帧长度。
				writeScanFixtureFile(t, root, relative, bytes.Replace(data, []byte("image/png"), []byte("image/jpg"), 1))
			} else {
				writeScanFixtureFile(t, root, relative, artworkPriorityM4A(picture))
			}
			observation, err := parser.ParseAudioFile(filepath.Join(root.Path, relative))
			if err != nil || observation.ArtworkMIME != tc.declared || !bytes.Equal(observation.Artwork, picture) {
				t.Fatalf("合成样本未携带预期 MIME 和 JPEG：MIME=%q，错误=%v", observation.ArtworkMIME, err)
			}
			runIncrementalTestScan(t, service, logs, root)
			assertArtworkPriorityBinding(t, fixture, service, root, relative, "", "embedded", picture)
		})
	}
}

// AC11：用旧签名模拟 GroupingVersion 改变；另覆盖 CommonVersion 过期。
// 仅修改隔离库的缓存与旧关系，音频和封面文件保持原字节、大小与 mtime。
func TestPostgreSQLArtworkPriority增量升版重选未改动文件封面(t *testing.T) {
	for _, oldParser := range []bool{false, true} {
		name := "归组签名失效且解析缓存命中"
		if oldParser {
			name = "公共解析版本及归组签名失效"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newLibraryFixture(t)
			root := insertScannerRoot(t, fixture, "artwork-priority-upgrade")
			service, logs := newIncrementalTestService(t, fixture)
			embedded, exact, shared := jpegArtworkOfWidth(4), jpegArtworkOfWidth(8), jpegArtworkOfWidth(12)
			writeArtworkPriorityFLAC(t, root, "Album/01.flac", "普通专辑", 1, 1, embedded)
			writeArtworkPriorityFLAC(t, root, "Mixed/a.flac", "专辑甲", 1, 2, embedded)
			writeArtworkPriorityFLAC(t, root, "Mixed/b.flac", "专辑乙", 1, 3, nil)
			writeScanFixtureFile(t, root, "Album/cover.jpg", exact)
			writeScanFixtureFile(t, root, "Mixed/cover.jpg", shared)
			_, first := runIncrementalTestScan(t, service, logs, root)
			if first.FilesParsed != 3 || first.CandidatesWritten != 3 {
				t.Fatalf("首扫应解析三轨并保存三个发行：%+v", first)
			}
			before := readIncrementalTestTracks(t, fixture, root)
			if len(before) != 3 {
				t.Fatalf("首扫应得到三轨：%+v", before)
			}
			// 构造旧规则的已存结果：普通专辑内嵌优先，歧义目录错误绑定共享文件夹图。
			store := artwork.NewStore(service.dataDirectory)
			for relative, track := range before {
				picture, source := shared, "folder"
				if relative == "Album/01.flac" {
					picture, source = embedded, "embedded"
				}
				if err := store.Save(context.Background(), fixture.database, track.ReleaseID, picture, source); err != nil {
					t.Fatalf("构造旧封面关系失败：%v", err)
				}
				assertArtworkPriorityBinding(t, fixture, service, root, relative, "", source, picture)
			}
			// 等价于升归组版本后所有候选签名不同，不依赖复制生产签名算法。
			if _, err := fixture.database.Exec(`UPDATE scan_candidate_states SET signature=$2 WHERE root_id=$1::uuid`, root.ID, strings.Repeat("0", 64)); err != nil {
				t.Fatal(err)
			}
			if oldParser {
				// 公共版本过期时，旧解析器没有读到的 FLAC 图也必须从原文件重新解析。
				oldVersion := strings.Replace(parser.CacheVersion(".flac"), fmt.Sprintf("c%d.", parser.CommonVersion), fmt.Sprintf("c%d.", parser.CommonVersion-1), 1)
				if _, err := fixture.database.Exec(`UPDATE library_parse_cache SET parser_version=$2,
					observation=observation-'ArtworkMIME' WHERE root_id=$1::uuid`, root.ID, oldVersion); err != nil {
					t.Fatal(err)
				}
			}
			statesBefore := readIncrementalTestCandidateStates(t, fixture, root)
			files := map[string][]byte{}
			infos := map[string]os.FileInfo{}
			for _, relative := range []string{"Album/01.flac", "Mixed/a.flac", "Mixed/b.flac", "Album/cover.jpg", "Mixed/cover.jpg"} {
				full := filepath.Join(root.Path, relative)
				data, err := os.ReadFile(full)
				if err != nil {
					t.Fatal(err)
				}
				info, err := os.Stat(full)
				if err != nil {
					t.Fatal(err)
				}
				files[relative], infos[relative] = data, info
			}
			_, second := runIncrementalTestScan(t, service, logs, root)
			wantParsed, wantHits, wantDeferred := int64(0), int64(3), int64(1)
			if oldParser {
				wantParsed, wantHits, wantDeferred = 3, 0, 0
			}
			if second.FilesParsed != wantParsed || second.ParseCacheHits != wantHits || second.CandidatesWritten != 3 || second.CandidatesSkipped != 0 || second.ArtworkDeferredLoads != wantDeferred {
				t.Fatalf("版本失效后未按预期重新选择封面：%+v", second)
			}
			assertArtworkPriorityBinding(t, fixture, service, root, "Album/01.flac", "", "folder", exact)
			assertArtworkPriorityBinding(t, fixture, service, root, "Mixed/a.flac", "same_dir_split", "embedded", embedded)
			assertArtworkPriorityBinding(t, fixture, service, root, "Mixed/b.flac", "same_dir_split", "", nil)
			if after := readIncrementalTestTracks(t, fixture, root); !reflect.DeepEqual(before, after) {
				t.Fatalf("只重选封面不应改变曲目身份或来源事实：前=%+v，后=%+v", before, after)
			}
			statesAfter := readIncrementalTestCandidateStates(t, fixture, root)
			if len(statesBefore) != 3 || len(statesAfter) != 3 {
				t.Fatalf("候选状态数量不符：前=%d，后=%d", len(statesBefore), len(statesAfter))
			}
			for key, old := range statesBefore {
				current := statesAfter[key]
				if current.ReleaseID != old.ReleaseID || current.Signature == old.Signature {
					t.Fatalf("候选未更新签名或发行身份改变：前=%+v，后=%+v", old, current)
				}
			}
			for relative, data := range files {
				full := filepath.Join(root.Path, relative)
				current, err := os.ReadFile(full)
				if err != nil || !bytes.Equal(current, data) {
					t.Fatalf("重扫改变源文件字节：%s：%v", relative, err)
				}
				info, err := os.Stat(full)
				if err != nil || info.Size() != infos[relative].Size() || !info.ModTime().Equal(infos[relative].ModTime()) {
					t.Fatalf("重扫改变源文件大小或 mtime：%s：%v", relative, err)
				}
			}
		})
	}
}
