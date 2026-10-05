package playback

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// playbackFixture 是隔离 schema 的已迁移测试库，加一个允许根目录和其中登记的一个音乐目录。
type playbackFixture struct {
	database    *sql.DB
	allowedRoot string
	musicRoot   string
	rootID      string
	service     *Service
}

func newPlaybackFixture(t *testing.T) *playbackFixture {
	t.Helper()
	scopedURL := postgrestest.NewSchema(t, "roomusic_test_")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	database, err := postgres.Open(ctx, scopedURL)
	if err != nil {
		t.Fatalf("open isolated playback database: %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close isolated playback database: %v", err)
		}
	})
	allowedRoot := realTempDir(t)
	musicRoot := filepath.Join(allowedRoot, "library")
	if err := os.Mkdir(musicRoot, 0o755); err != nil {
		t.Fatalf("create music root: %v", err)
	}
	fixture := &playbackFixture{database: database, allowedRoot: allowedRoot, musicRoot: musicRoot, rootID: uuid.New()}
	fixture.exec(t, `INSERT INTO library_roots(id,path,status,revision) VALUES($1::uuid,$2,'active',1)`, fixture.rootID, musicRoot)
	fixture.service = NewService(database, []string{allowedRoot})
	return fixture
}

func (fixture *playbackFixture) exec(t *testing.T, statement string, arguments ...any) {
	t.Helper()
	if _, err := fixture.database.Exec(statement, arguments...); err != nil {
		t.Fatalf("exec %q: %v", statement, err)
	}
}

// trackSpec 描述一条测试曲目；零值字段使用与真实扫描一致的默认值（present 的 FLAC）。
type trackSpec struct {
	relativePath string
	sourceKind   string
	codec        *string
	size         *int64
	modifiedAt   *time.Time
	fingerprint  string
	status       string
}

func textPointer(value string) *string { return &value }

func (fixture *playbackFixture) insertTrack(t *testing.T, spec trackSpec) string {
	t.Helper()
	if spec.sourceKind == "" {
		spec.sourceKind = "flac_vorbis_comment"
	}
	if spec.status == "" {
		spec.status = "present"
	}
	groupID, releaseID, mediumID, trackID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	fixture.exec(t, `INSERT INTO release_groups(id) VALUES($1::uuid)`, groupID)
	fixture.exec(t, `INSERT INTO releases(id,group_id,title,artist,source_root_id,relative_directory,candidate_anchor,candidate_kind)
		VALUES($1::uuid,$2::uuid,'专辑','艺术家',$3::uuid,$4,$5,'ordinary_directory')`, releaseID, groupID, fixture.rootID, "dir-"+trackID, fixture.rootID+":v2:"+trackID)
	fixture.exec(t, `INSERT INTO media(id,release_id,position,title) VALUES($1::uuid,$2::uuid,1,'Medium')`, mediumID, releaseID)
	var fingerprint any
	if spec.fingerprint != "" {
		fingerprint = spec.fingerprint
	}
	var modified any
	if spec.modifiedAt != nil {
		modified = spec.modifiedAt.UTC().Truncate(time.Microsecond)
	}
	fixture.exec(t, `INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,
			source_kind,codec,source_size,source_modified_at,content_fingerprint)
		VALUES($1::uuid,$2::uuid,1,'曲目','艺术家',1,$3::uuid,$4,$5,NOW(),$6,$7,$8,$9,$10)`,
		trackID, mediumID, fixture.rootID, spec.relativePath, spec.status, spec.sourceKind, spec.codec, spec.size, modified, fingerprint)
	return trackID
}

// insertPlayableFLAC 写入磁盘上的 FLAC 文件和与之完全一致的 present 曲目。
func (fixture *playbackFixture) insertPlayableFLAC(t *testing.T, relative string) string {
	t.Helper()
	content := flacContent(8192)
	writeSource(t, fixture.musicRoot, relative, content, scanTime)
	size := int64(len(content))
	modified := scanTime
	return fixture.insertTrack(t, trackSpec{relativePath: relative, codec: textPointer("flac"), size: &size, modifiedAt: &modified, fingerprint: "md5-of-audio"})
}

func requirePrepareError(t *testing.T, service *Service, trackID string, want error) {
	t.Helper()
	prepared, err := service.Prepare(context.Background(), trackID)
	if prepared != nil {
		prepared.File.Close()
	}
	if !errors.Is(err, want) {
		t.Fatalf("Prepare error = %v, want %v", err, want)
	}
}

