package governance

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/releasesql"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// placementFixture 是隔离 schema 的已迁移测试库加一个登记的目录；发行、曲目与封面都是合成行，不读任何文件。
type placementFixture struct {
	t          *testing.T
	database   *sql.DB
	store      *catalog.Store
	governance *Store
	rootID     string
	counter    int
}

func newPlacementFixture(t *testing.T) *placementFixture {
	t.Helper()
	scopedURL := postgrestest.NewSchema(t, "roomusic_test_")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	database, err := postgres.Open(ctx, scopedURL)
	if err != nil {
		t.Fatalf("open isolated catalog database: %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close isolated catalog database: %v", err)
		}
	})
	fixture := &placementFixture{t: t, database: database, store: catalog.NewStore(database), governance: NewStore(database), rootID: uuid.New()}
	fixture.exec(`INSERT INTO library_roots(id,path,status,revision) VALUES($1::uuid,'/library','active',1)`, fixture.rootID)
	return fixture
}

func (fixture *placementFixture) exec(statement string, arguments ...any) {
	fixture.t.Helper()
	if _, err := fixture.database.Exec(statement, arguments...); err != nil {
		fixture.t.Fatalf("exec %q: %v", statement, err)
	}
}

type placementRelease struct{ groupID, releaseID, mediumID string }

func (fixture *placementFixture) release(title string) placementRelease {
	fixture.t.Helper()
	fixture.counter++
	release := placementRelease{groupID: uuid.New(), releaseID: uuid.New(), mediumID: uuid.New()}
	fixture.exec(`INSERT INTO release_groups(id) VALUES($1::uuid)`, release.groupID)
	fixture.exec(`INSERT INTO releases(id,group_id,title,artist,album_artist,year,source_root_id,relative_directory,candidate_anchor,candidate_kind)
		VALUES($1::uuid,$2::uuid,$3,'艺术家','专辑艺术家',2026,$4::uuid,$5,$6,'ordinary_directory')`,
		release.releaseID, release.groupID, title, fixture.rootID, fmt.Sprintf("dir-%d", fixture.counter), fmt.Sprintf("%s:v2:%d", fixture.rootID, fixture.counter))
	fixture.exec(`INSERT INTO media(id,release_id,position,title) VALUES($1::uuid,$2::uuid,1,'Medium')`, release.mediumID, release.releaseID)
	return release
}

func (fixture *placementFixture) track(release placementRelease, title, status string, duration any) string {
	fixture.t.Helper()
	fixture.counter++
	id := uuid.New()
	fixture.exec(`INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,source_kind,duration_seconds)
		VALUES($1::uuid,$2::uuid,$3,$4,'艺术家',1,$5::uuid,$6,$7,NOW(),'physical',$8)`,
		id, release.mediumID, fixture.counter, title, fixture.rootID, fmt.Sprintf("track-%d.flac", fixture.counter), status, duration)
	return id
}

// effectiveFixture 在合成发行之上直接写修正行，验证 release_effective 视图与读取投影；
// 写入修正的受控流程（接口、版本号与记录）在后续步骤里测试，这里只需要满足修正表的延迟外键。
type effectiveFixture struct {
	*placementFixture
	actorID string
}

func newEffectiveFixture(t *testing.T) *effectiveFixture {
	t.Helper()
	fixture := &effectiveFixture{placementFixture: newPlacementFixture(t), actorID: uuid.New()}
	fixture.exec(`INSERT INTO users(id,username,password_hash) VALUES($1::uuid,'admin','x')`, fixture.actorID)
	return fixture
}

// visibleRelease 是带一首 present 曲目（因此可见）的发行。
func (fixture *effectiveFixture) visibleRelease(title string) placementRelease {
	fixture.t.Helper()
	release := fixture.release(title)
	fixture.track(release, "曲目", "present", 100.0) // 曲目标题与发行标题无关：搜索也会命中曲目标题
	return release
}

