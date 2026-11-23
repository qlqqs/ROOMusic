package postgres

import (
	"database/sql"
	"testing"
)

const beforeReleaseMergeMigration = `
INSERT INTO users(id,username,password_hash) VALUES ('f6700000-0000-4000-8000-000000000001','合并迁移用户','test-only');
INSERT INTO release_groups(id) VALUES ('a6700000-0000-4000-8000-000000000001'),('b6700000-0000-4000-8000-000000000001');
INSERT INTO releases(id,group_id,title,artist) VALUES
 ('a6700000-0000-4000-8000-000000000002','a6700000-0000-4000-8000-000000000001','目标','测试艺人'),
 ('b6700000-0000-4000-8000-000000000002','b6700000-0000-4000-8000-000000000001','来源','测试艺人');
INSERT INTO media(id,release_id,position) VALUES
 ('a6700000-0000-4000-8000-000000000003','a6700000-0000-4000-8000-000000000002',1),
 ('b6700000-0000-4000-8000-000000000003','b6700000-0000-4000-8000-000000000002',1);
INSERT INTO personal_favorite_state(user_id,revision) VALUES ('f6700000-0000-4000-8000-000000000001',1);
INSERT INTO favorite_albums(user_id,group_id,created_at,saved_title,saved_artist)
 VALUES ('f6700000-0000-4000-8000-000000000001','b6700000-0000-4000-8000-000000000001','2026-01-02Z','收藏标题','测试艺人');`

const mergedReleaseRollbackFixture = `
INSERT INTO operations(id,actor_id,operation_type,target_type,target_id,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id)
 VALUES ('d6700000-0000-4000-8000-000000000001','f6700000-0000-4000-8000-000000000001','release_merge','release','a6700000-0000-4000-8000-000000000002',gen_random_uuid(),'merge',1,2,'{}','{}','{}','fixture');
INSERT INTO operations_release_targets SELECT 'd6700000-0000-4000-8000-000000000001',id FROM releases;
INSERT INTO release_merges(source_release_id,target_release_id,operation_id)
 VALUES ('b6700000-0000-4000-8000-000000000002','a6700000-0000-4000-8000-000000000002','d6700000-0000-4000-8000-000000000001');
UPDATE media SET origin_release_id=release_id,origin_position=position,release_id='a6700000-0000-4000-8000-000000000002',position=2
 WHERE id='b6700000-0000-4000-8000-000000000003';
UPDATE favorite_albums SET group_id='a6700000-0000-4000-8000-000000000001' WHERE group_id='b6700000-0000-4000-8000-000000000001';
UPDATE personal_favorite_state SET revision=2 WHERE user_id='f6700000-0000-4000-8000-000000000001';`

func assertMergedDiscRestored(t *testing.T, db *sql.DB) {
	t.Helper()
	var owner string
	var position int
	err := db.QueryRow(`SELECT release_id::text,position FROM media WHERE id='b6700000-0000-4000-8000-000000000003'`).Scan(&owner, &position)
	if err != nil || owner != "b6700000-0000-4000-8000-000000000002" || position != 1 {
		t.Fatalf("0067 回退后的碟归属=%s 碟号=%d err=%v", owner, position, err)
	}
}
