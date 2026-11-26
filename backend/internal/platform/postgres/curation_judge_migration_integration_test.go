package postgres

import (
	"testing"

	"github.com/qlqq/roomusic/backend/migrations"
)

func TestPostgreSQLJudgeMigrationPreservesHistory(t *testing.T) {
	const version = 106
	available, err := loadMigrations(migrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	db := openMigrationTestDatabase(t)
	before, after := migrationPrefix(available, version-1), migrationPrefix(available, version)
	if _, ok := after["0106_curation_model_judge.sql"]; !ok {
		t.Fatal("缺少识别判断迁移")
	}
	rollbackApply(t, db, before)
	rollbackExec(t, db, `
 INSERT INTO users(id,username,password_hash,role) VALUES ('10600000-0000-4000-8000-000000000001','fixture','test-only','admin');
 INSERT INTO release_groups(id) VALUES ('10600000-0000-4000-8000-000000000002');
 INSERT INTO releases(id,group_id,title,artist) VALUES ('10600000-0000-4000-8000-000000000003','10600000-0000-4000-8000-000000000002','迁移夹具','合成艺人');
 INSERT INTO steward_conversations(id,user_id,mode,kind,model)
 VALUES ('10600000-0000-4000-8000-000000000004','10600000-0000-4000-8000-000000000001','steward','curation','synthetic'),
        ('10600000-0000-4000-8000-000000000005','10600000-0000-4000-8000-000000000001','steward','chat','synthetic');
 INSERT INTO steward_plans(id,conversation_id,user_id,digest,expires_at,background)
 SELECT gen_random_uuid(),id,user_id,'old-plan',now()+interval '1 day', kind='curation' FROM steward_conversations;
 INSERT INTO curation_pending_items(id,target_type,target_id,kind,status,route,rule_version,input_digest,evidence,conversation_id,claimed_at,review_retries,resolution)
 SELECT ('10600000-0000-4000-8000-0000000000'||suffix)::uuid,'release','10600000-0000-4000-8000-000000000003',kind,status,route,1,suffix,evidence::jsonb,
 '10600000-0000-4000-8000-000000000004',CASE WHEN status='in_progress' THEN now() END,CASE WHEN status='in_progress' THEN 2 ELSE 0 END,resolution
 FROM (VALUES ('10','name_patrol','waiting_model',NULL,'{}',NULL),
  ('11','recording_match','waiting_route','web','{}',NULL),
  ('12','release_match','waiting_route','lookup','{}',NULL),
  ('13','verify_name','in_progress',NULL,'{}',NULL),
  ('14','verify_artist_merge','resolved',NULL,'{"before":"合成甲团","after":"合成乙团"}','suggestion_recorded'),
  ('15','verify_name','resolved',NULL,'{}','suggestion_recorded')) AS old(suffix,kind,status,route,evidence,resolution);
 INSERT INTO operations(id,actor_id,operation_type,target_type,target_id,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id)
 VALUES ('10600000-0000-4000-8000-000000000016','10600000-0000-4000-8000-000000000001','release_auto_correction','release',
 '10600000-0000-4000-8000-000000000003',gen_random_uuid(),'old-adoption',1,2,'{}','{}','{}','fixture');
 INSERT INTO release_auto_values(release_id,field,mode,text_value,operation_id)
 VALUES ('10600000-0000-4000-8000-000000000003','title','set','已采用标题','10600000-0000-4000-8000-000000000016');
 INSERT INTO curation_model_exceptions(pending_item_id,tool,input,preview,provider,reason_code,status,decided_by,decided_at,operation_ids)
 VALUES ('10600000-0000-4000-8000-000000000014','observation','{"verdict":"different","reason":"不是同一团"}','{}','synthetic','no_evidence','open',NULL,NULL,'{}'),
        ('10600000-0000-4000-8000-000000000015','correct_release','{}','{}','synthetic','review_uncertain','accepted','10600000-0000-4000-8000-000000000001',now(),ARRAY['10600000-0000-4000-8000-000000000016'::uuid]);`)
	require := func(query string) {
		t.Helper()
		var ok bool
		if err := db.QueryRowContext(t.Context(), query).Scan(&ok); err != nil || !ok {
			t.Fatalf("迁移后断言结果=%t，错误=%v，查询=%s", ok, err, query)
		}
	}
	checkHistory := func() {
		t.Helper()
		require(`SELECT count(*)=1 AND bool_and(status='accepted' AND cardinality(operation_ids)=1) FROM curation_model_exceptions`)
		require(`SELECT count(*)=1 FROM release_effective r JOIN release_auto_values v ON v.release_id=r.id AND v.field='title'
		JOIN operations o ON o.id=v.operation_id WHERE r.title='已采用标题' AND o.fingerprint='old-adoption'`)
		require(`SELECT count(*)=2 AND bool_and(p.status=CASE WHEN c.kind='chat' THEN 'pending' ELSE 'superseded' END)
		FROM steward_plans p JOIN steward_conversations c ON c.id=p.conversation_id`)
		require(`SELECT count(*)=2 FROM curation_pending_items WHERE resolution='suggestion_recorded'`)
		require(`SELECT count(*)=0 FROM curation_pending_items WHERE kind='name_patrol' OR route='web'`)
		require(`SELECT count(*)=1 AND bool_and(status='resolved' AND resolution='superseded') FROM curation_pending_items WHERE route='lookup'`)
		require(`SELECT count(*)=1 AND bool_and(status='waiting_model' AND claimed_at IS NULL AND conversation_id IS NULL AND review_retries=0)
		FROM curation_pending_items WHERE input_digest='13'`)
	}
	rollbackApply(t, db, after)
	checkHistory()
	require(`SELECT count(*)=1 AND bool_and(legacy AND reason='不是同一团' AND model='synthetic' AND name_low<>name_high) FROM curation_artist_negative_pairs`)
	rollbackApply(t, db, before)
	checkHistory()
	require(`SELECT to_regclass('curation_artist_negative_pairs') IS NULL AND to_regclass('curation_match_names') IS NULL`)
	rollbackApply(t, db, after)
	checkHistory()
}