// correct 设下一条修正意图（替换该字段已有的意图）：value 是 nil 表示明确设为空，int 是年份，string 是文本值。
func (fixture *effectiveFixture) correct(releaseID, field string, value any) {
	fixture.t.Helper()
	transaction, err := fixture.database.Begin()
	if err != nil {
		fixture.t.Fatalf("begin correction: %v", err)
	}
	defer transaction.Rollback()
	// 一个字段只有一个当前意图：设下新的意图等于替换旧的。
	if _, err := transaction.Exec(`DELETE FROM release_corrections WHERE release_id=$1::uuid AND field=$2`, releaseID, field); err != nil {
		fixture.t.Fatalf("replace correction: %v", err)
	}
	operationID := uuid.New()
	if _, err := transaction.Exec(`INSERT INTO operations(id,actor_id,operation_type,target_type,target_id,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id)
		VALUES($1::uuid,$2::uuid,'release_correction','release',$3::uuid,$4::uuid,'f',1,2,'{}','{}','{}','test')`, operationID, fixture.actorID, releaseID, uuid.New()); err != nil {
		fixture.t.Fatalf("insert operation: %v", err)
	}
	statement, arguments := "", []any{releaseID, field, value, operationID}
	switch value.(type) {
	case nil:
		statement, arguments = `INSERT INTO release_corrections(release_id,field,mode,operation_id) VALUES($1::uuid,$2,'null',$3::uuid)`, []any{releaseID, field, operationID}
	case int:
		statement = `INSERT INTO release_corrections(release_id,field,mode,year_value,operation_id) VALUES($1::uuid,$2,'set',$3,$4::uuid)`
	default:
		statement = `INSERT INTO release_corrections(release_id,field,mode,text_value,operation_id) VALUES($1::uuid,$2,'set',$3,$4::uuid)`
	}
	if _, err := transaction.Exec(statement, arguments...); err != nil {
		fixture.t.Fatalf("insert correction %s: %v", field, err)
	}
	if err := transaction.Commit(); err != nil {
		fixture.t.Fatalf("commit correction: %v", err)
	}
}

func (fixture *effectiveFixture) clearCorrection(releaseID, field string) {
	fixture.t.Helper()
	fixture.exec(`DELETE FROM release_corrections WHERE release_id=$1::uuid AND field=$2`, releaseID, field)
}

// effectiveColumns 读取视图里所有可修正的列（NULL 读成 nil）。
func (fixture *effectiveFixture) effectiveColumns(releaseID string) map[string]any {
	fixture.t.Helper()
	var title, artist string
	var albumArtist, genre, catalogNumber, edition, label, barcode sql.NullString
	var year sql.NullInt64
	if err := fixture.database.QueryRow(`SELECT title,artist,album_artist,year,genre,catalog_number,edition,label,barcode FROM release_effective WHERE id=$1::uuid`, releaseID).
		Scan(&title, &artist, &albumArtist, &year, &genre, &catalogNumber, &edition, &label, &barcode); err != nil {
		fixture.t.Fatalf("read effective columns: %v", err)
	}
	nullable := func(value sql.NullString) any {
		if !value.Valid {
			return nil
		}
		return value.String
	}
	columns := map[string]any{"title": title, "artist": artist, "album_artist": nullable(albumArtist), "genre": nullable(genre),
		"catalog_number": nullable(catalogNumber), "edition": nullable(edition), "label": nullable(label), "barcode": nullable(barcode), "year": nil}
	if year.Valid {
		columns["year"] = int(year.Int64)
	}
	return columns
}

func (fixture *effectiveFixture) decision(releaseID, field, action string) {
	fixture.t.Helper()
	fixture.exec(`INSERT INTO release_field_decisions(release_id,field_key,selected_value,selected_source,confidence,action,rule_id)
		VALUES($1::uuid,$2,to_jsonb($2::text),'tag','low',$3,'test_v1')`, releaseID, field, action)
}

// issueCounts 是一个发行三类问题的待处理与已核对数目。
type issueCounts struct {
	openFields, openGrouping, openMissing             int64
	reviewedFields, reviewedGrouping, reviewedMissing int64
}

func (fixture *effectiveFixture) issueCounts(releaseID string) issueCounts {
	fixture.t.Helper()
	var counts issueCounts
	query := `SELECT ` + releasesql.ReleaseOpenFieldIssues + `, ` + releasesql.ReleaseOpenGroupingIssue + `, ` + releasesql.ReleaseOpenMissingTracks + `, ` +
		releasesql.ReleaseReviewedFieldIssues + `, ` + releasesql.ReleaseReviewedGroupingIssue + `, ` + releasesql.ReleaseReviewedMissingTracks +
		` FROM ` + releasesql.ReleaseSource + ` WHERE releases.id = $1::uuid`
	if err := fixture.database.QueryRow(query, releaseID).Scan(&counts.openFields, &counts.openGrouping, &counts.openMissing,
		&counts.reviewedFields, &counts.reviewedGrouping, &counts.reviewedMissing); err != nil {
		fixture.t.Fatalf("read issue counts: %v", err)
	}
	return counts
}