func TestPostgreSQLPlaybackPrepareOpensAScannedFLAC(t *testing.T) {
	fixture := newPlaybackFixture(t)
	trackID := fixture.insertPlayableFLAC(t, "专辑/01 曲目.flac")

	prepared, err := fixture.service.Prepare(context.Background(), trackID)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer prepared.File.Close()
	if prepared.TrackID != trackID || prepared.RootID != fixture.rootID || prepared.Size != 8192 {
		t.Fatalf("Prepared = %+v", prepared)
	}
	if prepared.Format.MIMEType != "audio/flac" || prepared.Format.Codecs != "" || len(prepared.ETag) != 32 {
		t.Fatalf("Prepared format/etag = %+v, %q", prepared.Format, prepared.ETag)
	}
	again, err := fixture.service.Prepare(context.Background(), trackID)
	if err != nil {
		t.Fatalf("second Prepare: %v", err)
	}
	defer again.File.Close()
	if again.ETag != prepared.ETag {
		t.Fatalf("ETag changed between two reads of an unchanged file: %q, %q", prepared.ETag, again.ETag)
	}
	info, err := os.Stat(filepath.Join(fixture.musicRoot, "专辑", "01 曲目.flac"))
	if err != nil || !info.ModTime().Equal(scanTime) {
		t.Fatalf("source file was modified by playback: %v, %v", info, err)
	}
}

func TestPostgreSQLPlaybackPrepareChecksTrackThenRootThenFormat(t *testing.T) {
	fixture := newPlaybackFixture(t)

	requirePrepareError(t, fixture.service, uuid.New(), ErrTrackNotFound)

	missing := fixture.insertTrack(t, trackSpec{relativePath: "gone.flac", codec: textPointer("flac"), status: "missing"})
	requirePrepareError(t, fixture.service, missing, ErrTrackNotFound)

	cue := fixture.insertTrack(t, trackSpec{relativePath: "album.cue#track-1@image.flac#index-0", sourceKind: "cue_virtual", codec: textPointer("flac")})
	requirePrepareError(t, fixture.service, cue, ErrNotDirectlyPlayable)

	for _, spec := range []trackSpec{
		{relativePath: "float.wav", sourceKind: "wav_info", codec: textPointer("ieee_float")},
		{relativePath: "extensible.wav", sourceKind: "wav_info", codec: textPointer("wav_extensible")},
		{relativePath: "no-codec.flac", codec: nil},
		{relativePath: "mismatched.flac", sourceKind: "flac_vorbis_comment", codec: textPointer("alac")},
	} {
		requirePrepareError(t, fixture.service, fixture.insertTrack(t, spec), ErrNotDirectlyPlayable)
	}

	// 目录停用先于格式判断：停用目录里的任何曲目（包括 CUE 虚拟轨）都是 root_disabled。
	playable := fixture.insertPlayableFLAC(t, "01.flac")
	fixture.exec(t, `UPDATE library_roots SET status='disabled' WHERE id=$1::uuid`, fixture.rootID)
	requirePrepareError(t, fixture.service, playable, ErrRootDisabled)
	requirePrepareError(t, fixture.service, cue, ErrRootDisabled)
	requirePrepareError(t, fixture.service, missing, ErrTrackNotFound)

	fixture.exec(t, `UPDATE library_roots SET status='active' WHERE id=$1::uuid`, fixture.rootID)
	prepared, err := fixture.service.Prepare(context.Background(), playable)
	if err != nil {
		t.Fatalf("Prepare after the root was restored: %v", err)
	}
	prepared.File.Close()
}

func TestPostgreSQLPlaybackPrepareRejectsUnreadableAndChangedSources(t *testing.T) {
	fixture := newPlaybackFixture(t)
	content := flacContent(8192)
	size := int64(len(content))
	modified := scanTime
	later := scanTime.Add(time.Second)
	smaller := size - 1

	writeSource(t, fixture.musicRoot, "present.flac", content, scanTime)
	writeSource(t, fixture.musicRoot, "wav-disguised-as-flac.flac", append([]byte("RIFF\x00\x00\x00\x00WAVE"), make([]byte, 8180)...), scanTime)
	outside := realTempDir(t)
	writeSource(t, outside, "secret.flac", content, scanTime)
	if err := os.Symlink(filepath.Join(outside, "secret.flac"), filepath.Join(fixture.musicRoot, "escape.flac")); err != nil {
		t.Skipf("symlinks are not available: %v", err)
	}

	unavailable := map[string]trackSpec{
		"file is gone":         {relativePath: "deleted.flac", codec: textPointer("flac"), size: &size, modifiedAt: &modified},
		"symlink leaves root":  {relativePath: "escape.flac", codec: textPointer("flac"), size: &size, modifiedAt: &modified},
		"path climbs out":      {relativePath: "../library-sibling/secret.flac", codec: textPointer("flac"), size: &size, modifiedAt: &modified},
		"path is a directory":  {relativePath: ".", codec: textPointer("flac"), size: &size, modifiedAt: &modified},
		"path is the root dir": {relativePath: "", codec: textPointer("flac"), size: &size, modifiedAt: &modified},
	}
	for name, spec := range unavailable {
		t.Run(name, func(t *testing.T) {
			requirePrepareError(t, fixture.service, fixture.insertTrack(t, spec), ErrSourceUnavailable)
		})
	}
	changed := map[string]trackSpec{
		"size differs":              {relativePath: "present.flac", codec: textPointer("flac"), size: &smaller, modifiedAt: &modified},
		"modification time differs": {relativePath: "present.flac", codec: textPointer("flac"), size: &size, modifiedAt: &later},
		"scan recorded no size":     {relativePath: "present.flac", codec: textPointer("flac"), modifiedAt: &modified},
		"scan recorded no time":     {relativePath: "present.flac", codec: textPointer("flac"), size: &size},
		"scan recorded neither":     {relativePath: "present.flac", codec: textPointer("flac")},
		"container differs":         {relativePath: "wav-disguised-as-flac.flac", codec: textPointer("flac"), size: &size, modifiedAt: &modified},
	}
	for name, spec := range changed {
		t.Run(name, func(t *testing.T) {
			requirePrepareError(t, fixture.service, fixture.insertTrack(t, spec), ErrSourceChanged)
		})
	}
}

