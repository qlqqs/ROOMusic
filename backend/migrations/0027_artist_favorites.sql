-- 原锚点保留用户意图；解析身份是可重建投影，合并不丢失任何一方的收藏。
CREATE TABLE artist_favorites (
 user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 artist_id UUID NOT NULL REFERENCES artists(id) ON DELETE RESTRICT,
 resolved_artist_id UUID NOT NULL REFERENCES artists(id) ON DELETE RESTRICT,
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (user_id,artist_id)
);
CREATE INDEX artist_favorites_user_resolved_idx ON artist_favorites(user_id,resolved_artist_id,created_at DESC,artist_id);
CREATE INDEX artist_favorites_artist_user_idx ON artist_favorites(artist_id,user_id);
CREATE INDEX artist_favorites_resolved_idx ON artist_favorites(resolved_artist_id);
