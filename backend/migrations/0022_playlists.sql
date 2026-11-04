-- 歌单私有数据，不接共享治理。撤销允许暂时超出普通加入的 10,000 首限额。
CREATE TABLE personal_playlists (
 id UUID PRIMARY KEY,
 user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100 AND name !~ '^[[:space:]]*$' AND name !~ '[[:cntrl:]]'),
 revision BIGINT NOT NULL DEFAULT 1 CHECK (revision BETWEEN 1 AND 9007199254740991),
 track_count BIGINT NOT NULL DEFAULT 0 CHECK (track_count BETWEEN 0 AND 9007199254740991),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(user_id,id)
);
CREATE INDEX personal_playlists_user_updated_idx ON personal_playlists(user_id,updated_at DESC,id);
CREATE TABLE personal_playlist_items (
 id UUID PRIMARY KEY,
 user_id UUID NOT NULL,
 playlist_id UUID NOT NULL,
 track_id UUID NOT NULL REFERENCES tracks(id) ON DELETE RESTRICT,
 order_key BIGINT NOT NULL,
 added_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(user_id,playlist_id) REFERENCES personal_playlists(user_id,id) ON DELETE CASCADE,
 UNIQUE(user_id,playlist_id,track_id),
 CONSTRAINT personal_playlist_items_order_uq UNIQUE(user_id,playlist_id,order_key) DEFERRABLE INITIALLY IMMEDIATE
);
CREATE INDEX personal_playlist_items_user_track_idx ON personal_playlist_items(user_id,track_id,playlist_id);
CREATE INDEX personal_playlist_items_track_idx ON personal_playlist_items(track_id);
-- 收据不外键关联歌单：删除后必须仍能重放成功响应且不能重新创建。
CREATE TABLE personal_playlist_requests (
 user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 idempotency_key UUID NOT NULL,
 command TEXT NOT NULL CHECK(command IN ('create','rename','delete','add_items','remove_item','restore_item','move_item')),
 playlist_id UUID NOT NULL,
 request_hash BYTEA NOT NULL CHECK(octet_length(request_hash)=32),
 response_version INTEGER NOT NULL DEFAULT 1 CHECK(response_version=1),
 response_status INTEGER NOT NULL CHECK(response_status IN (200,201)),
 response_body JSONB NOT NULL CHECK(jsonb_typeof(response_body)='object' AND octet_length(response_body::text)<=65536),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(user_id,idempotency_key)
);
CREATE TABLE personal_playlist_undo (
 user_id UUID NOT NULL,
 playlist_id UUID NOT NULL,
 token UUID NOT NULL,
 item_id UUID NOT NULL,
 track_id UUID NOT NULL REFERENCES tracks(id) ON DELETE RESTRICT,
 order_key BIGINT NOT NULL,
 added_at TIMESTAMPTZ NOT NULL,
 original_position BIGINT NOT NULL CHECK(original_position BETWEEN 1 AND 9007199254740991),
 previous_track_id UUID REFERENCES tracks(id) ON DELETE RESTRICT,
 removal_revision BIGINT NOT NULL CHECK(removal_revision BETWEEN 1 AND 9007199254740991),
 removed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 restore_request_key UUID,
 PRIMARY KEY(user_id,playlist_id,token),
 UNIQUE(user_id,token),
 FOREIGN KEY(user_id,playlist_id) REFERENCES personal_playlists(user_id,id) ON DELETE CASCADE,
 FOREIGN KEY(user_id,restore_request_key) REFERENCES personal_playlist_requests(user_id,idempotency_key) DEFERRABLE INITIALLY DEFERRED
);
CREATE INDEX personal_playlist_undo_track_idx ON personal_playlist_undo(track_id);
CREATE INDEX personal_playlist_undo_previous_idx ON personal_playlist_undo(previous_track_id) WHERE previous_track_id IS NOT NULL;