func TestPostgreSQLPlaybackPrepareRechecksTheRegisteredRootAgainstTheAllowList(t *testing.T) {
	fixture := newPlaybackFixture(t)
	trackID := fixture.insertPlayableFLAC(t, "01.flac")

	// 配置不再允许这个根目录（例如管理员改了 ROOMUSIC_ALLOWED_LIBRARY_ROOTS）：即使数据库里仍是 active。
	elsewhere := NewService(fixture.database, []string{realTempDir(t)})
	requirePrepareError(t, elsewhere, trackID, ErrSourceUnavailable)
	requirePrepareError(t, NewService(fixture.database, nil), trackID, ErrSourceUnavailable)

	// 登记的根目录被换成指向别处的符号链接：规范路径与登记路径不再一致。
	swapped := filepath.Join(fixture.allowedRoot, "swapped")
	if err := os.Rename(fixture.musicRoot, swapped); err != nil {
		t.Fatalf("move the music root: %v", err)
	}
	if err := os.Symlink(swapped, fixture.musicRoot); err != nil {
		t.Skipf("symlinks are not available: %v", err)
	}
	requirePrepareError(t, fixture.service, trackID, ErrSourceUnavailable)

	// 登记的根目录被删除。
	if err := os.Remove(fixture.musicRoot); err != nil {
		t.Fatalf("remove the symlink: %v", err)
	}
	requirePrepareError(t, fixture.service, trackID, ErrSourceUnavailable)
}

func TestPostgreSQLPlaybackReportsDatabaseFailuresAsErrDatabase(t *testing.T) {
	fixture := newPlaybackFixture(t)
	trackID := fixture.insertPlayableFLAC(t, "01.flac")
	closed, err := sql.Open("pgx", "postgres://127.0.0.1:1/none?sslmode=disable")
	if err != nil {
		t.Fatalf("open unreachable database handle: %v", err)
	}
	if err := closed.Close(); err != nil {
		t.Fatalf("close database handle: %v", err)
	}
	broken := NewService(closed, []string{fixture.allowedRoot})
	requirePrepareError(t, broken, trackID, ErrDatabase)
	if err := broken.StillPlayable(context.Background(), trackID, fixture.rootID); !errors.Is(err, ErrDatabase) {
		t.Fatalf("StillPlayable with a broken database = %v, want ErrDatabase", err)
	}
}

func TestPostgreSQLPlaybackStillPlayableFollowsTrackAndRootState(t *testing.T) {
	fixture := newPlaybackFixture(t)
	trackID := fixture.insertPlayableFLAC(t, "01.flac")
	ctx := context.Background()

	if err := fixture.service.StillPlayable(ctx, trackID, fixture.rootID); err != nil {
		t.Fatalf("StillPlayable for a healthy track: %v", err)
	}
	if err := fixture.service.StillPlayable(ctx, trackID, uuid.New()); !errors.Is(err, ErrTrackNotFound) {
		t.Fatalf("StillPlayable with a different root = %v, want ErrTrackNotFound", err)
	}
	if err := fixture.service.StillPlayable(ctx, uuid.New(), fixture.rootID); !errors.Is(err, ErrTrackNotFound) {
		t.Fatalf("StillPlayable for an unknown track = %v, want ErrTrackNotFound", err)
	}

	fixture.exec(t, `UPDATE library_roots SET status='disabled' WHERE id=$1::uuid`, fixture.rootID)
	if err := fixture.service.StillPlayable(ctx, trackID, fixture.rootID); !errors.Is(err, ErrRootDisabled) {
		t.Fatalf("StillPlayable in a disabled root = %v, want ErrRootDisabled", err)
	}
	fixture.exec(t, `UPDATE library_roots SET status='active' WHERE id=$1::uuid`, fixture.rootID)
	fixture.exec(t, `UPDATE tracks SET source_status='missing' WHERE id=$1::uuid`, trackID)
	if err := fixture.service.StillPlayable(ctx, trackID, fixture.rootID); !errors.Is(err, ErrTrackNotFound) {
		t.Fatalf("StillPlayable for a missing track = %v, want ErrTrackNotFound", err)
	}
}
