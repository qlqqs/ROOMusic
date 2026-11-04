-- 收藏按类型保留真实外键；共用集合版本，未来艺人收藏可直接加入同一版本协议。
CREATE TABLE personal_favorite_state (
 user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 revision BIGINT NOT NULL DEFAULT 0 CHECK (revision BETWEEN 0 AND 9007199254740991)
);
CREATE TABLE favorite_tracks (
 user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 track_id UUID NOT NULL REFERENCES tracks(id) ON DELETE RESTRICT,
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (user_id,track_id)
);
CREATE INDEX favorite_tracks_user_created_idx ON favorite_tracks(user_id,created_at DESC,track_id);
CREATE INDEX favorite_tracks_track_idx ON favorite_tracks(track_id);
CREATE TABLE favorite_albums (
 user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 group_id UUID NOT NULL REFERENCES release_groups(id) ON DELETE RESTRICT,
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 saved_title TEXT NOT NULL DEFAULT '',
 saved_artist TEXT NOT NULL DEFAULT '',
 PRIMARY KEY (user_id,group_id)
);
CREATE INDEX favorite_albums_user_created_idx ON favorite_albums(user_id,created_at DESC,group_id);
CREATE INDEX favorite_albums_group_idx ON favorite_albums(group_id);
