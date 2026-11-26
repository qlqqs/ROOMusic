package postgres

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/migrations"
)

// 新迁移若改变旧约束或枚举，必须提供新版数据，证明回退后不留新表数据。
var rollbackFixtures = map[int64]string{
	106: `UPDATE curation_work_states SET result=result || '{"judge":{"used":1,"next":0}}'::jsonb WHERE subject='release' AND target_id='e9800000-0000-4000-8000-000000000002';
 INSERT INTO curation_artist_negative_pairs(name_low,name_high,reason,model) VALUES('合成甲','合成乙','不同身份','mock-model');`,
	104: `INSERT INTO operations(id,actor_id,operation_type,target_type,target_id,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id)
 VALUES ('d1030000-0000-4000-8000-000000000001','f1030000-0000-4000-8000-000000000001','local_model_grouping','release',
 'e1030000-0000-4000-8000-000000000001',gen_random_uuid(),'grouping',1,2,'{}','{}','{}','fixture');
 INSERT INTO operations(id,actor_id,operation_type,target_type,target_id,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id,reverts_operation_id)
 SELECT gen_random_uuid(),actor_id,'revert',target_type,target_id,gen_random_uuid(),'undo',2,3,'{}','{}','{}','fixture',id
 FROM operations WHERE id='d1030000-0000-4000-8000-000000000001';`,
	103: `UPDATE curation_work_states SET status='query_error',retry_count=3,next_attempt_at=NULL,
 result=result || '{"retry":{"step":"isrc","kind":"album","source":"musicbrainz","reason":"invalid_response"}}'::jsonb
 WHERE subject='release' AND target_id='e9800000-0000-4000-8000-000000000002';`,
	102: `UPDATE curation_work_states SET requery_match_version=105
 WHERE subject='release' AND target_id='e9800000-0000-4000-8000-000000000002';
 INSERT INTO curation_release_match_results(release_id,source,status,checked_at,conflicts)
 VALUES('e9800000-0000-4000-8000-000000000002','synthetic','conflict','2026-01-02T00:00:00Z',ARRAY['track_count']);`,
	87: `INSERT INTO external_links(id,source,object_type,object_id,external_id,method,confidence)
 VALUES('e8700000-0000-4000-8000-000000000003','musicbrainz','release_group_match','e8700000-0000-4000-8000-000000000002','synthetic-group','search','certain');
 INSERT INTO curation_work_states(subject,target_id,rule_version,input_digest,status,next_attempt_at) VALUES
 ('release','e8700000-0000-4000-8000-000000000002',102,repeat('a',64),'unlisted',now()+interval '90 days'),
 ('release','e8700000-0000-4000-8000-000000000004',102,repeat('b',64),'no_usable_candidates',NULL);
 INSERT INTO curation_pending_items(id,target_type,target_id,kind,status,route,rule_version,input_digest,evidence)
 VALUES('e8700000-0000-4000-8000-000000000005','release','e8700000-0000-4000-8000-000000000002','release_match','waiting_route','lookup',102,repeat('a',64),'{}');`,
	85: `INSERT INTO playback_devices(id,user_id,kind,name) VALUES
 ('f8500000-0000-4000-8000-000000000001','f6900000-0000-4000-8000-000000000001','subsonic','合成客户端');
 INSERT INTO playback_zones(id,user_id,device_id) VALUES
 ('f8500000-0000-4000-8000-000000000002','f6900000-0000-4000-8000-000000000001','f8500000-0000-4000-8000-000000000001');`,
	79: `UPDATE playback_devices SET sync_delay_ms=-500 WHERE id='f7900000-0000-4000-8000-000000000001';
 UPDATE playback_devices SET sync_delay_ms=500 WHERE id='f7900000-0000-4000-8000-000000000002';
 INSERT INTO playback_group_members(device_id,user_id,leader_zone_id,ordinal)
 VALUES ('f7900000-0000-4000-8000-000000000002','f6900000-0000-4000-8000-000000000001','f7900000-0000-4000-8000-000000000003',1);`,
	78: `UPDATE upnp_settings SET slimproto_enabled=false WHERE id=1;
 INSERT INTO playback_devices(id,user_id,kind,name,external_id,max_sample_rate)
 VALUES ('f7800000-0000-4000-8000-000000000001','f6900000-0000-4000-8000-000000000001','slimproto','合成 SlimProto 播放器','slim:02:00:00:00:00:01',384000);
 INSERT INTO playback_zones(id,user_id,device_id,position_seconds)
 VALUES ('f7800000-0000-4000-8000-000000000002','f6900000-0000-4000-8000-000000000001','f7800000-0000-4000-8000-000000000001',12);
 INSERT INTO slimproto_device_formats(udn,declared_max_sample_rate,max_sample_rate,volume)
 VALUES ('slim:02:00:00:00:00:01',768000,384000,'fixed');`,
	77: `INSERT INTO playback_devices(id,user_id,kind,name,external_id,max_sample_rate)
 VALUES ('f7700000-0000-4000-8000-000000000001','f6900000-0000-4000-8000-000000000001','airplay','合成接收端','airplay:02:00:00:00:00:01',48000);
 INSERT INTO playback_zones(id,user_id,device_id,position_seconds)
 VALUES ('f7700000-0000-4000-8000-000000000002','f6900000-0000-4000-8000-000000000001','f7700000-0000-4000-8000-000000000001',12);
 INSERT INTO playback_queue_entries(zone_id,entry_id,position,track_id)
 VALUES ('f7700000-0000-4000-8000-000000000002','f7700000-0000-4000-8000-000000000003',0,'f6900000-0000-4000-8000-000000000008');
 INSERT INTO airplay_device_formats VALUES ('airplay:02:00:00:00:00:01',48000,24);`,
	76: `INSERT INTO upnp_manual_devices(udn,address,name,model,capabilities,kind)
 VALUES ('airplay:02:00:00:00:00:01','192.168.44.3:7000','合成接收端','Fake','{}','airplay');
 INSERT INTO upnp_device_grants(udn,user_id) VALUES ('airplay:02:00:00:00:00:01','f6900000-0000-4000-8000-000000000001');
 INSERT INTO airplay_pairings(udn,credentials_ciphertext) VALUES ('airplay:02:00:00:00:00:01','synthetic');`,
	75: `INSERT INTO playback_devices(id,user_id,kind,name,external_id,max_sample_rate)
 VALUES ('f7400000-0000-4000-8000-000000000002','f6900000-0000-4000-8000-000000000001','cast','合成 Cast 设备','cast:migration',96000);
 INSERT INTO playback_zones(id,user_id,device_id,position_seconds)
 VALUES ('f7400000-0000-4000-8000-000000000003','f6900000-0000-4000-8000-000000000001','f7400000-0000-4000-8000-000000000002',18);
 INSERT INTO cast_device_formats VALUES ('cast:migration','flac',96000);
 INSERT INTO upnp_device_grants VALUES ('cast:migration','f6900000-0000-4000-8000-000000000001');
 INSERT INTO upnp_manual_devices(udn,address,name,model,capabilities,kind)
 VALUES ('cast:migration','192.168.44.2:8009','客厅','Fake','{}','cast');`,
	74: `INSERT INTO curation_pending_items(id,target_type,artist_id,kind,status,rule_version,input_digest,evidence)
 VALUES ('e7300000-0000-4000-8000-000000000001','artist','a6800000-0000-4000-8000-000000000001','artist_identity','resolved',1,repeat('a',64),'{}');
 INSERT INTO curation_model_exceptions(pending_item_id,plan_item_id,tool,input,preview,provider,review_id,review_model,review_decision,review_source,status,decided_at,decided_by)
 VALUES ('e7300000-0000-4000-8000-000000000001',gen_random_uuid(),'resolve_artist_identity','{}','{}','synthetic',gen_random_uuid(),'synthetic-review','needs_human_confirmation','model','superseded',now(),'f6800000-0000-4000-8000-000000000001');
 INSERT INTO artist_external_artworks(artist_id,storage_key,mime,width,height,source,source_url,citations,operation_id)
 VALUES ('a6800000-0000-4000-8000-000000000001','synthetic.png','image/png',200,200,'web_search','https://example.org/artist',
 '[{"url":"https://example.org/artist","title":"合成图片出处"}]','d7300000-0000-4000-8000-000000000001');
 INSERT INTO artist_external_bios(artist_id,text,lang,source,source_url,citations,operation_id)
 VALUES ('a6800000-0000-4000-8000-000000000001','合成简介','zh','web_search','https://example.org/artist',
 '[{"url":"https://example.org/artist","title":"合成简介出处"}]','d7300000-0000-4000-8000-000000000002');`,
	72: artistManualMediaRollbackFixture,
	71: sourcePluginRollbackFixture,
	70: upnpPlaybackRollbackFixture,
	69: upnpDiscoveryRollbackFixture,
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

// PostgreSQL 必须原子拒绝仍含新取值的回退。逐项移除夹具后继续标准往返。
func checkCurationRulesRollback(t *testing.T, db *sql.DB, previous fstest.MapFS) {
	t.Helper()
	for _, tc := range []struct{ constraint, cleanup string }{
		{"curation_pending_items_status_check", `DELETE FROM curation_pending_items WHERE id='e8700000-0000-4000-8000-000000000005'`},
		{"curation_work_states_status_check", `DELETE FROM curation_work_states WHERE subject='release' AND target_id IN ('e8700000-0000-4000-8000-000000000002','e8700000-0000-4000-8000-000000000004')`},
		{"external_links_object_type_check", `DELETE FROM external_links WHERE id='e8700000-0000-4000-8000-000000000003'`},
	} {
		before := rollbackSnapshot(t, db)
		err := applyMigrationsFromFS(t.Context(), db, previous)
		var state *pgconn.PgError
		if !errors.As(err, &state) || state.SQLState() != "23514" || state.ConstraintName != tc.constraint {
			t.Fatalf("新版数据存在时，回退应由 %s 拒绝，得到 %v，数据库错误 %v", tc.constraint, err, state)
		}
		if diff := postgrestest.Diff(before, rollbackSnapshot(t, db)); len(diff) != 0 {
			t.Fatalf("回退被拒绝后仍有变更：%v", diff)
		}
		rollbackExec(t, db, tc.cleanup)
	}
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
			if m.Version == 103 {
				rollbackExec(t, db, `INSERT INTO curation_work_states(subject,target_id,rule_version,input_digest,status,result,updated_at,next_attempt_at)
 VALUES('release','e9800000-0000-4000-8000-000000000002',104,repeat('a',64),'try_later','{"reason":"synthetic"}',now(),now());`)
			}
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
			if m.Version == 68 || m.Version == 72 || m.Version == 74 {
				rollbackExec(t, db, beforeArtistMediaMigration)
			}
			if m.Version == 74 {
				rollbackExec(t, db, `INSERT INTO operations(id,actor_id,operation_type,target_type,target_id,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id)
 SELECT id::uuid,'f6800000-0000-4000-8000-000000000001',kind,'artist','a6800000-0000-4000-8000-000000000001',
 gen_random_uuid(),kind,1,2,'{}','{}','{}','fixture'
 FROM (VALUES ('d7300000-0000-4000-8000-000000000001','artist_external_artwork'),
 ('d7300000-0000-4000-8000-000000000002','artist_external_bio')) AS f(id,kind)`)
			}
			if m.Version == 72 {
				rollbackExec(t, db, artistMediaRollbackFixture)
				rollbackExec(t, db, beforeArtistManualMediaMigration)
			}
			if m.Version == 71 {
				rollbackExec(t, db, beforeSourcePluginMigration)
			}
			if m.Version == 69 || m.Version == 70 || m.Version == 75 || m.Version == 76 || m.Version == 77 || m.Version == 78 || m.Version == 79 || m.Version == 85 {
				rollbackExec(t, db, beforeUPnPMigration)
			}
			if m.Version == 75 || m.Version == 76 || m.Version == 77 || m.Version == 78 {
				rollbackExec(t, db, upnpDiscoveryRollbackFixture)
				rollbackExec(t, db, upnpPlaybackRollbackFixture)
			}
			if m.Version == 79 {
				rollbackExec(t, db, `INSERT INTO playback_devices(id,user_id,kind,name,external_id,max_sample_rate) VALUES
 ('f7900000-0000-4000-8000-000000000001','f6900000-0000-4000-8000-000000000001','slimproto','合成主设备','slim:02:00:00:00:00:01',96000),
 ('f7900000-0000-4000-8000-000000000002','f6900000-0000-4000-8000-000000000001','slimproto','合成成员设备','slim:02:00:00:00:00:02',48000);
 INSERT INTO playback_zones(id,user_id,device_id,position_seconds) VALUES
 ('f7900000-0000-4000-8000-000000000003','f6900000-0000-4000-8000-000000000001','f7900000-0000-4000-8000-000000000001',12),
 ('f7900000-0000-4000-8000-000000000004','f6900000-0000-4000-8000-000000000001','f7900000-0000-4000-8000-000000000002',24);`)
			}
			if m.Version == 87 {
				rollbackExec(t, db, `INSERT INTO release_groups(id) VALUES('e8700000-0000-4000-8000-000000000001');
 INSERT INTO releases(id,group_id,title,artist) VALUES('e8700000-0000-4000-8000-000000000002',
 'e8700000-0000-4000-8000-000000000001','整理规则迁移测试发行','测试艺人');`)
			}
			if m.Version == 102 {
				rollbackExec(t, db, `INSERT INTO release_groups(id) VALUES('e9800000-0000-4000-8000-000000000001');
 INSERT INTO releases(id,group_id,title,artist)
 SELECT id::uuid,'e9800000-0000-4000-8000-000000000001','合成发行','合成艺人'
 FROM (VALUES ('e9800000-0000-4000-8000-000000000002'),('e9800000-0000-4000-8000-000000000003'),
              ('e9800000-0000-4000-8000-000000000004')) AS fixture(id);
 INSERT INTO curation_work_states(subject,target_id,rule_version,input_digest,status,result)
 VALUES ('release','e9800000-0000-4000-8000-000000000002',104,repeat('a',64),'unlisted','{}'),
        ('release','e9800000-0000-4000-8000-000000000003',104,repeat('b',64),'no_usable_candidates','{"reason":"candidates_revoked"}'),
        ('release','e9800000-0000-4000-8000-000000000004',104,repeat('c',64),'uncertain','{"method":"search","conflicts":["too_many"]}');`)
			}
			if strings.HasSuffix(m.Name, "_curation_local_assist.sql") {
				rollbackExec(t, db, `INSERT INTO users(id,username,password_hash,role) VALUES('f1030000-0000-4000-8000-000000000001','local-assist-rollback','synthetic','admin');`)
			}
			scanGrouping := strings.HasSuffix(m.Name, "_scan_grouping.sql")
			if scanGrouping {
				rollbackExec(t, db, scanGroupingLegacyRows)
			}
			artistWeb := strings.HasSuffix(m.Name, "_artist_web_lookup.sql")
			if artistWeb {
				rollbackExec(t, db, `INSERT INTO artists(id,initial_name) VALUES
 ('a1030000-0000-4000-8000-000000000001','合成候选艺人'),
 ('a1030000-0000-4000-8000-000000000002','已有对应艺人');
 INSERT INTO curation_pending_items(id,target_type,artist_id,kind,status,rule_version,input_digest,evidence,resolution,resolved_at,item_tool_calls,item_tokens)
 VALUES('a1030000-0000-4000-8000-000000000003','artist','a1030000-0000-4000-8000-000000000001','artist_identity','resolved',1,'合成旧证据','{}','suggestion_recorded',now(),4,120);
 INSERT INTO curation_model_exceptions(id,pending_item_id,tool,input,preview,provider,reason_code)
 VALUES('a1030000-0000-4000-8000-000000000004','a1030000-0000-4000-8000-000000000003','resolve_artist_identity','{}','{}','synthetic','no_evidence');
 INSERT INTO external_links(id,source,object_type,object_id,external_id,method,confidence)
 VALUES('a1030000-0000-4000-8000-000000000005','musicbrainz','artist','a1030000-0000-4000-8000-000000000002',
 'a1030000-0000-4000-8000-000000000006','artist_credit','certain');
 INSERT INTO curation_model_usage(day,input_tokens,output_tokens) VALUES('2026-01-02',90,30);`)
			}
			s0 := rollbackSnapshot(t, db)
			rollbackApply(t, db, migrationPrefix(available, m.Version))
			s1 := rollbackSnapshot(t, db)
			if artistWeb {
				assertArtistWebMigrationSeed(t, db)
				rollbackExec(t, db, `UPDATE curation_artist_web_queue SET generation=1,state='stopped',started_at=now(),stopped_at=now() WHERE id;
 UPDATE curation_pending_items SET artist_web_generation=1,artist_web_suspended=true,artist_web_priority=2
 WHERE id='a1030000-0000-4000-8000-000000000003';`)
			}
			if m.Version == 102 {
				assertProgramMatchMigration(t, db)
			}
			if m.Version == 103 {
				var status, result string
				err := db.QueryRowContext(t.Context(), `SELECT status,result::text FROM curation_work_states
					WHERE subject='release' AND target_id='e9800000-0000-4000-8000-000000000002'`).Scan(&status, &result)
				if err != nil || status != "try_later" || strings.Contains(result, "\"retry\"") {
					t.Fatalf("0103 下行应恢复旧 try_later 并清除断点，状态=%q 结果=%s 错误=%v", status, result, err)
				}
			}
			if scanGrouping {
				assertScanGroupingMigrationUp(t, db)
				checkScanGroupingAttachmentConstraints(t, db)
				// 从新版未知曲号的内部次序恢复旧曲位；已知曲号应保持原值。
				rollbackExec(t, db, `UPDATE tracks SET position=0,sort_position=7 WHERE id='e9800000-0000-4000-8000-000000000005';
 UPDATE tracks SET sort_position=2 WHERE id='e9800000-0000-4000-8000-000000000004'`)
			}
			if m.Version == 78 {
				var enabled bool
				err := db.QueryRowContext(t.Context(), "SELECT enabled AND NOT shared AND slimproto_enabled FROM upnp_settings WHERE id=1").Scan(&enabled)
				if err != nil || !enabled {
					t.Fatalf("0078 保留旧设置并默认开启 SlimProto = %t，错误 %v", enabled, err)
				}
			}
			if m.Version == 76 {
				var defaultEnabled bool
				err := db.QueryRowContext(t.Context(), "SELECT airplay_enabled FROM upnp_settings WHERE id=1").Scan(&defaultEnabled)
				if err != nil || !defaultEnabled {
					t.Fatalf("0076 升级已有局域网设置后的 AirPlay 默认开关 = %t，错误 %v", defaultEnabled, err)
				}
			}
			if m.Version == 75 {
				var settingsPreserved bool
				var legacyKind string
				err := db.QueryRowContext(t.Context(), `SELECT enabled AND NOT shared AND cast_enabled AND upnp_enabled,
					(SELECT kind FROM upnp_manual_devices WHERE udn='uuid:upnp-migration') FROM upnp_settings WHERE id=1`).
					Scan(&settingsPreserved, &legacyKind)
				if err != nil || !settingsPreserved || legacyKind != "upnp" {
					t.Fatalf("0075 旧设置/手动设备迁移 = %v / %q，错误 %v", settingsPreserved, legacyKind, err)
				}
			}
			if strings.HasSuffix(m.Name, "_instance_insights.sql") {
				assertInsightsMigrationSeed(t, db)
				for _, tc := range []struct{ id, state string }{{"true", "23505"}, {"false", "23514"}} {
					_, err := db.ExecContext(t.Context(), `INSERT INTO instance_insights (id, instance_id) VALUES ($1::boolean, gen_random_uuid())`, tc.id)
					var rejected interface{ SQLState() string }
					if !errors.As(err, &rejected) || rejected.SQLState() != tc.state {
						t.Fatalf("插入 id=%s：错误 %v，期望 SQLSTATE %s", tc.id, err, tc.state)
					}
				}
				rollbackExec(t, db, `UPDATE instance_insights SET enabled=false, notice_acknowledged_at=now(), last_attempt_at=now(), last_error='http_503' WHERE id`)
			}
			if m.Version == 72 {
				assertArtistManualMediaBackfill(t, db)
			}
			if m.Version == 69 {
				var enabled, shared bool
				err := db.QueryRow("SELECT enabled,shared FROM upnp_settings WHERE id=1").Scan(&enabled, &shared)
				if err != nil || enabled || !shared {
					t.Fatalf("0069 默认设置 enabled=%v shared=%v err=%v", enabled, shared, err)
				}
			}
			fixture, ok := rollbackFixtures[m.Version]
			if rollbackNeedsFixture(s0, s1) && !ok {
				t.Fatalf("迁移 %d 改变旧约束或枚举，必须补 rollbackFixtures 的新取值数据", m.Version)
			}
			if fixture != "" {
				rollbackExec(t, db, fixture)
			}
			if m.Version == 87 {
				checkCurationRulesRollback(t, db, migrationPrefix(available, m.Version-1))
			}
			if m.Version == 65 {
				var title, artist string
				var revision int64
				err := db.QueryRow(`SELECT title,artist,edit_revision FROM track_effective WHERE id='c3000000-0000-4000-8000-000000000065'`).Scan(&title, &artist, &revision)
				if err != nil || title != "人工标题" || artist != "自动艺人" || revision != 5 {
					t.Fatalf("0065 升级夹具的有效值：title=%q artist=%q revision=%d err=%v", title, artist, revision, err)
				}
			}
			if m.Version == 71 {
				// 回退保留新版本产生的操作历史。其他表仍与升级前快照逐行比对。
				opHistory := rollbackSnapshot(t, db).Data["operations"]
				s0.Data["operations"], s1.Data["operations"] = opHistory, opHistory
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
			if scanGrouping {
				var restored bool
				err := db.QueryRowContext(t.Context(), `SELECT
 (SELECT position=7 FROM track_effective WHERE id='e9800000-0000-4000-8000-000000000005')
 AND (SELECT position=5 FROM track_effective WHERE id='e9800000-0000-4000-8000-000000000004')
 AND to_regclass('release_attachments') IS NULL AND to_regprocedure('catalog_list_sort_key(text)') IS NULL`).Scan(&restored)
				if err != nil || !restored {
					t.Fatalf("归组迁移回退后的旧曲位、附件表、排序函数：restored=%v err=%v", restored, err)
				}
			}
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
			if artistWeb {
				assertArtistWebMigrationSeed(t, db)
				if diff := postgrestest.Diff(s1, s3); len(diff) > 0 {
					t.Fatalf("艺人联网迁移再次升级未保留旧行：\n%s", strings.Join(diff, "\n"))
				}
			}
			if scanGrouping {
				assertScanGroupingMigrationUp(t, db)
			}
			if strings.HasSuffix(m.Name, "_instance_insights.sql") {
				assertInsightsMigrationSeed(t, db)
			}
			if m.Version == 72 {
				assertArtistManualMediaBackfill(t, db)
			}
			if m.Version == 102 {
				assertProgramMatchMigration(t, db)
			}
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

func assertArtistWebMigrationSeed(t *testing.T, db *sql.DB) {
	t.Helper()
	var seeded bool
	err := db.QueryRowContext(t.Context(), `SELECT
 (SELECT state='idle' AND generation=0 AND actor_id IS NULL AND started_at IS NULL AND stopped_at IS NULL
 FROM curation_artist_web_queue WHERE id) AND EXISTS (
 SELECT FROM curation_pending_items WHERE id='a1030000-0000-4000-8000-000000000003'
 AND artist_web_generation IS NULL AND artist_web_actor IS NULL AND artist_web_priority IS NULL AND NOT artist_web_suspended
 AND item_tool_calls=4 AND item_tokens=120)`).Scan(&seeded)
	if err != nil || !seeded {
		t.Fatalf("迁移默认队列与旧待定项的用量不符：seeded=%v，错误=%v", seeded, err)
	}
}

func assertProgramMatchMigration(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), `SELECT s.status,q.status,q.reason,q.conflicts::text,
 q.checked_at IS NULL,s.next_attempt_at IS NULL,s.file_digest IS NOT NULL
 FROM curation_work_states s JOIN curation_release_match_results q ON q.release_id=s.target_id
 WHERE s.subject='release' AND q.source='musicbrainz' ORDER BY q.release_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	want := []struct {
		status, reason string
		conflicts      []string
	}{
		{"unmatched", "", nil},
		{"unmatched", "candidates_revoked", nil},
		{"conflict", "", []string{"too_many"}},
	}
	for _, expected := range want {
		if !rows.Next() {
			t.Fatalf("0102 缺少来源结论 %v：%v", expected, rows.Err())
		}
		var workStatus, status, reason string
		var conflicts string
		var unknownTime, noDue, fileDigest bool
		if err := rows.Scan(&workStatus, &status, &reason, &conflicts, &unknownTime, &noDue, &fileDigest); err != nil {
			t.Fatal(err)
		}
		if workStatus != expected.status || status != expected.status || reason != expected.reason ||
			conflicts != "{"+strings.Join(expected.conflicts, ",")+"}" || !unknownTime || !noDue || !fileDigest {
			t.Errorf("0102 来源结论为 %q/%q、原因 %q、冲突 %s、未知时间 %t、无到期时间 %t、有文件摘要 %t，期望 %v",
				workStatus, status, reason, conflicts, unknownTime, noDue, fileDigest, expected)
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("0102 来源结论多于预期或读取失败：%v", rows.Err())
	}
}

func assertInsightsMigrationSeed(t *testing.T, db *sql.DB) {
	t.Helper()
	var id string
	var defaults bool
	err := db.QueryRowContext(t.Context(), `SELECT instance_id::text,
		enabled AND created_at IS NOT NULL AND notice_acknowledged_at IS NULL
		AND last_attempt_at IS NULL AND last_success_at IS NULL AND last_error IS NULL
		FROM instance_insights WHERE id`).Scan(&id, &defaults)
	if err != nil || !uuid.Valid(id) || id[14] != '4' || !defaults {
		t.Fatalf("0072 默认记录：实例 ID %q，默认值有效 %t，错误 %v", id, defaults, err)
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

func TestPostgreSQLCurationModelRulesMigrationRoundTrip(t *testing.T) {
	const version = 101
	available, err := loadMigrations(migrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	db := openMigrationTestDatabase(t)
	before, after := migrationPrefix(available, version-1), migrationPrefix(available, version)
	if _, ok := after["0101_curation_model_rules.sql"]; !ok {
		t.Fatal("缺少模型规则迁移")
	}
	rollbackApply(t, db, before)
	rollbackExec(t, db, `UPDATE curation_settings SET model_daily_token_limit=NULL,web_daily_token_limit=23456,model_concurrency=7 WHERE id=1;
 INSERT INTO curation_model_usage(day,input_tokens,output_tokens,web_input_tokens,web_output_tokens) VALUES
 ('2026-01-02',100,25,30,3),('2026-01-03',47,13,5,7);
 INSERT INTO release_groups(id) VALUES('f9800000-0000-4000-8000-000000000010');
 INSERT INTO releases(id,group_id,title,artist) VALUES
 ('f9800000-0000-4000-8000-000000000011','f9800000-0000-4000-8000-000000000010','合成旧触限发行','合成艺人'),
 ('f9800000-0000-4000-8000-000000000012','f9800000-0000-4000-8000-000000000010','合成旧来源退避发行','合成艺人');
 INSERT INTO curation_pending_items(id,target_type,target_id,kind,status,route,rule_version,input_digest,evidence,next_attempt_at,reset_item_usage_on_claim,attempts) VALUES
 ('f9800000-0000-4000-8000-000000000001','release','f9800000-0000-4000-8000-000000000011','release_match','waiting_route','lookup',102,'synthetic-budget','{}','2026-01-09T00:00:00Z',true,2),
 ('f9800000-0000-4000-8000-000000000002','release','f9800000-0000-4000-8000-000000000012','release_match','waiting_route','lookup',102,'synthetic-source','{}','2026-01-09T00:00:00Z',false,1);`)
	requireQuery := func(query string) {
		t.Helper()
		var result bool
		if err := db.QueryRowContext(t.Context(), query).Scan(&result); err != nil || !result {
			t.Fatalf("结果应为真，实际 %t，错误 %v，查询 %s", result, err, query)
		}
	}
	checkTotals := func() {
		t.Helper()
		requireQuery(`SELECT count(*)=2 AND bool_and(CASE WHEN day='2026-01-02' THEN input_tokens=100 AND output_tokens=25 ELSE day='2026-01-03' AND input_tokens=47 AND output_tokens=13 END) FROM curation_model_usage`)
	}
	checkQueue := func() {
		t.Helper()
		requireQuery(`SELECT count(*)=2 AND bool_and(status='waiting_route' AND attempts=0 AND CASE WHEN reset_item_usage_on_claim THEN next_attempt_at IS NULL ELSE next_attempt_at IS NOT DISTINCT FROM '2026-01-09T00:00:00Z'::timestamptz END) FROM curation_pending_items`)
	}
	rollbackApply(t, db, after)
	requireQuery(`SELECT model_daily_token_limit IS NULL AND model_concurrency=7 AND max_request_tokens=128000 FROM curation_settings WHERE id=1`)
	requireQuery(`SELECT count(*)=0 FROM information_schema.columns WHERE table_schema=current_schema() AND column_name IN ('web_daily_token_limit','web_input_tokens','web_output_tokens')`)
	checkTotals()
	checkQueue()
	rollbackExec(t, db, `UPDATE curation_settings SET model_daily_token_limit=98765 WHERE id=1`)
	rollbackApply(t, db, before)
	requireQuery(`SELECT model_daily_token_limit=98765 AND web_daily_token_limit IS NULL AND model_concurrency=7 FROM curation_settings WHERE id=1`)
	requireQuery(`SELECT bool_and(web_input_tokens=0 AND web_output_tokens=0) FROM curation_model_usage`)
	checkTotals()
	checkQueue()
	rollbackApply(t, db, after)
	requireQuery(`SELECT model_daily_token_limit=98765 AND model_concurrency=7 AND max_request_tokens=128000 FROM curation_settings WHERE id=1`)
	checkTotals()
	checkQueue()
}

const scanGroupingLegacyRows = `
 INSERT INTO library_roots(id,path) VALUES('e9800000-0000-4000-8000-000000000000','/synthetic/scan-grouping');
 INSERT INTO release_groups(id) VALUES('e9800000-0000-4000-8000-000000000001');
 INSERT INTO releases(id,group_id,title,artist) VALUES ('e9800000-0000-4000-8000-000000000002',
 'e9800000-0000-4000-8000-000000000001','旧发行','合成艺人');
 INSERT INTO media(id,release_id,position) VALUES ('e9800000-0000-4000-8000-000000000003','e9800000-0000-4000-8000-000000000002',1);
 INSERT INTO tracks(id,medium_id,position,title,artist,source_root_id,relative_path,observed_at) VALUES
 ('e9800000-0000-4000-8000-000000000004','e9800000-0000-4000-8000-000000000003',5,'旧曲目','合成艺人','e9800000-0000-4000-8000-000000000000','One.flac',NOW()),
 ('e9800000-0000-4000-8000-000000000005','e9800000-0000-4000-8000-000000000003',7,'补序曲目','合成艺人','e9800000-0000-4000-8000-000000000000','Two.flac',NOW());`

func assertScanGroupingMigrationUp(t *testing.T, db *sql.DB) {
	t.Helper()
	var tracks, versionTerms, emptyAttachments, viewColumns bool
	err := db.QueryRowContext(t.Context(), `
 WITH columns AS (
  SELECT table_name, array_agg(column_name||':'||data_type ORDER BY ordinal_position) AS signature
  FROM information_schema.columns
  WHERE table_schema=current_schema() AND table_name IN ('track_effective','tracks') GROUP BY table_name
 )
 SELECT (SELECT array_agg(COALESCE(sort_position,position) ORDER BY relative_path)=ARRAY[5,7]
                 AND bool_and(sort_position IS NULL) FROM track_effective),
        (SELECT scan_version_terms=ARRAY[]::text[] FROM releases WHERE id='e9800000-0000-4000-8000-000000000002'),
        NOT EXISTS (SELECT FROM release_attachments),
        (SELECT signature FROM columns WHERE table_name='tracks')=(SELECT signature FROM columns WHERE table_name='track_effective')`).
		Scan(&tracks, &versionTerms, &emptyAttachments, &viewColumns)
	if err != nil || !tracks || !versionTerms || !emptyAttachments || !viewColumns {
		t.Fatalf("归组上行后旧曲位=%v，版本词默认值=%v，附件为空=%v，视图列序和类型=%v，错误 %v",
			tracks, versionTerms, emptyAttachments, viewColumns, err)
	}
}

func checkScanGroupingAttachmentConstraints(t *testing.T, db *sql.DB) {
	t.Helper()
	const release = "e9800000-0000-4000-8000-000000000002"
	const insert = `INSERT INTO release_attachments(release_id,relative_path,kind,image_format,width,height)
		VALUES($1::uuid,$2,$3,$4,$5,$6)`
	rollbackExec(t, db, insert, release, "Booklet.pdf", "document", nil, nil, nil)
	rollbackExec(t, db, insert, release, "Scans/Page.tif", "image", "tiff", 4, 8)
	for _, tc := range []struct {
		name, state string
		args        []any
	}{
		{"同一发行同一路径", "23505", []any{release, "Booklet.pdf", "document", nil, nil, nil}},
		{"文档不能存图宽", "23514", []any{release, "Other.pdf", "document", nil, 1, nil}},
		{"图片宽不能是零", "23514", []any{release, "Zero.png", "image", "png", 0, 1}},
		{"图片高度不能缺失", "23514", []any{release, "Height.png", "image", "png", 1, nil}},
		{"图片格式不能缺失", "23514", []any{release, "Format.png", "image", nil, 1, 1}},
		{"附件种类受限", "23514", []any{release, "Unknown.txt", "text", nil, nil, nil}},
		{"发行必须存在", "23503", []any{uuid.New(), "Booklet.pdf", "document", nil, nil, nil}},
	} {
		_, err := db.ExecContext(t.Context(), insert, tc.args...)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != tc.state {
			t.Errorf("%s：错误 %v，期望 SQLSTATE %s", tc.name, err, tc.state)
		}
	}
	const otherRelease = "e9800000-0000-4000-8000-000000000006"
	rollbackExec(t, db, `INSERT INTO releases(id,group_id,title,artist)
		VALUES($1::uuid,'e9800000-0000-4000-8000-000000000001','临时发行','合成艺人')`, otherRelease)
	rollbackExec(t, db, insert, otherRelease, "Booklet.pdf", "document", nil, nil, nil)
	rollbackExec(t, db, `DELETE FROM releases WHERE id=$1::uuid`, otherRelease)
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM release_attachments`).Scan(&count); err != nil || count != 2 {
		t.Errorf("删除临时发行后附件数 = %d，期望保留原发行的两条附件，错误 %v", count, err)
	}
}

func TestPostgreSQLLocalAssistUpgrade(t *testing.T) {
	available, err := loadMigrations(migrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	var target migration
	for _, m := range available {
		if strings.HasSuffix(m.Name, "_curation_local_assist.sql") {
			target = m
			break
		}
	}
	if target.Version == 0 {
		t.Fatal("缺少本地辅助迁移")
	}
	db := openMigrationTestDatabase(t)
	before := migrationPrefix(available, target.Version-1)
	after := migrationPrefix(available, target.Version)
	rollbackApply(t, db, before)
	rollbackExec(t, db, `
 INSERT INTO users(id,username,password_hash,role)
 VALUES('a1030000-0000-4000-8000-000000000001','local-upgrade-test','synthetic','admin');
 INSERT INTO library_roots(id,path) VALUES('a1030000-0000-4000-8000-000000000002','/synthetic/local-upgrade');
 INSERT INTO release_groups(id) VALUES('a1030000-0000-4000-8000-000000000003');
 INSERT INTO releases(id,group_id,title,artist,source_root_id) VALUES
 ('a1030000-0000-4000-8000-000000000004','a1030000-0000-4000-8000-000000000003','旧标题','旧艺人','a1030000-0000-4000-8000-000000000002'),
 ('a1030000-0000-4000-8000-000000000005','a1030000-0000-4000-8000-000000000003','另一发行','旧艺人','a1030000-0000-4000-8000-000000000002');
 INSERT INTO curation_model_usage(day,input_tokens,output_tokens) VALUES('2026-01-01',321,123);
 INSERT INTO curation_pending_items(id,target_type,target_id,kind,status,rule_version,input_digest,evidence,resolution,resolved_at,claimed_at)
 SELECT id::uuid,'release',CASE WHEN status='in_progress' THEN 'a1030000-0000-4000-8000-000000000005'::uuid ELSE 'a1030000-0000-4000-8000-000000000004'::uuid END,'local_cleanup',status,3,id,'{}',resolution,
 CASE WHEN resolution IS NOT NULL THEN now() END, CASE WHEN status='in_progress' THEN now() END
 FROM (VALUES ('a1030000-0000-4000-8000-000000000011','resolved','local_cleanup'),
 ('a1030000-0000-4000-8000-000000000012','waiting_model',NULL),
 ('a1030000-0000-4000-8000-000000000013','in_progress',NULL)) AS old(id,status,resolution);
 INSERT INTO operations(id,actor_id,operation_type,target_type,target_id,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id)
 SELECT id::uuid,'a1030000-0000-4000-8000-000000000001',kind,'release','a1030000-0000-4000-8000-000000000004',gen_random_uuid(),kind,1,2,'{}','{}','{}','local-upgrade'
 FROM (VALUES ('a1030000-0000-4000-8000-000000000021','release_auto_correction'),
 ('a1030000-0000-4000-8000-000000000022','release_auto_correction')) AS old(id,kind);
 INSERT INTO operations(id,actor_id,operation_type,target_type,target_id,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id,reverts_operation_id)
 SELECT 'a1030000-0000-4000-8000-000000000023',actor_id,'revert',target_type,target_id,gen_random_uuid(),'undo-old',2,3,'{}','{}','{}','local-upgrade',id
 FROM operations WHERE id='a1030000-0000-4000-8000-000000000022';
 INSERT INTO release_auto_values(release_id,field,mode,text_value,operation_id)
 VALUES('a1030000-0000-4000-8000-000000000004','title','set','已写一部分','a1030000-0000-4000-8000-000000000021');
 `)
	require := func(query string) {
		t.Helper()
		var got bool
		if err := db.QueryRowContext(t.Context(), query).Scan(&got); err != nil || !got {
			t.Fatalf("查询 %s 应为真，实际 %v，错误 %v", query, got, err)
		}
	}
	checkOld := func() {
		t.Helper()
		require(`SELECT input_tokens=321 AND output_tokens=123 FROM curation_model_usage WHERE day='2026-01-01'`)
		require(`SELECT text_value='已写一部分' FROM release_auto_values WHERE field='title'`)
		require(`SELECT reverts_operation_id='a1030000-0000-4000-8000-000000000022' FROM operations WHERE id='a1030000-0000-4000-8000-000000000023'`)
		require(`SELECT count(*)=3 AND count(*) FILTER(WHERE resolution='inputs_changed')=2 AND count(*) FILTER(WHERE resolution='local_cleanup')=1
			FROM curation_pending_items WHERE kind='local_cleanup' AND status='resolved' AND rule_version=3`)
	}
	rollbackApply(t, db, after)
	checkOld()
	rollbackExec(t, db, `
 INSERT INTO operations(id,actor_id,operation_type,target_type,target_id,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id,reverts_operation_id)
 VALUES ('a1030000-0000-4000-8000-000000000030','a1030000-0000-4000-8000-000000000001','local_model_grouping','release',
 'a1030000-0000-4000-8000-000000000004',gen_random_uuid(),'group',3,4,'{}','{}','{}','local-upgrade',NULL),
 ('a1030000-0000-4000-8000-000000000031','a1030000-0000-4000-8000-000000000001','revert','release',
 'a1030000-0000-4000-8000-000000000004',gen_random_uuid(),'undo-group',4,5,'{}','{}','{}','local-upgrade','a1030000-0000-4000-8000-000000000030');
 INSERT INTO local_credit_parts(operation_id,target_id,role,source_ref,raw,parts,joins)
 VALUES('a1030000-0000-4000-8000-000000000021','a1030000-0000-4000-8000-000000000004','album_artist','synthetic','甲 & 乙','["甲","乙"]','[" & "]');
 `)
	rollbackApply(t, db, before)
	checkOld()
	require(`SELECT to_regclass('local_model_groupings') IS NULL AND to_regclass('local_credit_parts') IS NULL`)
	require(`SELECT count(*)=3 FROM operations`)
	rollbackApply(t, db, after)
	checkOld()
	require(`SELECT NOT EXISTS(SELECT FROM local_model_groupings) AND NOT EXISTS(SELECT FROM local_credit_parts)`)
}
