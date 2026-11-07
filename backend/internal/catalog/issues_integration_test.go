package catalog

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/releasesql"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// 迁移 0019 与“需要关注”的三类拆分。核对行在这里直接写入，依据用与读取相同的表达式算出；
// 写入核对的受控流程（接口、版本号与操作记录）在后续步骤里测试，这里只需要满足核对表的延迟外键。

// issueCounts 是一个发行三类问题的待处理与已核对数目。
type issueCounts struct {
	openFields, openGrouping, openMissing             int64
	reviewedFields, reviewedGrouping, reviewedMissing int64
}

func (counts issueCounts) open() int64 {
	return counts.openFields + counts.openGrouping + counts.openMissing
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

func issueConstraintOf(err error) string {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		return postgresError.ConstraintName
	}
	return ""
}

func TestPostgreSQLIssueReviewRowsRejectInvalidStates(t *testing.T) {
	fixture := newEffectiveFixture(t)
	release := fixture.visibleRelease("专辑")
	operationID := uuid.New()
	fixture.exec(`INSERT INTO operations(id,actor_id,operation_type,target_type,target_id,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id)
		VALUES($1::uuid,$2::uuid,'release_issue_review','release',$3::uuid,$4::uuid,'f',1,2,'{}','{}','{}','test')`, operationID, fixture.actorID, release.releaseID, uuid.New())
	for name, testCase := range map[string]struct{ statement, constraint string }{
		"a field that cannot be reviewed": {`INSERT INTO release_issue_reviews(release_id,issue,basis,operation_id) VALUES($1::uuid,'title','{}',$2::uuid)`, "release_issue_reviews_issue_ck"},
		"a basis that is not an object":   {`INSERT INTO release_issue_reviews(release_id,issue,basis,operation_id) VALUES($1::uuid,'grouping','[]',$2::uuid)`, "release_issue_reviews_basis_ck"},
	} {
		if _, err := fixture.database.Exec(testCase.statement, release.releaseID, operationID); issueConstraintOf(err) != testCase.constraint {
			t.Errorf("%s: error = %v, want %s", name, err, testCase.constraint)
		}
	}
	// 每个版本每类问题至多一行；没有对应操作记录的核对在提交时被拒绝。
	fixture.exec(`INSERT INTO release_issue_reviews(release_id,issue,basis,operation_id) VALUES($1::uuid,'grouping','{}',$2::uuid)`, release.releaseID, operationID)
	if _, err := fixture.database.Exec(`INSERT INTO release_issue_reviews(release_id,issue,basis,operation_id) VALUES($1::uuid,'grouping','{}',$2::uuid)`, release.releaseID, operationID); issueConstraintOf(err) != "release_issue_reviews_pkey" {
		t.Errorf("a second review of the same issue: error = %v, want release_issue_reviews_pkey", err)
	}
	if _, err := fixture.database.Exec(`INSERT INTO release_issue_reviews(release_id,issue,basis,operation_id) VALUES($1::uuid,'media_type','{}',$2::uuid)`, release.releaseID, uuid.New()); issueConstraintOf(err) != "release_issue_reviews_operation_id_fkey" {
		t.Errorf("a review referencing an operation that does not exist: error = %v, want release_issue_reviews_operation_id_fkey", err)
	}
}

