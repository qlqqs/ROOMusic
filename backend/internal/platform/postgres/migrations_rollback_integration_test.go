package postgres

import (
	"fmt"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/migrations"
)

// 新迁移若改变旧约束或枚举，必须提供新版数据，证明回退后不留新表数据。
var rollbackFixtures = map[int64]string{
	68: artistMediaRollbackFixture,
	67: mergedReleaseRollbackFixture,
	65: `UPDATE tracks SET edit_revision=5 WHERE id='c3000000-0000-4000-8000-000000000065';
 INSERT INTO operations(id,actor_id,operation_type,target_type,target_id,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id)
 SELECT id::uuid,'f6400000-0000-4000-8000-000000000000',kind,'release','e6400000-0000-4000-8000-000000000002',gen_random_uuid(),kind,1,2,'{}','{}','{}','fixture'
 FROM (VALUES ('d6500000-0000-4000-8000-000000000001','track_correction'),('d6500000-0000-4000-8000-000000000002','track_auto_correction')) AS fixture(id,kind);
 INSERT INTO operations(id,actor_id,operation_type,target_type,target_id,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id,reverts_operation_id)
 SELECT undo::uuid,actor_id,'revert',target_type,target_id,gen_random_uuid(),'undo',2,3,'{}','{}','{}','fixture',id
 FROM operations JOIN (VALUES ('d6500000-0000-4000-8000-000000000001','d6500000-0000-4000-8000-000000000003'),
 ('d6500000-0000-4000-8000-000000000002','d6500000-0000-4000-8000-000000000004')) AS fixture(original,undo) ON id=original::uuid;
 INSERT INTO track_corrections(track_id,field,mode,text_value,operation_id)
 VALUES('c3000000-0000-4000-8000-000000000065','title','set','人工标题','d6500000-0000-4000-8000-000000000001');
 INSERT INTO track_auto_values(track_id,field,mode,text_value,operation_id)
 VALUES('c3000000-0000-4000-8000-000000000065','artist','set','自动艺人','d6500000-0000-4000-8000-000000000002');
 INSERT INTO curation_suppressions(target_type,target_id,subject,value_hash,value,operation_id,created_by)
 SELECT 'track','c3000000-0000-4000-8000-000000000065',subject,repeat('a',64),'{}',op::uuid,'f6400000-0000-4000-8000-000000000000'
 FROM (VALUES ('title','d6500000-0000-4000-8000-000000000003'),('artist','d6500000-0000-4000-8000-000000000004')) AS fixture(subject,op);
 INSERT INTO artist_credits(id,track_id,role,name_id,position,layer)
 SELECT gen_random_uuid(),'c3000000-0000-4000-8000-000000000065','track_artist','a2000000-0000-4000-8000-000000000001',1,layer
 FROM (VALUES ('correction'),('auto')) AS fixture(layer);
 UPDATE steward_plan_items SET operation_id='d6500000-0000-4000-8000-000000000002' WHERE id='f6400000-0000-4000-8000-000000000005';
 UPDATE steward_plan_items SET operation_id='d6500000-0000-4000-8000-000000000003' WHERE id='f6500000-0000-4000-8000-000000000005';
 INSERT INTO curation_model_exceptions(pending_item_id,plan_item_id,tool,input,preview,provider,review_id,review_model,review_decision,review_source)
 VALUES('e6400000-0000-4000-8000-000000000004','f6500000-0000-4000-8000-000000000005','correct_track','{}','{}','synthetic',gen_random_uuid(),'synthetic-review','rejected','model');`,
	64: `UPDATE steward_plan_items SET decision='escalated' WHERE id='f6400000-0000-4000-8000-000000000005';
 INSERT INTO steward_conversations(id,user_id,title,model,mode,kind)
 VALUES('f6400000-0000-4000-8000-000000000006','f6400000-0000-4000-8000-000000000000','自动整理','synthetic','steward','curation');
 INSERT INTO steward_messages(id,conversation_id,sequence,role,content)
 VALUES('f6400000-0000-4000-8000-000000000007','f6400000-0000-4000-8000-000000000006',1,'assistant','{}');
 INSERT INTO steward_plans(id,conversation_id,user_id,digest,expires_at,background)
 VALUES('f6400000-0000-4000-8000-000000000008','f6400000-0000-4000-8000-000000000006',
 'f6400000-0000-4000-8000-000000000000','curation',now()+interval '1 day',true);
 UPDATE curation_pending_items SET status='in_progress',claimed_at=now(),attempts=1,review_retries=1,next_attempt_at=now(),
 conversation_id='f6400000-0000-4000-8000-000000000006' WHERE id='e6400000-0000-4000-8000-000000000003';
 UPDATE curation_pending_items SET status='waiting_human',conversation_id='f6400000-0000-4000-8000-000000000006'
 WHERE id='e6400000-0000-4000-8000-000000000004';
 INSERT INTO curation_model_exceptions(pending_item_id,plan_item_id,tool,input,preview,provider,review_id,review_model,review_decision,review_source,conversation_id)
 VALUES('e6400000-0000-4000-8000-000000000004','f6400000-0000-4000-8000-000000000005','correct_release','{}','{}',
 'synthetic',gen_random_uuid(),'synthetic-review','needs_human_confirmation','model','f6400000-0000-4000-8000-000000000006');
 INSERT INTO curation_model_usage(day,input_tokens,output_tokens) VALUES('2026-11-23',23,7);
 UPDATE curation_settings SET model='synthetic-model',model_daily_token_limit=NULL,model_updated_at=now() WHERE id=1;`,
	62: `INSERT INTO operations(id,actor_id,operation_type,target_type,target_id,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id)
 SELECT id::uuid,'f6200000-0000-4000-8000-000000000000','external_link','release','e6200000-0000-4000-8000-000000000002',
 gen_random_uuid(),'external_link',revision,revision+1,'{}','{}','{}','fixture'
 FROM (VALUES ('d6200000-0000-4000-8000-000000000001',1),('d6200000-0000-4000-8000-000000000002',3)) AS fixture(id,revision);
 INSERT INTO operations(id,actor_id,operation_type,target_type,target_id,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id,reverts_operation_id)
 SELECT 'd6200000-0000-4000-8000-000000000003',actor_id,'revert',target_type,target_id,gen_random_uuid(),'undo',2,3,'{}','{}','{}','fixture',id
 FROM operations WHERE id='d6200000-0000-4000-8000-000000000001';
 INSERT INTO external_links(id,source,object_type,object_id,external_id,method,confidence,operation_id)
 VALUES('e6200000-0000-4000-8000-000000000004','musicbrainz','release','e6200000-0000-4000-8000-000000000002','fixture-release','barcode','high','d6200000-0000-4000-8000-000000000002');
 INSERT INTO curation_suppressions(target_type,target_id,subject,value_hash,value,operation_id,created_by)
 VALUES('release','e6200000-0000-4000-8000-000000000002','external_link',repeat('a',64),'{"source":"musicbrainz","external_id":"old-release"}',
 'd6200000-0000-4000-8000-000000000003','f6200000-0000-4000-8000-000000000000'),
 ('release','e6200000-0000-4000-8000-000000000002','group_join',repeat('b',64),'{"group_id":"e6200000-0000-4000-8000-000000000001"}',
 NULL,'f6200000-0000-4000-8000-000000000000');
 INSERT INTO curation_work_states(subject,target_id,rule_version,input_digest,status,attempts,generation)
 VALUES('release','e6200000-0000-4000-8000-000000000002',101,repeat('c',64),'uncertain',1,1);
 INSERT INTO curation_pending_items(target_type,target_id,kind,status,rule_version,input_digest,evidence)
 VALUES('release','e6200000-0000-4000-8000-000000000002','release_match','waiting_model',101,repeat('c',64),'{}');
 UPDATE curation_settings SET paused=true WHERE id=1;
 UPDATE steward_plan_items SET operation_id='d6200000-0000-4000-8000-000000000002' WHERE id='f6200000-0000-4000-8000-000000000006';
 UPDATE steward_plan_items SET operation_id='d6200000-0000-4000-8000-000000000003' WHERE id='f6200000-0000-4000-8000-000000000007';`,
	61: `INSERT INTO operations(id,actor_id,operation_type,target_type,target_id,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id)
 SELECT id::uuid,'f6100000-0000-4000-8000-000000000000',kind,'release','e6100000-0000-4000-8000-000000000002',gen_random_uuid(),kind,1,2,'{}','{}','{}','fixture'
 FROM (VALUES ('d6100000-0000-4000-8000-000000000001','release_external_artwork'),('d6100000-0000-4000-8000-000000000002','track_external_lyrics')) AS fixture(id,kind);
 INSERT INTO operations(id,actor_id,operation_type,target_type,target_id,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id,reverts_operation_id)
 SELECT 'd6100000-0000-4000-8000-000000000003',actor_id,'revert',target_type,target_id,gen_random_uuid(),'undo',2,3,'{}','{}','{}','fixture',id FROM operations WHERE id='d6100000-0000-4000-8000-000000000001';
 INSERT INTO release_external_artworks(release_id,content_hash,mime_type,width,height,storage_key,source,external_id,operation_id)
 VALUES('e6100000-0000-4000-8000-000000000002',repeat('a',64),'image/jpeg',1,1,'fixture.jpeg','synthetic','image/1','d6100000-0000-4000-8000-000000000001');
 INSERT INTO track_external_lyrics(track_id,lyrics,content_hash,source,external_id,operation_id)
 VALUES('e6100000-0000-4000-8000-000000000004','合成歌词',repeat('b',64),'synthetic','1','d6100000-0000-4000-8000-000000000002');
 INSERT INTO curation_suppressions(target_type,target_id,subject,value_hash,value,operation_id,created_by)
 SELECT target,id::uuid,subject,repeat('c',64),'{}','d6100000-0000-4000-8000-000000000003','f6100000-0000-4000-8000-000000000000'
 FROM (VALUES ('release','e6100000-0000-4000-8000-000000000002','artwork'),('release','e6100000-0000-4000-8000-000000000002','artwork_source'),
 ('track','e6100000-0000-4000-8000-000000000004','lyrics'),('track','e6100000-0000-4000-8000-000000000004','lyrics_source')) AS fixture(target,id,subject);
 UPDATE steward_plan_items SET operation_id='d6100000-0000-4000-8000-000000000003' WHERE id='f6100000-0000-4000-8000-000000000006';`,
	60: `INSERT INTO curation_source_settings(source,enabled,updated_by) VALUES
 ('musicbrainz',true,'f6000000-0000-4000-8000-000000000000');
 INSERT INTO external_links(id,source,object_type,object_id,external_id,method,confidence) VALUES
 ('f6000000-0000-4000-8000-000000000003','musicbrainz','release','f6000000-0000-4000-8000-000000000002',
 'f6000000-0000-4000-8000-000000000004','tag','exact');`,
	59: `INSERT INTO track_identifiers(track_id,kind,value,ordinal) VALUES
 ('f5900000-0000-4000-8000-000000000004','mb_recording','f5900000-0000-4000-8000-000000000005',0),
 ('f5900000-0000-4000-8000-000000000004','isrc','ZZTST2600001',0);`,
	58: `INSERT INTO curation_batches(id,tier,actor_id) VALUES
 ('f5800000-0000-4000-8000-000000000002','program','f5800000-0000-4000-8000-000000000000');
 UPDATE curation_settings SET enabled=true,enabled_by='f5800000-0000-4000-8000-000000000000',enabled_at=now() WHERE id=1;
 INSERT INTO operations(id,actor_id,operation_type,target_type,target_id,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id,origin,batch_id)
 VALUES('f5800000-0000-4000-8000-000000000003','f5800000-0000-4000-8000-000000000000','release_auto_correction','release','f5800000-0000-4000-8000-000000000001',gen_random_uuid(),'auto',1,2,'{}','{}','{}','fixture','{"source":"curation","provider":"synthetic"}','f5800000-0000-4000-8000-000000000002');
 INSERT INTO operations(id,actor_id,operation_type,target_type,target_id,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id,reverts_operation_id)
 SELECT 'f5800000-0000-4000-8000-000000000004',actor_id,'revert',target_type,target_id,gen_random_uuid(),'undo',2,3,'{}','{}','{}','fixture',id FROM operations WHERE operation_type='release_auto_correction';
 INSERT INTO release_auto_values(release_id,field,mode,text_value,operation_id)
 VALUES('f5800000-0000-4000-8000-000000000001','title','set','自动标题','f5800000-0000-4000-8000-000000000003');
 UPDATE releases SET auto_revision=3 WHERE id='f5800000-0000-4000-8000-000000000001';
 INSERT INTO curation_suppressions(target_type,target_id,subject,value_hash,value,operation_id,created_by)
 SELECT target_type,target_id,'title',repeat('a',64),'{"mode":"set","value":"自动标题"}',id,actor_id FROM operations WHERE id='f5800000-0000-4000-8000-000000000004';
 UPDATE artist_names SET origin='auto' WHERE id='f5800000-0000-4000-8000-000000000006';
 INSERT INTO artist_credits(id,release_id,role,name_id,position,layer)
 VALUES(gen_random_uuid(),'f5800000-0000-4000-8000-000000000001','album_artist','f5800000-0000-4000-8000-000000000006',1,'auto');`,
	56: `UPDATE steward_user_settings SET protocol='responses',
 overrides='{"protocol":"responses","native_search":true}',
 probe_result='{"protocol":"responses","native_search":"supported","responses_available":true,"failures":[{"protocol":"openai","code":"synthetic"},{"protocol":"responses","code":"synthetic"}]}',
 search_source='tavily',search_key_ciphertext='synthetic',search_key_last_four='test'
 WHERE user_id='f5600000-0000-4000-8000-000000000000';
 UPDATE steward_messages SET provider_blocks='{"protocol":"responses","blocks":[]}' WHERE id='f5600000-0000-4000-8000-000000000002';`,
	55: `UPDATE steward_plan_items SET decision='rejected_by_review' WHERE id='f5500000-0000-4000-8000-000000000006';
 UPDATE steward_user_settings SET review_model='synthetic-review-model';
 INSERT INTO steward_reviews(id,plan_id,plan_digest,evidence_digest,policy_version,model,status,items,usage,expires_at) VALUES('f5500000-0000-4000-8000-000000000007','f5500000-0000-4000-8000-000000000005','c7','evidence',1,'synthetic-review-model','completed','[{"item_id":"f5500000-0000-4000-8000-000000000006","decision":"rejected","risk":"low","reasons":["合成理由"],"source":"model"}]','{}',now()+interval '15 minutes');`,
	54: `UPDATE steward_tool_calls SET status='awaiting_confirmation' WHERE id='f5400000-0000-4000-8000-000000000003';
 UPDATE steward_conversations SET auto_approved_tools=ARRAY['correct_release'] WHERE id='f5400000-0000-4000-8000-000000000001';
 UPDATE operations SET origin='{"source":"chat","mode":"assistant"}' WHERE id='f5400000-0000-4000-8000-000000000004';
 INSERT INTO steward_plans(id,conversation_id,user_id,digest,expires_at) VALUES('f5400000-0000-4000-8000-000000000005','f5400000-0000-4000-8000-000000000001','f5400000-0000-4000-8000-000000000000','c6',now()+interval '1 day');
 INSERT INTO steward_plan_items(id,plan_id,tool_call_id,position,tool_name,input,preview,revertible,idempotency_key)
 SELECT 'f5400000-0000-4000-8000-000000000006','f5400000-0000-4000-8000-000000000005',id,0,'correct_release','{}','{}',true,idempotency_key FROM steward_tool_calls WHERE id='f5400000-0000-4000-8000-000000000003';`,
	50: `UPDATE tracks SET lyrics='外置歌词',lyrics_source='lrc_file' WHERE id='c3000000-0000-4000-8000-000000000050';`,
	42: `UPDATE scan_runs SET trigger_kind='scheduled' WHERE id='f2000000-0000-4000-8000-000000000001';
 UPDATE scan_schedule SET enabled=FALSE,hour=5 WHERE id=1;`,
	41: `INSERT INTO library_parse_cache(root_id,relative_path,directory,source_size,source_modified_at,parser_version,observation,seen_scan_id)
 VALUES('f1000000-0000-4000-8000-000000000001','album/01.flac','album',128,NOW(),'c1.f1','{}','f2000000-0000-4000-8000-000000000001');
 INSERT INTO scan_candidate_states(root_id,candidate_key,release_id,signature)
 VALUES('f1000000-0000-4000-8000-000000000001','rollback-candidate','f4000000-0000-4000-8000-000000000001',repeat('a',64));
 UPDATE scan_runs SET scan_mode='full' WHERE id='f2000000-0000-4000-8000-000000000001';`,
	40: "", // 仅新增可空搜索派生列与索引，往返不需要新版取值。
	39: `UPDATE tracks SET work_title='Cello Suite, BWV 1007',work_catalog='BWV 1007',work_index=1,
 movement_number=1,movement_title='Prelude',work_source='tag' WHERE id='c3000000-0000-4000-8000-000000000035';
 INSERT INTO catalog_search_values(id,track_id,field,position,original_value,normalized_value,normalization_version,projection_generation,short_grams) VALUES
 ('c5000000-0000-4000-8000-000000000035','c3000000-0000-4000-8000-000000000035','work_title',1,'Cello Suite, BWV 1007','cello suite, bwv 1007',1,1,ARRAY['ce']);`,
	38: `INSERT INTO track_credits(track_id,role,name,detail,source,position) VALUES
 ('c3000000-0000-4000-8000-000000000034','lyricist','测试署名','libretto','lyrics',1),
 ('c3000000-0000-4000-8000-000000000034','arranger','测试署名',NULL,'tag',2),
 ('c3000000-0000-4000-8000-000000000034','performer','测试演奏者','violin','tag',4),
 ('c3000000-0000-4000-8000-000000000034','performer','测试演奏者','cello','lyrics',5);
 INSERT INTO artist_credits(id,track_id,role,name_id,position,layer) VALUES
 ('c4000000-0000-4000-8000-000000000034','c3000000-0000-4000-8000-000000000034','lyricist','a2000000-0000-4000-8000-000000000001',1,'scan'),
 ('c4000000-0000-4000-8000-000000000035','c3000000-0000-4000-8000-000000000034','arranger','a2000000-0000-4000-8000-000000000001',2,'scan');
 INSERT INTO artist_effective_credits(credit_id,track_id,role,name_id,artist_id,position)
 SELECT id,track_id,role,name_id,'a0000000-0000-4000-8000-000000000001',position
 FROM artist_credits WHERE track_id='c3000000-0000-4000-8000-000000000034';
 INSERT INTO artist_tag_evidence(track_id,role,tag_key,occurrence_index,raw_value,source_kind) VALUES
 ('c3000000-0000-4000-8000-000000000034','lyricist','LYRICIST',0,'测试署名','vorbis'),
 ('c3000000-0000-4000-8000-000000000034','arranger','ARRANGER',0,'测试署名','vorbis');
 INSERT INTO catalog_search_values(id,track_id,field,role,position,original_value,normalized_value,normalization_version,projection_generation,short_grams) VALUES
 ('c5000000-0000-4000-8000-000000000034','c3000000-0000-4000-8000-000000000034','credit','lyricist',1,'测试署名','测试署名',1,1,ARRAY['测试']),
 ('c5000000-0000-4000-8000-000000000035','c3000000-0000-4000-8000-000000000034','credit','arranger',2,'测试署名','测试署名',1,1,ARRAY['测试']);`,
	37: "",
	36: "",
	35: `UPDATE library_roots SET path='/rollback-relocated',revision=2
 WHERE id='f1000000-0000-4000-8000-000000000001';
 INSERT INTO library_root_operations(id,root_id,actor_id,operation_type,status,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id)
 VALUES('f3000000-0000-4000-8000-000000000004','f1000000-0000-4000-8000-000000000001',
 'f0000000-0000-4000-8000-000000000001','relocate','succeeded','回滚改路径','relocate-fixture',1,2,
 '{"path":"/rollback-filter","revision":1}','{"path":"/rollback-relocated","revision":2}','{}','rollback-relocate');`,
	34: `INSERT INTO library_root_exclusions(root_id,relative_path)
 VALUES('f1000000-0000-4000-8000-000000000001','测试子目录');
 INSERT INTO scan_held_sources(scan_run_id,root_id,kind,relative_path)
 VALUES('f2000000-0000-4000-8000-000000000001','f1000000-0000-4000-8000-000000000001','file','损坏.flac');
 UPDATE scan_runs SET skipped_files=1 WHERE id='f2000000-0000-4000-8000-000000000001';
 INSERT INTO library_root_operations(id,root_id,actor_id,operation_type,status,idempotency_key,fingerprint,result_revision,after_state,response,request_id)
 VALUES('f3000000-0000-4000-8000-000000000001','f1000000-0000-4000-8000-000000000001',
 'f0000000-0000-4000-8000-000000000001','exclude','succeeded','回滚排除','exclude-fixture',1,
 '{"relative_path":"测试子目录"}','{}','rollback-exclude'),
 ('f3000000-0000-4000-8000-000000000002','f1000000-0000-4000-8000-000000000001',
 'f0000000-0000-4000-8000-000000000001','unexclude','succeeded','回滚恢复','unexclude-fixture',1,
 '{"relative_path":"已移除子目录"}','{}','rollback-unexclude');`,
	33: "",
	// 0032 的非空列表转换由专门的数据迁移用例覆盖；结构往返不另加会截断的数据。
	32: "",
	31: `INSERT INTO artist_alias_signals(left_key_id,right_key_id,bracket_key_id,alias_part,release_id,alias_release_id,left_name,right_name)
 VALUES('a1000000-0000-4000-8000-000000000001','a1000000-0000-4000-8000-000000000002',
 'a1000000-0000-4000-8000-000000000001','inner','c0000000-0000-4000-8000-000000000001',
 'c0000000-0000-4000-8000-000000000001','测试（别名）','别名');`,
}

