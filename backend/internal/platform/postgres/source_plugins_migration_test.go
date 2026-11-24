package postgres

const beforeSourcePluginMigration = `
INSERT INTO users(id,username,password_hash) VALUES('f7100000-0000-4000-8000-000000000001','plugin-migration','synthetic');
INSERT INTO artists(id,initial_name) VALUES
 ('a7100000-0000-4000-8000-000000000001','插件资料艺人'),
 ('a7100000-0000-4000-8000-000000000002','内置资料艺人');
INSERT INTO operations(id,actor_id,operation_type,target_type,target_id,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id)
 VALUES('d7100000-0000-4000-8000-000000000002','f7100000-0000-4000-8000-000000000001','artist_external_bio','artist',
 'a7100000-0000-4000-8000-000000000002',gen_random_uuid(),'builtin-bio',1,2,'{}','{}','{}','fixture');
INSERT INTO artist_external_bios(artist_id,text,lang,source,source_url,operation_id)
 VALUES('a7100000-0000-4000-8000-000000000002','合成内置简介','zh','wikipedia','https://fixture.invalid/builtin','d7100000-0000-4000-8000-000000000002');
INSERT INTO curation_source_settings(source,enabled,updated_by)
 VALUES('theaudiodb',true,'f7100000-0000-4000-8000-000000000001');
INSERT INTO curation_source_keys(source,name,ciphertext,last_four,updated_by)
 VALUES('theaudiodb','api_key','builtin-fixture','test','f7100000-0000-4000-8000-000000000001');`

const sourcePluginRollbackFixture = `
INSERT INTO source_plugins(id,manifest,capabilities,sha256,wasm_hash,manifest_hash)
 VALUES('fixture','{"id":"fixture"}','["artist_bio"]',repeat('a',64),repeat('b',64),repeat('c',64));
INSERT INTO curation_source_settings(source,enabled,updated_by)
 VALUES('plugin:fixture',true,'f7100000-0000-4000-8000-000000000001');
INSERT INTO curation_source_keys(source,name,ciphertext,last_four,updated_by)
 VALUES('plugin:fixture','api_key','plugin-fixture','test','f7100000-0000-4000-8000-000000000001');
INSERT INTO operations(id,actor_id,operation_type,target_type,target_id,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id,origin)
 VALUES('d7100000-0000-4000-8000-000000000001','f7100000-0000-4000-8000-000000000001','artist_external_bio','artist',
 'a7100000-0000-4000-8000-000000000001',gen_random_uuid(),'plugin-bio',1,2,'{}','{}','{}','fixture','{"source":"curation","provider":"plugin:fixture"}');
INSERT INTO artist_external_bios(artist_id,text,lang,source,source_url,operation_id)
 VALUES('a7100000-0000-4000-8000-000000000001','合成插件简介','zh','plugin:fixture','https://fixture.invalid/plugin','d7100000-0000-4000-8000-000000000001');`
