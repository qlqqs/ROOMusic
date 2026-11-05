-- 艺人身份不可物理删除；事实、人工意图与可重建投影分离。
ALTER TABLE catalog_projection_state DROP CONSTRAINT catalog_projection_state_capability_check;
ALTER TABLE catalog_projection_state ADD CONSTRAINT catalog_projection_state_capability_check CHECK (capability IN ('search','artists'));
INSERT INTO catalog_projection_state(capability,target_generation,textnorm_version,search_projection_version,build_id,status,catalog_epoch)
VALUES ('artists',1,1,1,gen_random_uuid(),'preparing',1);
CREATE TABLE artists (
 id UUID PRIMARY KEY, initial_name TEXT NOT NULL CHECK (octet_length(initial_name) BETWEEN 1 AND 4096),
 edit_revision BIGINT NOT NULL DEFAULT 1 CHECK (edit_revision BETWEEN 1 AND 9007199254740991), created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE artist_name_keys (
 id UUID PRIMARY KEY, normalization_version BIGINT NOT NULL CHECK (normalization_version>0),
 normalized_key TEXT NOT NULL CHECK (octet_length(normalized_key) BETWEEN 1 AND 65536),
 key_hash BYTEA NOT NULL CHECK (octet_length(key_hash)=32), initial_artist_id UUID NOT NULL REFERENCES artists(id) ON DELETE RESTRICT,
 UNIQUE(normalization_version,key_hash)
);
CREATE INDEX artist_name_keys_initial_idx ON artist_name_keys(initial_artist_id);
CREATE TABLE artist_names (
 id UUID PRIMARY KEY, key_id UUID NOT NULL REFERENCES artist_name_keys(id) ON DELETE RESTRICT,
 spelling TEXT NOT NULL CHECK (octet_length(spelling) BETWEEN 1 AND 4096), spelling_hash BYTEA NOT NULL CHECK (octet_length(spelling_hash)=32),
 origin TEXT NOT NULL CHECK (origin IN ('scan','correction','seed')), created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), UNIQUE(key_id,spelling_hash)
);
CREATE TABLE artist_credits (
 id UUID PRIMARY KEY, release_id UUID REFERENCES releases(id) ON DELETE CASCADE, track_id UUID REFERENCES tracks(id) ON DELETE CASCADE,
 role TEXT NOT NULL, name_id UUID NOT NULL REFERENCES artist_names(id) ON DELETE RESTRICT,
 position INTEGER NOT NULL CHECK(position>0), layer TEXT NOT NULL CHECK(layer IN ('scan','correction')),
 CHECK(num_nonnulls(release_id,track_id)=1),
 CHECK((release_id IS NOT NULL AND role IN ('release_artist','album_artist')) OR
 (track_id IS NOT NULL AND role IN ('track_artist','composer','conductor','performer','producer'))),
 UNIQUE NULLS NOT DISTINCT(release_id,track_id,role,name_id,layer)
);
CREATE INDEX artist_credits_track_idx ON artist_credits(track_id);
CREATE INDEX artist_credits_name_idx ON artist_credits(name_id);
CREATE TABLE artist_name_bindings (
 key_id UUID PRIMARY KEY REFERENCES artist_name_keys(id) ON DELETE RESTRICT,
 artist_id UUID NOT NULL REFERENCES artists(id) ON DELETE RESTRICT,
 operation_id UUID NOT NULL REFERENCES operations(id) DEFERRABLE INITIALLY DEFERRED
);
CREATE INDEX artist_name_bindings_artist_idx ON artist_name_bindings(artist_id);
CREATE TABLE artist_assignment_overrides (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(), track_id UUID REFERENCES tracks(id) ON DELETE RESTRICT,
 release_id UUID REFERENCES releases(id) ON DELETE RESTRICT, key_id UUID NOT NULL REFERENCES artist_name_keys(id) ON DELETE RESTRICT,
 artist_id UUID NOT NULL REFERENCES artists(id) ON DELETE RESTRICT,
 operation_id UUID NOT NULL REFERENCES operations(id) DEFERRABLE INITIALLY DEFERRED,
 CHECK(num_nonnulls(track_id,release_id)=1), UNIQUE NULLS NOT DISTINCT(track_id,release_id,key_id)
);
CREATE UNIQUE INDEX artist_assignment_track_key_idx ON artist_assignment_overrides(track_id,key_id) WHERE track_id IS NOT NULL;
CREATE UNIQUE INDEX artist_assignment_release_key_idx ON artist_assignment_overrides(release_id,key_id) WHERE release_id IS NOT NULL;
CREATE INDEX artist_assignment_artist_idx ON artist_assignment_overrides(artist_id);
CREATE TABLE artist_overrides (
 artist_id UUID PRIMARY KEY REFERENCES artists(id) ON DELETE RESTRICT,
 display_name TEXT CHECK (display_name IS NULL OR octet_length(display_name) BETWEEN 1 AND 4096),
 redirect_artist_id UUID REFERENCES artists(id) ON DELETE RESTRICT,
 name_operation_id UUID REFERENCES operations(id) DEFERRABLE INITIALLY DEFERRED,
 redirect_operation_id UUID REFERENCES operations(id) DEFERRABLE INITIALLY DEFERRED,
 CHECK(redirect_artist_id IS NULL OR redirect_artist_id<>artist_id)
);
CREATE INDEX artist_overrides_redirect_idx ON artist_overrides(redirect_artist_id);
CREATE TABLE artist_effective_credits (
 credit_id UUID PRIMARY KEY REFERENCES artist_credits(id) ON DELETE CASCADE,
 release_id UUID REFERENCES releases(id) ON DELETE CASCADE, track_id UUID REFERENCES tracks(id) ON DELETE CASCADE,
 role TEXT NOT NULL, name_id UUID NOT NULL REFERENCES artist_names(id) ON DELETE RESTRICT,
 artist_id UUID NOT NULL REFERENCES artists(id) ON DELETE RESTRICT, position INTEGER NOT NULL CHECK(position>0),
 CHECK(num_nonnulls(release_id,track_id)=1)
);
CREATE INDEX artist_effective_artist_idx ON artist_effective_credits(artist_id,role,release_id,track_id);
CREATE INDEX artist_effective_release_idx ON artist_effective_credits(release_id);
CREATE INDEX artist_effective_track_idx ON artist_effective_credits(track_id);
CREATE INDEX artist_effective_name_idx ON artist_effective_credits(name_id);
CREATE TABLE artist_profiles (
 artist_id UUID PRIMARY KEY REFERENCES artists(id) ON DELETE RESTRICT,
 auto_display_name TEXT NOT NULL, effective_display_name TEXT NOT NULL,
 sort_key TEXT NOT NULL, initial_group TEXT NOT NULL CHECK(initial_group ~ '^[A-Z]$' OR initial_group IN ('kana','#')),
 version BIGINT NOT NULL CHECK(version>0)
);
CREATE INDEX artist_profiles_sort_idx ON artist_profiles(initial_group,(left(sort_key,128) COLLATE "C"),artist_id);
CREATE TABLE artist_search_names (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(), artist_id UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
 name_id UUID REFERENCES artist_names(id) ON DELETE CASCADE,
 source_artist_id UUID REFERENCES artists(id) ON DELETE RESTRICT,
 original_value TEXT NOT NULL, normalized_value TEXT NOT NULL CHECK(octet_length(normalized_value) BETWEEN 1 AND 65536),
 normalization_version BIGINT NOT NULL, projection_generation BIGINT NOT NULL,
 short_grams TEXT[] NOT NULL, UNIQUE NULLS NOT DISTINCT(artist_id,name_id,source_artist_id)
);
CREATE INDEX artist_search_names_artist_idx ON artist_search_names(artist_id);
CREATE INDEX artist_search_names_trgm_idx ON artist_search_names USING gin(normalized_value public.gin_trgm_ops);
CREATE INDEX artist_search_names_short_idx ON artist_search_names USING gin(short_grams);
CREATE INDEX artist_search_names_exact_idx ON artist_search_names(md5(normalized_value));
CREATE TABLE artist_tag_evidence (
 track_id UUID NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
 role TEXT NOT NULL CHECK(role IN ('track_artist','album_artist','composer','conductor','performer','producer')),
 tag_key TEXT NOT NULL CHECK(octet_length(tag_key) BETWEEN 1 AND 128), occurrence_index INTEGER NOT NULL CHECK(occurrence_index BETWEEN 0 AND 99),
 raw_value TEXT NOT NULL CHECK(octet_length(raw_value) BETWEEN 1 AND 4096), source_kind TEXT NOT NULL,
 PRIMARY KEY(track_id,role,tag_key,occurrence_index)
);
CREATE TABLE artist_candidate_signals (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(), left_key_id UUID NOT NULL REFERENCES artist_name_keys(id),
 right_key_id UUID NOT NULL REFERENCES artist_name_keys(id), kind TEXT NOT NULL CHECK(kind IN ('same_track_role','album_track_one_to_one')),
 release_id UUID NOT NULL REFERENCES releases(id) ON DELETE CASCADE, track_id UUID REFERENCES tracks(id) ON DELETE CASCADE,
 role TEXT, left_name TEXT NOT NULL, right_name TEXT NOT NULL,
 CHECK(left_key_id<right_key_id), UNIQUE NULLS NOT DISTINCT(left_key_id,right_key_id,kind,release_id,track_id,role)
);
CREATE INDEX artist_candidate_signals_release_idx ON artist_candidate_signals(release_id);
CREATE TABLE artist_candidate_ignores (
 left_key_id UUID NOT NULL REFERENCES artist_name_keys(id), right_key_id UUID NOT NULL REFERENCES artist_name_keys(id),
 operation_id UUID NOT NULL REFERENCES operations(id) DEFERRABLE INITIALLY DEFERRED,
 CHECK(left_key_id<right_key_id), PRIMARY KEY(left_key_id,right_key_id)
);
-- 发行级完成标记由CATP内实时写者与回填共同维护；无姓名也必须标完成。
CREATE TABLE artist_projection_releases (
 release_id UUID PRIMARY KEY REFERENCES releases(id) ON DELETE CASCADE,
 applied_generation BIGINT NOT NULL CHECK(applied_generation>0)
);
ALTER TABLE operations DROP CONSTRAINT operations_type_ck;
ALTER TABLE operations ADD CONSTRAINT operations_type_ck CHECK(operation_type IN
 ('release_correction','release_group_change','release_group_default','release_issue_review','release_claim','revert',
 'artist_merge','artist_split','artist_rename','artist_candidate_ignore'));