func TestPostgreSQLIssueReviewsExcludeReviewedIssuesFromAttention(t *testing.T) {
	fixture := newEffectiveFixture(t)
	ctx := context.Background()
	store := fixture.store
	release := fixture.visibleRelease("有问题的版本")
	sibling := fixture.visibleRelease("同一张专辑的另一个版本")
	fixture.exec(`UPDATE releases SET group_id=$2::uuid WHERE id=$1::uuid`, sibling.releaseID, release.groupID)

	fixture.uncertainField(release.releaseID, "title", "标题", []string{"标题", "另一个标题"})
	fixture.uncertainField(release.releaseID, "source_type", "CD", []string{"CD", "SACD"})
	fixture.uncertainField(release.releaseID, "media_type", "CD", []string{"CD", "Vinyl"})
	fixture.groupingConflict(release.releaseID, `["dir/01.flac","dir/02.flac"]`)
	returned := fixture.track(release, "会回来的曲目", "missing", 10.0)
	again := fixture.track(release, "回来后又缺失的曲目", "missing", 10.0)
	fixture.uncertainField(sibling.releaseID, "label", "原厂牌", []string{"原厂牌", "另一厂牌"})

	check := func(label string, want issueCounts, wantGroup int64) {
		t.Helper()
		if got := fixture.issueCounts(release.releaseID); got != want {
			t.Fatalf("%s: issue counts = %+v, want %+v", label, got, want)
		}
		summary, err := store.LoadReleaseSummary(ctx, release.releaseID)
		if err != nil || summary.AttentionCount != want.open() {
			t.Fatalf("%s: attention_count = %d (%v), want the open issues %d", label, summary.AttentionCount, err, want.open())
		}
		group, err := store.LoadReleaseGroup(ctx, release.groupID)
		if err != nil || group.AttentionCount != wantGroup {
			t.Fatalf("%s: group attention = %d (%v), want %d", label, group.AttentionCount, err, wantGroup)
		}
	}
	// 没有核对时：3 个字段 + 1 个归组冲突 + 2 首缺失曲目，与只看扫描结论的算法相同；组再加上另一个版本的 1 个字段。
	check("before any review", issueCounts{openFields: 3, openGrouping: 1, openMissing: 2}, 7)

	// 核对只压下自己那一类：来源类型的核对不影响介质类型与标题。
	fixture.reviewIssue(release.releaseID, "source_type")
	check("after reviewing the source type", issueCounts{openFields: 2, reviewedFields: 1, openGrouping: 1, openMissing: 2}, 6)
	fixture.reviewIssue(release.releaseID, "media_type")
	fixture.reviewIssue(release.releaseID, "grouping")
	fixture.reviewIssue(release.releaseID, "missing_tracks")
	check("after reviewing everything reviewable", issueCounts{openFields: 1, reviewedFields: 2, reviewedGrouping: 1, reviewedMissing: 2}, 2)

	// 扫描原样重写依据不算变化；候选变了的介质类型、来源路径变了的归组冲突重新计入。
	fixture.uncertainField(release.releaseID, "source_type", "CD", []string{"CD", "SACD"})
	fixture.groupingConflict(release.releaseID, `["dir/01.flac","dir/02.flac"]`)
	check("after an unchanged rescan", issueCounts{openFields: 1, reviewedFields: 2, reviewedGrouping: 1, reviewedMissing: 2}, 2)
	fixture.uncertainField(release.releaseID, "media_type", "CD", []string{"CD", "Vinyl", "SACD"})
	fixture.groupingConflict(release.releaseID, `["dir/01.flac","dir/03.flac"]`)
	check("after the basis changed", issueCounts{openFields: 2, reviewedFields: 1, openGrouping: 1, reviewedMissing: 2}, 4)

	// 缺失曲目逐首判断：回来又缺失（最后一次看到的时间变了）的与核对之后新缺失的曲目重新计入，其余仍不计；回来了的不计。
	fixture.exec(`UPDATE tracks SET observed_at = observed_at + INTERVAL '1 microsecond' WHERE id=$1::uuid`, again)
	fixture.track(release, "核对之后新缺失的曲目", "missing", 10.0)
	check("after one track went missing again and another went missing", issueCounts{openFields: 2, reviewedFields: 1, openGrouping: 1, openMissing: 2, reviewedMissing: 1}, 6)
	fixture.exec(`UPDATE tracks SET source_status='present', observed_at=NOW() WHERE id=$1::uuid`, returned)
	check("after a reviewed track came back", issueCounts{openFields: 2, reviewedFields: 1, openGrouping: 1, openMissing: 2}, 6)

	// 再次核对覆盖过期的行；字段问题由修正处理。版本没有待处理的问题时，“只看需要关注”不再列出它，组仍因另一个版本被列出。
	fixture.reviewIssue(release.releaseID, "media_type")
	fixture.reviewIssue(release.releaseID, "grouping")
	fixture.reviewIssue(release.releaseID, "missing_tracks")
	fixture.correct(release.releaseID, "title", "确认过的标题")
	check("after handling everything", issueCounts{reviewedFields: 2, reviewedGrouping: 1, reviewedMissing: 2}, 1)
	listed, err := store.ListReleases(ctx, ListParams{AttentionRequired: true, Page: 1, PageSize: 10})
	if err != nil || listed.Total != 1 || listed.Items[0].ID != sibling.releaseID {
		t.Fatalf("attention filter = %+v, %v; want only the other version", listed, err)
	}
	groups, err := store.ListReleaseGroups(ctx, ListParams{AttentionRequired: true, Page: 1, PageSize: 10})
	if err != nil || groups.Total != 1 || groups.Items[0].ID != release.groupID || groups.Items[0].AttentionCount != 1 {
		t.Fatalf("group attention filter = %+v, %v; want the group with the other version's issue", groups, err)
	}
	fixture.correct(sibling.releaseID, "label", "原厂牌")
	if groups, err := store.ListReleaseGroups(ctx, ListParams{AttentionRequired: true, Page: 1, PageSize: 10}); err != nil || groups.Total != 0 {
		t.Fatalf("group attention filter after the last issue was handled = %+v, %v; want none", groups, err)
	}
}