// uncertainField 写入一条扫描拿不准的字段决定（各曲目的值不一致），替换已有的那条，与扫描整行重写相同。
func (fixture *effectiveFixture) uncertainField(releaseID, field, value string, candidates []string) {
	fixture.t.Helper()
	fixture.exec(`DELETE FROM release_field_decisions WHERE release_id=$1::uuid AND field_key=$2`, releaseID, field)
	fixture.exec(`INSERT INTO release_field_decisions(release_id,field_key,selected_value,selected_source,confidence,action,rule_id,candidates,reason)
		VALUES($1::uuid,$2,to_jsonb($3::text),'tag','medium','uncertain_apply','majority_v1',to_jsonb($4::text[]),'inconsistent_candidates')`, releaseID, field, value, candidates)
}

// groupingConflict 写入同一目录归组冲突的依据（来源路径是 JSON 数组），替换已有的那条。
func (fixture *effectiveFixture) groupingConflict(releaseID, sourceRefs string) {
	fixture.t.Helper()
	fixture.exec(`INSERT INTO release_grouping_evidence(release_id,candidate_kind,rule_id,source_refs,reason) VALUES($1::uuid,'same_dir_split','organizer_v2',$2::jsonb,'same_directory_conflict')
		ON CONFLICT(release_id) DO UPDATE SET source_refs=EXCLUDED.source_refs,reason=EXCLUDED.reason,observed_at=NOW()`, releaseID, sourceRefs)
}

// reviewIssue 按问题现在的依据写入一条核对（替换已有的），连同它指向的操作记录。
func (fixture *effectiveFixture) reviewIssue(releaseID, issue string) {
	fixture.t.Helper()
	operationID := uuid.New()
	statement := `INSERT INTO release_issue_reviews(release_id,issue,basis,operation_id)
		SELECT decision.release_id, decision.field_key, ` + releasesql.FieldIssueBasis + `, $3::uuid
		FROM release_field_decisions decision WHERE decision.release_id = $1::uuid AND decision.field_key = $2`
	arguments := []any{releaseID, issue, operationID}
	switch issue {
	case "grouping":
		statement = `INSERT INTO release_issue_reviews(release_id,issue,basis,operation_id)
			SELECT grouping.release_id, 'grouping', ` + releasesql.GroupingIssueBasis + `, $2::uuid
			FROM release_grouping_evidence grouping WHERE grouping.release_id = $1::uuid`
		arguments = []any{releaseID, operationID}
	case "missing_tracks":
		statement = `INSERT INTO release_issue_reviews(release_id,issue,basis,operation_id)
			SELECT $1::uuid, 'missing_tracks', jsonb_build_object('tracks', jsonb_object_agg(missing_track.id::text, ` + releasesql.MissingTrackSeenAt + `)), $2::uuid
			FROM tracks missing_track JOIN media missing_medium ON missing_medium.id = missing_track.medium_id
			WHERE missing_medium.release_id = $1::uuid AND missing_track.source_status = 'missing'`
		arguments = []any{releaseID, operationID}
	}
	transaction, err := fixture.database.Begin()
	if err != nil {
		fixture.t.Fatalf("begin review: %v", err)
	}
	defer transaction.Rollback()
	if _, err := transaction.Exec(`DELETE FROM release_issue_reviews WHERE release_id=$1::uuid AND issue=$2`, releaseID, issue); err != nil {
		fixture.t.Fatalf("replace review: %v", err)
	}
	if _, err := transaction.Exec(`INSERT INTO operations(id,actor_id,operation_type,target_type,target_id,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id)
		VALUES($1::uuid,$2::uuid,'release_issue_review','release',$3::uuid,$4::uuid,'f',1,2,'{}','{}','{}','test')`, operationID, fixture.actorID, releaseID, uuid.New()); err != nil {
		fixture.t.Fatalf("insert operation: %v", err)
	}
	result, err := transaction.Exec(statement, arguments...)
	if err != nil {
		fixture.t.Fatalf("insert review %s: %v", issue, err)
	}
	if inserted, _ := result.RowsAffected(); inserted != 1 {
		fixture.t.Fatalf("review %s inserted %d rows; the issue does not exist", issue, inserted)
	}
	if err := transaction.Commit(); err != nil {
		fixture.t.Fatalf("commit review: %v", err)
	}
}