ALTER TABLE operations DROP CONSTRAINT operations_target_type_ck;
ALTER TABLE operations ADD CONSTRAINT operations_target_type_ck CHECK(target_type IN ('release','release_group','artist'));
CREATE TABLE operations_artist_targets (
 operation_id UUID NOT NULL REFERENCES operations(id) ON DELETE CASCADE,
 artist_id UUID NOT NULL REFERENCES artists(id) ON DELETE RESTRICT, PRIMARY KEY(operation_id,artist_id)
);
CREATE INDEX operations_artist_targets_artist_idx ON operations_artist_targets(artist_id,operation_id);

-- 固定种子只建立初始路由，不写人工绑定；重扫不会恢复被拆开的别名。
INSERT INTO artists(id,initial_name) VALUES('a0000000-0000-4000-8000-000000000001','周杰伦');
INSERT INTO artist_name_keys(id,normalization_version,normalized_key,key_hash,initial_artist_id) VALUES('a1000000-0000-4000-8000-000000000001',1,'周杰伦',decode('d1d51d7a7c5cdc1daaeca7c0e1600772736035fdc67d85e27c2d0be3bb230a2e','hex'),'a0000000-0000-4000-8000-000000000001');
INSERT INTO artist_name_keys(id,normalization_version,normalized_key,key_hash,initial_artist_id) VALUES('a1000000-0000-4000-8000-000000000002',1,'jay chou',decode('5597aaaaae9aa19932244fcd723e05fc1ee09f0cf1852b93d71e57d24eff2f23','hex'),'a0000000-0000-4000-8000-000000000001');
INSERT INTO artist_names(id,key_id,spelling,spelling_hash,origin) VALUES('a2000000-0000-4000-8000-000000000001','a1000000-0000-4000-8000-000000000001','周杰伦',decode('d1d51d7a7c5cdc1daaeca7c0e1600772736035fdc67d85e27c2d0be3bb230a2e','hex'),'seed');
INSERT INTO artist_names(id,key_id,spelling,spelling_hash,origin) VALUES('a2000000-0000-4000-8000-000000000002','a1000000-0000-4000-8000-000000000001','周杰倫',decode('521110509a726ec863fb4a478ffbeb44140e381210c1180203443abe48c91a9a','hex'),'seed');
INSERT INTO artist_names(id,key_id,spelling,spelling_hash,origin) VALUES('a2000000-0000-4000-8000-000000000003','a1000000-0000-4000-8000-000000000002','Jay Chou',decode('a27d0557066c1d4d1e4cce21a315eb8b3914c8337782dc5c4a0ad1c674a01f59','hex'),'seed');
