package postgres

import (
	"database/sql"
	"testing"
)

const beforeArtistMediaMigration = `
INSERT INTO users(id,username,password_hash) VALUES ('f6800000-0000-4000-8000-000000000001','资料迁移用户','test-only');
INSERT INTO artists(id,initial_name,edit_revision) VALUES ('a6800000-0000-4000-8000-000000000001','资料迁移艺人',7);
INSERT INTO external_links(id,source,object_type,object_id,external_id,method,confidence)
 VALUES ('a6800000-0000-4000-8000-000000000002','musicbrainz','artist','a6800000-0000-4000-8000-000000000001',
 '10000000-0000-4000-8000-000000000001','artist_credit','certain');
INSERT INTO curation_work_states(subject,target_id,rule_version,input_digest,status)
 VALUES ('release','a6800000-0000-4000-8000-000000000003',1,repeat('a',64),'done');
INSERT INTO operations(id,actor_id,operation_type,target_type,target_id,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id)
 VALUES ('d6800000-0000-4000-8000-000000000001','f6800000-0000-4000-8000-000000000001','artist_rename','artist',
 'a6800000-0000-4000-8000-000000000001',gen_random_uuid(),'old-rename',6,7,'{}','{}','{}','fixture');
INSERT INTO operations_artist_targets(operation_id,artist_id)
 VALUES ('d6800000-0000-4000-8000-000000000001','a6800000-0000-4000-8000-000000000001');
INSERT INTO curation_suppressions(target_type,target_id,subject,value_hash,value,operation_id,created_by)
 VALUES ('artist','a6800000-0000-4000-8000-000000000001','external_link',repeat('c',64),'{}',NULL,'f6800000-0000-4000-8000-000000000001');`

const artistMediaRollbackFixture = `
UPDATE artists SET auto_revision=4 WHERE id='a6800000-0000-4000-8000-000000000001';
INSERT INTO operations(id,actor_id,operation_type,target_type,target_id,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id)
 SELECT id::uuid,'f6800000-0000-4000-8000-000000000001',kind,'artist','a6800000-0000-4000-8000-000000000001',
 gen_random_uuid(),kind,1,2,'{}','{}','{}','fixture'
 FROM (VALUES ('d6800000-0000-4000-8000-000000000002','artist_external_artwork'),
 ('d6800000-0000-4000-8000-000000000003','artist_external_bio'),
 ('d6800000-0000-4000-8000-000000000004','artist_external_bio')) AS f(id,kind);
INSERT INTO operations(id,actor_id,operation_type,target_type,target_id,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id,reverts_operation_id)
 VALUES ('d6800000-0000-4000-8000-000000000005','f6800000-0000-4000-8000-000000000001','revert','artist',
 'a6800000-0000-4000-8000-000000000001',gen_random_uuid(),'old-media-undo',3,4,'{}','{}','{}','fixture','d6800000-0000-4000-8000-000000000004');
INSERT INTO operations_artist_targets(operation_id,artist_id)
 SELECT id,target_id FROM operations WHERE id IN ('d6800000-0000-4000-8000-000000000002','d6800000-0000-4000-8000-000000000003',
 'd6800000-0000-4000-8000-000000000004','d6800000-0000-4000-8000-000000000005');
INSERT INTO artist_external_artworks(artist_id,storage_key,mime,width,height,source,source_url,author,license,license_url,operation_id)
 VALUES ('a6800000-0000-4000-8000-000000000001','fixture.png','image/png',960,960,'commons','https://commons.wikimedia.org/wiki/File:Fixture.png',
 '合成作者','CC BY 4.0','https://creativecommons.org/licenses/by/4.0/','d6800000-0000-4000-8000-000000000002');
INSERT INTO artist_external_bios(artist_id,text,lang,source,source_url,license,operation_id)
 VALUES ('a6800000-0000-4000-8000-000000000001','合成简介','zh','wikipedia','https://zh.wikipedia.org/wiki/Fixture','CC BY-SA 4.0',
 'd6800000-0000-4000-8000-000000000003');
INSERT INTO curation_suppressions(target_type,target_id,subject,value_hash,value,operation_id,created_by)
 VALUES ('artist','a6800000-0000-4000-8000-000000000001','artist_bio',repeat('b',64),'{"source":"theaudiodb","external_id":"1"}',
 'd6800000-0000-4000-8000-000000000005','f6800000-0000-4000-8000-000000000001');
INSERT INTO curation_work_states(subject,target_id,rule_version,input_digest,status)
 VALUES ('artist_media','a6800000-0000-4000-8000-000000000001',1,repeat('b',64),'done');`