func migrationPrefix(available []migration, last int64) fstest.MapFS {
	source := fstest.MapFS{}
	for _, m := range available {
		if m.Version > last {
			continue
		}
		source[m.Name] = &fstest.MapFile{Data: m.SQL}
		if m.Down != nil {
			source[m.Down.Name] = &fstest.MapFile{Data: m.Down.SQL}
		}
	}
	return source
}
func rollbackNeedsFixture(before, after postgrestest.SchemaSnapshot) bool {
	for key, value := range before.Structure {
		next, ok := after.Structure[key]
		if (strings.HasPrefix(key, "constraint/") || strings.HasPrefix(key, "enum/")) && ok && value != next {
			return true
		}
	}
	return false
}
func TestPostgreSQLMigrationRollbackRoundTrip(t *testing.T) {
	available, err := loadMigrations(migrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	var reversible []migration
	for _, m := range available {
		if m.Version >= rollbackRequiredFromVersion && m.Down != nil {
			if m.Down.Irreversible {
				if m.Down.Reason == "" {
					t.Fatalf("不可回退迁移缺理由：%d", m.Version)
				}
			} else {
				reversible = append(reversible, m)
			}
		}
	}
	// 当前无可回退的真实迁移时注入示例；以后自动遍历发布集合，不允许空集伪通过。
	if len(reversible) == 0 {
		t.Log("嵌入集合暂无可回退迁移，注入测试 FS 验证往返框架")
		source := migrationPrefix(available, int64(len(available)))
		v := int64(len(available)) + 1
		source[fmt.Sprintf("%04d_roundtrip.sql", v)] = &fstest.MapFile{Data: []byte(`CREATE TABLE roundtrip_probe(id bigint PRIMARY KEY,note text); INSERT INTO roundtrip_probe VALUES(1,'合成种子'); ALTER TABLE users ADD COLUMN roundtrip_note text; CREATE INDEX roundtrip_index ON roundtrip_probe(note);`)}
		source[fmt.Sprintf("%04d_roundtrip.down.sql", v)] = &fstest.MapFile{Data: []byte(`DROP INDEX roundtrip_index; ALTER TABLE users DROP COLUMN roundtrip_note; DROP TABLE roundtrip_probe;`)}
		available, err = loadMigrations(source)
		if err != nil {
			t.Fatal(err)
		}
		reversible = append(reversible, available[len(available)-1])
	}
	for _, m := range reversible {
		t.Run(m.Name, func(t *testing.T) {
			db := openMigrationTestDatabase(t)
			// 每个候选独立建 schema，之前即使有不可回退迁移也不会妨碍后续测试。
			rollbackApply(t, db, migrationPrefix(available, m.Version-1))
			if m.Version == 31 {
				// 新信号引用升级前已有的发行，保证回退只需删除新版信号表。
				rollbackExec(t, db, `INSERT INTO release_groups(id) VALUES('c1000000-0000-4000-8000-000000000001');
 INSERT INTO releases(id,group_id,title,artist) VALUES('c0000000-0000-4000-8000-000000000001',
 'c1000000-0000-4000-8000-000000000001','迁移测试发行','测试艺人');`)
			}
			if m.Version == 38 {
				// 先存旧版依赖与无细分署名，回退须保留它们并删除新版重复细分。
				rollbackExec(t, db, `INSERT INTO library_roots(id,path) VALUES('c6000000-0000-4000-8000-000000000034','/synthetic/credit-rollback');
 INSERT INTO release_groups(id) VALUES('c1000000-0000-4000-8000-000000000034');
 INSERT INTO releases(id,group_id,title,artist) VALUES('c0000000-0000-4000-8000-000000000034',
 'c1000000-0000-4000-8000-000000000034','署名迁移测试发行','测试艺人');
 INSERT INTO media(id,release_id,position) VALUES('c2000000-0000-4000-8000-000000000034','c0000000-0000-4000-8000-000000000034',1);
 INSERT INTO tracks(id,medium_id,position,title,artist,source_root_id,relative_path,observed_at) VALUES
 ('c3000000-0000-4000-8000-000000000034','c2000000-0000-4000-8000-000000000034',1,'署名迁移测试曲目','测试艺人',
 'c6000000-0000-4000-8000-000000000034','synthetic-credit.flac',NOW());
 INSERT INTO track_credits(id,track_id,role,name,position) VALUES
 ('c7000000-0000-4000-8000-000000000034','c3000000-0000-4000-8000-000000000034','performer','测试演奏者',3);`)
			}
			if m.Version == 39 {
				// 作品附着于旧曲目；回退应只删作品字段与搜索值，不删除曲目本身。
				rollbackExec(t, db, `INSERT INTO library_roots(id,path) VALUES('c6000000-0000-4000-8000-000000000035','/synthetic/work-rollback');
 INSERT INTO release_groups(id) VALUES('c1000000-0000-4000-8000-000000000035');
 INSERT INTO releases(id,group_id,title,artist) VALUES('c0000000-0000-4000-8000-000000000035',
 'c1000000-0000-4000-8000-000000000035','作品迁移测试发行','测试艺人');
 INSERT INTO media(id,release_id,position) VALUES('c2000000-0000-4000-8000-000000000035','c0000000-0000-4000-8000-000000000035',1);
 INSERT INTO tracks(id,medium_id,position,title,artist,source_root_id,relative_path,observed_at) VALUES
 ('c3000000-0000-4000-8000-000000000035','c2000000-0000-4000-8000-000000000035',1,'作品迁移测试曲目','测试艺人',
 'c6000000-0000-4000-8000-000000000035','synthetic-work.flac',NOW());`)
			}
			if m.Version == 34 || m.Version == 35 || m.Version == 41 || m.Version == 42 {
				// 升级前保留旧类型收据；回退只能移除新类型，不能误删原目录、扫描或操作记录。
				rollbackExec(t, db, `INSERT INTO users(id,username,password_hash,role)
 VALUES('f0000000-0000-4000-8000-000000000001','rollback-filter-admin','test-only','admin');
 INSERT INTO library_roots(id,path) VALUES('f1000000-0000-4000-8000-000000000001','/rollback-filter');
 INSERT INTO scan_runs(id,status,started_at,finished_at)
 VALUES('f2000000-0000-4000-8000-000000000001','succeeded',NOW(),NOW());
 INSERT INTO library_root_operations(id,root_id,actor_id,operation_type,status,idempotency_key,fingerprint,result_revision,request_id)
 VALUES('f3000000-0000-4000-8000-000000000003','f1000000-0000-4000-8000-000000000001',
 'f0000000-0000-4000-8000-000000000001','create','succeeded','旧目录收据','create-fixture',1,'rollback-create');`)
			}
			if m.Version == 50 {
				// 外置行升级前为空，内嵌行始终保留，快照会核对整行与其余依赖。
				rollbackExec(t, db, `INSERT INTO library_roots(id,path) VALUES('c6000000-0000-4000-8000-000000000050','/synthetic/lyrics-rollback');
 INSERT INTO release_groups(id) VALUES('c1000000-0000-4000-8000-000000000050');
 INSERT INTO releases(id,group_id,title,artist) VALUES('c0000000-0000-4000-8000-000000000050','c1000000-0000-4000-8000-000000000050','歌词迁移测试','测试艺人');
 INSERT INTO media(id,release_id,position) VALUES('c2000000-0000-4000-8000-000000000050','c0000000-0000-4000-8000-000000000050',1);
 INSERT INTO tracks(id,medium_id,position,title,artist,source_root_id,relative_path,observed_at,lyrics,lyrics_source) VALUES
 ('c3000000-0000-4000-8000-000000000050','c2000000-0000-4000-8000-000000000050',1,'外置曲目','测试艺人','c6000000-0000-4000-8000-000000000050','external.flac',NOW(),NULL,NULL),
 ('c3000000-0000-4000-8000-000000000051','c2000000-0000-4000-8000-000000000050',2,'内嵌曲目','测试艺人','c6000000-0000-4000-8000-000000000050','embedded.flac',NOW(),'保留内嵌歌词','vorbis_lyrics');`)
			}
			if m.Version == 54 {
				// C6 等待确认回退为未执行，旧会话与操作记录必须留下。
				rollbackExec(t, db, `INSERT INTO users(id,username,password_hash,role) VALUES('f5400000-0000-4000-8000-000000000000','c6-rollback','synthetic','admin');
 INSERT INTO steward_conversations(id,user_id,title,model) VALUES('f5400000-0000-4000-8000-000000000001','f5400000-0000-4000-8000-000000000000','C6 旧会话','synthetic');
 INSERT INTO steward_messages(id,conversation_id,sequence,role,content) VALUES('f5400000-0000-4000-8000-000000000002','f5400000-0000-4000-8000-000000000001',1,'assistant','{}');
 INSERT INTO steward_tool_calls(id,message_id,position,tool_name,input,status,error_code) VALUES('f5400000-0000-4000-8000-000000000003','f5400000-0000-4000-8000-000000000002',0,'correct_release','{}','failed','not_executed');
 INSERT INTO operations(id,actor_id,operation_type,target_type,target_id,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id) VALUES('f5400000-0000-4000-8000-000000000004','f5400000-0000-4000-8000-000000000000','release_correction','release','f5400000-0000-4000-8000-000000000007','f5400000-0000-4000-8000-000000000008','c6',1,2,'{}','{}','{}','c6-rollback');`)
			}

			if m.Version == 55 {
				// C7 回退把审查拒绝转为 C6 的 declined，旧会话、方案与设置保留。
				rollbackExec(t, db, `INSERT INTO users(id,username,password_hash,role) VALUES('f5500000-0000-4000-8000-000000000000','c7-rollback','synthetic','admin');
 INSERT INTO steward_user_settings(user_id,model) VALUES('f5500000-0000-4000-8000-000000000000','synthetic-model');
 INSERT INTO steward_conversations(id,user_id,title,model,mode) VALUES('f5500000-0000-4000-8000-000000000001','f5500000-0000-4000-8000-000000000000','C7 旧会话','synthetic','steward');
 INSERT INTO steward_messages(id,conversation_id,sequence,role,content) VALUES('f5500000-0000-4000-8000-000000000002','f5500000-0000-4000-8000-000000000001',1,'assistant','{}');
 INSERT INTO steward_tool_calls(id,message_id,position,tool_name,input,status,error_code) VALUES('f5500000-0000-4000-8000-000000000003','f5500000-0000-4000-8000-000000000002',0,'correct_release','{}','failed','rejected_by_review');
 INSERT INTO steward_plans(id,conversation_id,user_id,digest,expires_at) VALUES('f5500000-0000-4000-8000-000000000005','f5500000-0000-4000-8000-000000000001','f5500000-0000-4000-8000-000000000000','c7',now()+interval '1 day');
 INSERT INTO steward_plan_items(id,plan_id,tool_call_id,position,tool_name,input,preview,revertible,idempotency_key,decision) SELECT 'f5500000-0000-4000-8000-000000000006','f5500000-0000-4000-8000-000000000005',id,0,'correct_release','{}','{}',true,idempotency_key,'declined' FROM steward_tool_calls WHERE id='f5500000-0000-4000-8000-000000000003';`)
			}
			if m.Version == 56 {
				// C8 回退恢复旧协议与探测值，保留用户设置和消息，删除新增搜索与供应商块字段。
				rollbackExec(t, db, `INSERT INTO users(id,username,password_hash,role) VALUES('f5600000-0000-4000-8000-000000000000','c8-rollback','synthetic','admin');
 INSERT INTO steward_user_settings(user_id,protocol,model,overrides,probe_result,probed_at)
 VALUES('f5600000-0000-4000-8000-000000000000','openai','synthetic-model','{"protocol":"openai"}',
 '{"protocol":"openai","failures":[{"protocol":"openai","code":"synthetic"}]}',now());
 INSERT INTO steward_conversations(id,user_id,title,model) VALUES('f5600000-0000-4000-8000-000000000001','f5600000-0000-4000-8000-000000000000','C8 旧会话','synthetic-model');
 INSERT INTO steward_messages(id,conversation_id,sequence,role,content) VALUES('f5600000-0000-4000-8000-000000000002','f5600000-0000-4000-8000-000000000001',1,'assistant','{}');`)
			}
			if m.Version == 58 {
				rollbackExec(t, db, `INSERT INTO users(id,username,password_hash) VALUES('f5800000-0000-4000-8000-000000000000','rollback-auto','synthetic');
 INSERT INTO release_groups(id) VALUES('f5800000-0000-4000-8000-000000000005');
 INSERT INTO releases(id,group_id,title,artist) VALUES('f5800000-0000-4000-8000-000000000001','f5800000-0000-4000-8000-000000000005','扫描标题','扫描艺人');
 INSERT INTO artist_names(id,key_id,spelling,spelling_hash,origin)
 VALUES('f5800000-0000-4000-8000-000000000006','a1000000-0000-4000-8000-000000000001','合成艺人',decode(repeat('ab',32),'hex'),'scan');`)
			}
			if m.Version == 59 {
				rollbackExec(t, db, `INSERT INTO library_roots(id,path) VALUES('f5900000-0000-4000-8000-000000000000','/synthetic/identifier-rollback');
 INSERT INTO release_groups(id) VALUES('f5900000-0000-4000-8000-000000000001');
 INSERT INTO releases(id,group_id,title,artist) VALUES
 ('f5900000-0000-4000-8000-000000000002','f5900000-0000-4000-8000-000000000001','标识迁移测试发行','测试艺人');
 INSERT INTO media(id,release_id,position) VALUES('f5900000-0000-4000-8000-000000000003','f5900000-0000-4000-8000-000000000002',1);
 INSERT INTO tracks(id,medium_id,position,title,artist,source_root_id,relative_path,observed_at) VALUES
 ('f5900000-0000-4000-8000-000000000004','f5900000-0000-4000-8000-000000000003',1,'标识迁移测试曲目','测试艺人',
 'f5900000-0000-4000-8000-000000000000','synthetic-identifier.flac',NOW());`)
			}
			if m.Version == 60 {
				rollbackExec(t, db, `INSERT INTO users(id,username,password_hash) VALUES('f6000000-0000-4000-8000-000000000000','rollback-sources','synthetic');
 INSERT INTO release_groups(id) VALUES('f6000000-0000-4000-8000-000000000001');
 INSERT INTO releases(id,group_id,title,artist) VALUES
 ('f6000000-0000-4000-8000-000000000002','f6000000-0000-4000-8000-000000000001','外部对应迁移测试发行','测试艺人');`)
			}
			if m.Version == 61 {
				rollbackExec(t, db, `INSERT INTO users(id,username,password_hash,role) VALUES('f6100000-0000-4000-8000-000000000000','external-rollback','synthetic','admin');
 INSERT INTO steward_conversations(id,user_id,title,model,mode) VALUES('f6100000-0000-4000-8000-000000000001','f6100000-0000-4000-8000-000000000000','C7 旧会话','synthetic','steward');
 INSERT INTO steward_messages(id,conversation_id,sequence,role,content) VALUES('f6100000-0000-4000-8000-000000000002','f6100000-0000-4000-8000-000000000001',1,'assistant','{}');
 INSERT INTO steward_tool_calls(id,message_id,position,tool_name,input,status,error_code) VALUES('f6100000-0000-4000-8000-000000000003','f6100000-0000-4000-8000-000000000002',0,'revert_operation','{}','failed','rejected_by_review');
 INSERT INTO steward_plans(id,conversation_id,user_id,digest,expires_at) VALUES('f6100000-0000-4000-8000-000000000005','f6100000-0000-4000-8000-000000000001','f6100000-0000-4000-8000-000000000000','c7',now()+interval '1 day');
 INSERT INTO steward_plan_items(id,plan_id,tool_call_id,position,tool_name,input,preview,revertible,idempotency_key,decision) SELECT 'f6100000-0000-4000-8000-000000000006','f6100000-0000-4000-8000-000000000005',id,0,'revert_operation','{}','{}',true,idempotency_key,'declined' FROM steward_tool_calls WHERE id='f6100000-0000-4000-8000-000000000003';
 INSERT INTO library_roots(id,path) VALUES('e6100000-0000-4000-8000-000000000000','/synthetic/identifier-rollback');
 INSERT INTO release_groups(id) VALUES('e6100000-0000-4000-8000-000000000001');
 INSERT INTO releases(id,group_id,title,artist) VALUES
 ('e6100000-0000-4000-8000-000000000002','e6100000-0000-4000-8000-000000000001','标识迁移测试发行','测试艺人');
 INSERT INTO media(id,release_id,position) VALUES('e6100000-0000-4000-8000-000000000003','e6100000-0000-4000-8000-000000000002',1);
 INSERT INTO tracks(id,medium_id,position,title,artist,source_root_id,relative_path,observed_at) VALUES
 ('e6100000-0000-4000-8000-000000000004','e6100000-0000-4000-8000-000000000003',1,'标识迁移测试曲目','测试艺人',
 'e6100000-0000-4000-8000-000000000000','synthetic-identifier.flac',NOW());`)
			}
			if m.Version == 62 {
				rollbackExec(t, db, `INSERT INTO users(id,username,password_hash,role)
 VALUES('f6200000-0000-4000-8000-000000000000','program-rollback','synthetic','admin');
 INSERT INTO steward_conversations(id,user_id,title,model,mode)
 VALUES('f6200000-0000-4000-8000-000000000001','f6200000-0000-4000-8000-000000000000','旧会话','synthetic','steward');
 INSERT INTO steward_messages(id,conversation_id,sequence,role,content)
 VALUES('f6200000-0000-4000-8000-000000000002','f6200000-0000-4000-8000-000000000001',1,'assistant','{}');
 INSERT INTO steward_tool_calls(id,message_id,position,tool_name,input,status,error_code)
 VALUES('f6200000-0000-4000-8000-000000000003','f6200000-0000-4000-8000-000000000002',0,'revert_operation','{}','failed','rejected_by_review'),
 ('f6200000-0000-4000-8000-000000000004','f6200000-0000-4000-8000-000000000002',1,'revert_operation','{}','failed','rejected_by_review');
 INSERT INTO steward_plans(id,conversation_id,user_id,digest,expires_at)
 VALUES('f6200000-0000-4000-8000-000000000005','f6200000-0000-4000-8000-000000000001','f6200000-0000-4000-8000-000000000000','c-program',now()+interval '1 day');
 INSERT INTO steward_plan_items(id,plan_id,tool_call_id,position,tool_name,input,preview,revertible,idempotency_key,decision)
 SELECT item::uuid,'f6200000-0000-4000-8000-000000000005',id,position,tool_name,'{}','{}',true,idempotency_key,'declined'
 FROM steward_tool_calls JOIN (VALUES (0,'f6200000-0000-4000-8000-000000000006'),(1,'f6200000-0000-4000-8000-000000000007')) AS fixture(pos,item)
 ON position=pos WHERE message_id='f6200000-0000-4000-8000-000000000002';
 INSERT INTO release_groups(id) VALUES('e6200000-0000-4000-8000-000000000001');
 INSERT INTO releases(id,group_id,title,artist)
 VALUES('e6200000-0000-4000-8000-000000000002','e6200000-0000-4000-8000-000000000001','程序迁移测试发行','测试艺人');
 INSERT INTO external_links(id,source,object_type,object_id,external_id,method,confidence)
 VALUES('e6200000-0000-4000-8000-000000000003','musicbrainz','release_group','e6200000-0000-4000-8000-000000000001','existing-group','barcode','high');`)
			}
			if m.Version == 64 || m.Version == 65 {
				rollbackExec(t, db, `INSERT INTO users(id,username,password_hash,role)
 VALUES('f6400000-0000-4000-8000-000000000000','curation-rollback','synthetic','admin');
 INSERT INTO steward_conversations(id,user_id,title,model,mode)
 VALUES('f6400000-0000-4000-8000-000000000001','f6400000-0000-4000-8000-000000000000','保留的对话','synthetic','steward');
 INSERT INTO steward_messages(id,conversation_id,sequence,role,content)
 VALUES('f6400000-0000-4000-8000-000000000002','f6400000-0000-4000-8000-000000000001',1,'assistant','{}');
 INSERT INTO steward_tool_calls(id,message_id,position,tool_name,input,status)
 VALUES('f6400000-0000-4000-8000-000000000003','f6400000-0000-4000-8000-000000000002',0,'correct_release','{}','failed');
 INSERT INTO steward_plans(id,conversation_id,user_id,digest,expires_at)
 VALUES('f6400000-0000-4000-8000-000000000004','f6400000-0000-4000-8000-000000000001',
 'f6400000-0000-4000-8000-000000000000','chat',now()+interval '1 day');
 INSERT INTO steward_plan_items(id,plan_id,tool_call_id,position,tool_name,input,preview,revertible,idempotency_key,decision)
 SELECT 'f6400000-0000-4000-8000-000000000005','f6400000-0000-4000-8000-000000000004',id,position,tool_name,'{}','{}',true,idempotency_key,'declined'
 FROM steward_tool_calls WHERE id='f6400000-0000-4000-8000-000000000003';
 INSERT INTO release_groups(id) VALUES('e6400000-0000-4000-8000-000000000001');
 INSERT INTO releases(id,group_id,title,artist)
 VALUES('e6400000-0000-4000-8000-000000000002','e6400000-0000-4000-8000-000000000001','整理迁移测试发行','测试艺人');
 INSERT INTO curation_pending_items(id,target_type,target_id,kind,status,rule_version,input_digest,evidence)
 VALUES('e6400000-0000-4000-8000-000000000003','release','e6400000-0000-4000-8000-000000000002','release_match','waiting_model',1,repeat('a',64),'{}'),
 ('e6400000-0000-4000-8000-000000000004','release','e6400000-0000-4000-8000-000000000002','recording_match','waiting_model',1,repeat('b',64),'{}');`)
			}
			if m.Version == 65 {
				// 基线含需要保留的旧操作、旧标记、旧署名和工作状态，由快照逐行比较。
				rollbackExec(t, db, `INSERT INTO library_roots(id,path) VALUES('c6000000-0000-4000-8000-000000000065','/synthetic/track-rollback');
 INSERT INTO media(id,release_id,position) VALUES('c2000000-0000-4000-8000-000000000065','e6400000-0000-4000-8000-000000000002',1);
 INSERT INTO tracks(id,medium_id,position,title,artist,source_root_id,relative_path,observed_at) VALUES
 ('c3000000-0000-4000-8000-000000000065','c2000000-0000-4000-8000-000000000065',1,'扫描标题','扫描艺人','c6000000-0000-4000-8000-000000000065','track.flac',now());
 INSERT INTO operations(id,actor_id,operation_type,target_type,target_id,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id)
 VALUES('d6500000-0000-4000-8000-000000000005','f6400000-0000-4000-8000-000000000000','release_correction','release','e6400000-0000-4000-8000-000000000002',gen_random_uuid(),'release',1,2,'{}','{}','{}','fixture');
 INSERT INTO curation_suppressions(target_type,target_id,subject,value_hash,value,operation_id,created_by) VALUES
 ('track','c3000000-0000-4000-8000-000000000065','lyrics',repeat('a',64),'{}',NULL,'f6400000-0000-4000-8000-000000000000'),
 ('release','e6400000-0000-4000-8000-000000000002','title',repeat('a',64),'{}','d6500000-0000-4000-8000-000000000005','f6400000-0000-4000-8000-000000000000');
 INSERT INTO artist_credits(id,track_id,role,name_id,position,layer) VALUES
 (gen_random_uuid(),'c3000000-0000-4000-8000-000000000065','track_artist','a2000000-0000-4000-8000-000000000001',1,'scan'),
 (gen_random_uuid(),'c3000000-0000-4000-8000-000000000065','composer','a2000000-0000-4000-8000-000000000001',1,'correction');
 INSERT INTO artist_credits(id,release_id,role,name_id,position,layer)
 VALUES(gen_random_uuid(),'e6400000-0000-4000-8000-000000000002','album_artist','a2000000-0000-4000-8000-000000000001',1,'auto');
 INSERT INTO curation_work_states(subject,target_id,rule_version,input_digest,status,result)
 VALUES('release','e6400000-0000-4000-8000-000000000002',1,repeat('a',64),'matched','{"track_fields":true}');
 INSERT INTO steward_tool_calls(id,message_id,position,tool_name,input,status)
 VALUES('f6500000-0000-4000-8000-000000000003','f6400000-0000-4000-8000-000000000002',1,'revert_operation','{}','failed');
 INSERT INTO steward_plan_items(id,plan_id,tool_call_id,position,tool_name,input,preview,revertible,idempotency_key,decision)
 SELECT 'f6500000-0000-4000-8000-000000000005','f6400000-0000-4000-8000-000000000004',id,position,tool_name,'{}','{}',true,idempotency_key,'declined'
 FROM steward_tool_calls WHERE id='f6500000-0000-4000-8000-000000000003';
 INSERT INTO curation_model_exceptions(pending_item_id,plan_item_id,tool,input,preview,provider,review_id,review_model,review_decision,review_source)
 VALUES('e6400000-0000-4000-8000-000000000003','f6400000-0000-4000-8000-000000000005','correct_release','{}','{}','synthetic',gen_random_uuid(),'synthetic-review','rejected','model');`)
			}
			if m.Version == 67 {
				rollbackExec(t, db, beforeReleaseMergeMigration)
			}
			if m.Version == 68 {
				rollbackExec(t, db, beforeArtistMediaMigration)
			}
			s0 := rollbackSnapshot(t, db)
			rollbackApply(t, db, migrationPrefix(available, m.Version))
			s1 := rollbackSnapshot(t, db)
			fixture, ok := rollbackFixtures[m.Version]
			if rollbackNeedsFixture(s0, s1) && !ok {
				t.Fatalf("迁移 %d 改变旧约束或枚举，必须补 rollbackFixtures 的新取值数据", m.Version)
			}
			if fixture != "" {
				rollbackExec(t, db, fixture)
			}
			if m.Version == 65 {
				var title, artist string
				var revision int64
				err := db.QueryRow(`SELECT title,artist,edit_revision FROM track_effective WHERE id='c3000000-0000-4000-8000-000000000065'`).Scan(&title, &artist, &revision)
				if err != nil || title != "人工标题" || artist != "自动艺人" || revision != 5 {
					t.Fatalf("0065 升级夹具的有效值：title=%q artist=%q revision=%d err=%v", title, artist, revision, err)
				}
			}
			if m.Version == 56 {
				var valid bool
				if err := db.QueryRow(`SELECT protocol='responses' AND overrides->>'protocol'='responses' AND probe_result->>'protocol'='responses'
 AND search_source='tavily' AND search_key_ciphertext IS NOT NULL FROM steward_user_settings WHERE user_id='f5600000-0000-4000-8000-000000000000'`).Scan(&valid); err != nil || !valid {
					t.Fatalf("0056 新协议与搜索夹具未生效：valid=%v err=%v", valid, err)
				}
			}
			if m.Version == 50 {
				var valid bool
				if err := db.QueryRow(`SELECT lyrics='外置歌词' AND lyrics_source='lrc_file' FROM tracks WHERE id='c3000000-0000-4000-8000-000000000050'`).Scan(&valid); err != nil || !valid {
					t.Fatalf("0050 外置夹具未生效：valid=%v err=%v", valid, err)
				}
			}
			if m.Version == 42 {
				var trigger string
				var enabled bool
				var hour int
				if err := db.QueryRow(`SELECT trigger_kind FROM scan_runs WHERE id='f2000000-0000-4000-8000-000000000001'`).Scan(&trigger); err != nil || trigger != "scheduled" {
					t.Fatalf("0042 定时扫描夹具：trigger=%q err=%v", trigger, err)
				}
				if err := db.QueryRow(`SELECT enabled,hour FROM scan_schedule WHERE id=1`).Scan(&enabled, &hour); err != nil || enabled || hour != 5 {
					t.Fatalf("0042 设置夹具：enabled=%v hour=%d err=%v", enabled, hour, err)
				}
			}
			if m.Version == 41 {
				var mode string
				if err := db.QueryRow(`SELECT scan_mode FROM scan_runs WHERE id='f2000000-0000-4000-8000-000000000001'`).Scan(&mode); err != nil || mode != "full" {
					t.Fatalf("0041 新扫描模式夹具：mode=%q err=%v", mode, err)
				}
			}
			if m.Version == 67 {
				// 降级保留转移后的收藏，其他旧表仍按升级前的快照逐行比对。
				moved := rollbackSnapshot(t, db)
				s0.Data["favorite_albums"] = moved.Data["favorite_albums"]
				s0.Data["personal_favorite_state"] = moved.Data["personal_favorite_state"]
			}
			if m.Version == 35 {
				// 改路径是已提交业务状态，下行只删除新类型收据，不恢复旧路径或版本。
				// 以回退前的完整目录数据作期望，仍检查下行没有误改任何目录列。
				s0.Data["library_roots"] = rollbackSnapshot(t, db).Data["library_roots"]
			}
			rollbackApply(t, db, migrationPrefix(available, m.Version-1))
			s2 := rollbackSnapshot(t, db)
			if m.Version == 67 {
				assertMergedDiscRestored(t, db)
			}
			if m.Version == 64 {
				// 快照还会核对新增列、表和索引消失，且旧表的完整数据回到基线。
				var restored, removed bool
				err := db.QueryRow(`SELECT
 (SELECT decision='declined' FROM steward_plan_items WHERE id='f6400000-0000-4000-8000-000000000005')
 AND (SELECT count(*)=2 FROM curation_pending_items WHERE status='waiting_model' AND target_id='e6400000-0000-4000-8000-000000000002'),
 NOT EXISTS (SELECT 1 FROM steward_conversations WHERE id='f6400000-0000-4000-8000-000000000006')
 AND NOT EXISTS (SELECT 1 FROM steward_messages WHERE id='f6400000-0000-4000-8000-000000000007')
 AND NOT EXISTS (SELECT 1 FROM steward_plans WHERE id='f6400000-0000-4000-8000-000000000008')
 AND to_regclass('curation_model_exceptions') IS NULL AND to_regclass('curation_model_usage') IS NULL`).Scan(&restored, &removed)
				if err != nil || !restored || !removed {
					t.Fatalf("0064 下行未转换旧行或删除后台数据：restored=%v removed=%v err=%v", restored, removed, err)
				}
			}
			if m.Version == 50 {
				var cleared bool
				if err := db.QueryRow(`SELECT lyrics IS NULL AND lyrics_source IS NULL FROM tracks WHERE id='c3000000-0000-4000-8000-000000000050'`).Scan(&cleared); err != nil || !cleared {
					t.Fatalf("0050 下行没有清空外置行：cleared=%v err=%v", cleared, err)
				}
			}
			if m.Version == 35 {
				var path string
				var revision int64
				err := db.QueryRow(`SELECT path,revision FROM library_roots WHERE id='f1000000-0000-4000-8000-000000000001'`).Scan(&path, &revision)
				if err != nil || path != "/rollback-relocated" || revision != 2 {
					t.Fatalf("0035 下行不应撤销已改路径或版本：path=%q revision=%d err=%v", path, revision, err)
				}
			}
			if diff := postgrestest.Diff(s0, s2); len(diff) > 0 {
				t.Fatalf("升级→回退不一致：\n%s", strings.Join(diff, "\n"))
			}
			rollbackApply(t, db, migrationPrefix(available, m.Version))
			s3 := rollbackSnapshot(t, db)
			// 再升级只比较结构和行数；种子 UUID、时间等合法非确定值不应误报。
			countsOnly := func(snapshot postgrestest.SchemaSnapshot) postgrestest.SchemaSnapshot {
				data := make(map[string]string, len(snapshot.Data))
				for table, value := range snapshot.Data {
					count, _, _ := strings.Cut(value, " md5=")
					data[table] = count
				}
				return postgrestest.SchemaSnapshot{Structure: snapshot.Structure, Data: data}
			}
			if diff := postgrestest.Diff(countsOnly(s1), countsOnly(s3)); len(diff) > 0 {
				t.Fatalf("再次升级结构/行数不一致：\n%s", strings.Join(diff, "\n"))
			}
		})
	}
}

func TestPostgreSQLSchemaSnapshotDetectsChanges(t *testing.T) {
	for _, tc := range []struct{ name, ddl, key string }{
		{"列", `ALTER TABLE rollback_probe ADD COLUMN leftover text`, "column/rollback_probe/leftover"},
		{"约束", `ALTER TABLE rollback_probe ADD CONSTRAINT extra_unique UNIQUE(value)`, "constraint/rollback_probe/extra_unique"},
		{"索引", `CREATE INDEX leftover_idx ON rollback_probe(value)`, "index/leftover_idx"},
		{"CHECK取值", `ALTER TABLE rollback_probe DROP CONSTRAINT value_check; ALTER TABLE rollback_probe ADD CONSTRAINT value_check CHECK(value IN ('原有数据','新版取值'))`, "constraint/rollback_probe/value_check"},
		{"数据", `UPDATE rollback_probe SET value='被更改'`, "数据 rollback_probe"},
		{"残留行", `INSERT INTO rollback_probe VALUES(2,'残留')`, "数据 rollback_probe"},
		{"视图", `CREATE VIEW leftover_view AS SELECT id FROM rollback_probe`, "view/leftover_view"},
		{"函数", `CREATE FUNCTION leftover_fn() RETURNS int LANGUAGE sql AS 'SELECT 1'`, "function/leftover_fn"},
		{"枚举", `CREATE TYPE leftover_enum AS ENUM ('a','b')`, "enum/leftover_enum"},
		{"域", `CREATE DOMAIN leftover_domain AS int CHECK(VALUE>0)`, "domain/leftover_domain"},
		{"序列", `CREATE SEQUENCE leftover_seq START 9 INCREMENT 2`, "sequence/leftover_seq"},
		{"触发器", `CREATE FUNCTION leftover_trigger_fn() RETURNS trigger LANGUAGE plpgsql AS 'BEGIN RETURN NEW; END'; CREATE TRIGGER leftover_trigger BEFORE INSERT ON rollback_probe FOR EACH ROW EXECUTE FUNCTION leftover_trigger_fn()`, "trigger/rollback_probe/leftover_trigger"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openMigrationTestDatabase(t)
			source := rollbackTestFS(30)
			rollbackApply(t, db, source)
			rollbackExec(t, db, `ALTER TABLE rollback_probe ADD CONSTRAINT value_check CHECK(value <> '禁止')`)
			before := rollbackSnapshot(t, db)
			rollbackExec(t, db, tc.ddl)
			after := rollbackSnapshot(t, db)
			diff := postgrestest.Diff(before, after)
			if !strings.Contains(strings.Join(diff, "\n"), tc.key) {
				t.Fatalf("快照漏检 %s：%v", tc.name, diff)
			}
			if tc.name == "CHECK取值" && !rollbackNeedsFixture(before, after) {
				t.Fatal("约束变化必须要求夹具")
			}
		})
	}
	t.Run("列序号和schema名称归一化", func(t *testing.T) {
		first, second := openMigrationTestDatabase(t), openMigrationTestDatabase(t)
		rollbackApply(t, first, rollbackTestFS(30))
		rollbackApply(t, second, rollbackTestFS(30))
		rollbackExec(t, second, `ALTER TABLE rollback_probe ADD COLUMN discarded int; ALTER TABLE rollback_probe DROP COLUMN discarded`)
		// 最后一列删除再添加，逻辑顺序不变但 attnum 真的改变。
		rollbackExec(t, second, `ALTER TABLE rollback_probe DROP COLUMN value; ALTER TABLE rollback_probe ADD COLUMN value text; UPDATE rollback_probe SET value='原有数据'`)
		a, b := rollbackSnapshot(t, first), rollbackSnapshot(t, second)
		if diff := postgrestest.Diff(a, b); len(diff) > 0 {
			t.Fatalf("物理列号/schema/applied_at 不应影响快照：%v", diff)
		}
		rollbackExec(t, second, `UPDATE schema_migrations SET applied_at=applied_at+interval '1 day'`)
		if !reflect.DeepEqual(b, rollbackSnapshot(t, second)) {
			t.Fatal("applied_at 未排除")
		}
	})
	t.Run("千行阈值", func(t *testing.T) {
		db := openMigrationTestDatabase(t)
		rollbackApply(t, db, rollbackTestFS(30))
		rollbackExec(t, db, `CREATE TABLE many_rows(id int); INSERT INTO many_rows SELECT generate_series(1,1001)`)
		before := rollbackSnapshot(t, db)
		if before.Data["many_rows"] != "rows=1001" {
			t.Fatalf("大表不应计算摘要：%v", before.Data)
		}
	})
}

func TestRollbackFixtureDetection(t *testing.T) {
	for _, key := range []string{"constraint/t/c", "enum/state"} {
		a := postgrestest.SchemaSnapshot{Structure: map[string]string{key: "old"}}
		b := postgrestest.SchemaSnapshot{Structure: map[string]string{key: "new"}}
		if !rollbackNeedsFixture(a, b) {
			t.Errorf("漏检：%s", key)
		}
	}
}

// 确认嵌入 FS 能被原样复制，不把 .down.sql 算进正向版本数。
func TestEmbeddedMigrationRollbackFiles(t *testing.T) {
	available, err := loadMigrations(migrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	copied := migrationPrefix(available, available[len(available)-1].Version)
	for name := range copied {
		raw, err := fs.ReadFile(migrations.Files, name)
		if err != nil || string(raw) != string(copied[name].Data) {
			t.Fatalf("复制改变迁移：%s %v", name, err)
		}
	}
}