const beforeArtistManualMediaMigration = `
UPDATE operations SET origin='{"source":"curation","provider_kind":"source","provider":"commons","basis":"MusicBrainz 艺人 ABCDEF12-0000-4000-8000-000000000001"}'
 WHERE id='d6800000-0000-4000-8000-000000000002';
INSERT INTO artists(id,initial_name) VALUES ('a7200000-0000-4000-8000-000000000001','没有对应的旧艺人');
INSERT INTO operations(id,actor_id,operation_type,target_type,target_id,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id)
 SELECT 'd7200000-0000-4000-8000-000000000001',actor_id,operation_type,target_type,'a7200000-0000-4000-8000-000000000001',
 gen_random_uuid(),fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id
 FROM operations WHERE id='d6800000-0000-4000-8000-000000000003';
INSERT INTO artist_external_bios(artist_id,text,lang,source,source_url,license,operation_id)
 SELECT 'a7200000-0000-4000-8000-000000000001',text,lang,source,source_url,license,'d7200000-0000-4000-8000-000000000001'
 FROM artist_external_bios WHERE artist_id='a6800000-0000-4000-8000-000000000001';`

const artistManualMediaRollbackFixture = `
INSERT INTO operations(id,actor_id,operation_type,target_type,target_id,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id)
 SELECT gen_random_uuid(),'f6800000-0000-4000-8000-000000000001',kind,'artist','a6800000-0000-4000-8000-000000000001',
 gen_random_uuid(),kind,7,8,'{}','{}','{}','fixture'
 FROM (VALUES ('artist_manual_image'),('artist_manual_bio'),('artist_link')) AS f(kind);
INSERT INTO artist_manual_artworks(artist_id,storage_key,mime,width,height,operation_id)
 SELECT target_id,'manual.png','image/png',2,3,id FROM operations WHERE operation_type='artist_manual_image';
INSERT INTO artist_manual_bios(artist_id,text,lang,operation_id)
 SELECT target_id,'合成人工简介','zh',id FROM operations WHERE operation_type='artist_manual_bio';
INSERT INTO artwork_retained(storage_key,operation_id)
 SELECT 'older.png',id FROM operations WHERE operation_type='artist_manual_image';
INSERT INTO external_links(id,source,object_type,object_id,external_id,method,confidence,operation_id)
 SELECT gen_random_uuid(),'wikidata','artist',target_id,'Q72','artist_manual','confirmed',id FROM operations WHERE operation_type='artist_link';
INSERT INTO operations(id,actor_id,operation_type,target_type,target_id,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id,reverts_operation_id)
 SELECT gen_random_uuid(),actor_id,'revert',target_type,target_id,gen_random_uuid(),'undo-link',8,9,'{}','{}','{}','fixture',id
 FROM operations WHERE operation_type='artist_link';`

func assertArtistManualMediaBackfill(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, tc := range []struct {
		table, operation, mbid string
	}{
		{"artist_external_artworks", "d6800000-0000-4000-8000-000000000002", "abcdef12-0000-4000-8000-000000000001"},
		{"artist_external_bios", "d6800000-0000-4000-8000-000000000003", "10000000-0000-4000-8000-000000000001"},
		{"artist_external_bios", "d7200000-0000-4000-8000-000000000001", ""},
	} {
		var source, id sql.NullString
		err := db.QueryRowContext(t.Context(), "SELECT basis_source,basis_id FROM "+tc.table+" WHERE operation_id=$1", tc.operation).Scan(&source, &id)
		if err != nil || source.Valid != (tc.mbid != "") || id.Valid != (tc.mbid != "") ||
			(source.Valid && source.String != "musicbrainz") || id.String != tc.mbid {
			t.Errorf("0072 回填 %s 的依据=(%+v,%+v)，期望 MusicBrainz %q（空串表示两列均 NULL），错误=%v", tc.operation, source, id, tc.mbid, err)
		}
	}
}
