-- 同曲同身份的多值署名不再当作重复艺人信号；已忽略的名字对保持不动。
DELETE FROM artist_candidate_signals WHERE kind='same_track_role';
ALTER TABLE artist_candidate_signals DROP CONSTRAINT artist_candidate_signals_kind_check;
ALTER TABLE artist_candidate_signals ADD CONSTRAINT artist_candidate_signals_kind_check CHECK(kind='album_track_one_to_one');

-- 一行记录带括号写法所在发行与单独写法所在发行的组合；任一端刷新时重算。
CREATE TABLE artist_alias_signals (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 left_key_id UUID NOT NULL REFERENCES artist_name_keys(id),
 right_key_id UUID NOT NULL REFERENCES artist_name_keys(id),
 bracket_key_id UUID NOT NULL REFERENCES artist_name_keys(id),
 alias_part TEXT NOT NULL CHECK(alias_part IN ('inner','outer')),
 release_id UUID NOT NULL REFERENCES releases(id) ON DELETE CASCADE,
 alias_release_id UUID NOT NULL REFERENCES releases(id) ON DELETE CASCADE,
 left_name TEXT NOT NULL, right_name TEXT NOT NULL,
 CHECK(left_key_id<right_key_id),
 CHECK(bracket_key_id IN (left_key_id,right_key_id)),
 UNIQUE(left_key_id,right_key_id,release_id,alias_release_id)
);
CREATE INDEX artist_alias_signals_release_idx ON artist_alias_signals(release_id);
CREATE INDEX artist_alias_signals_alias_release_idx ON artist_alias_signals(alias_release_id);
-- 刷新时只读取以右括号结尾的名字键。
CREATE INDEX artist_name_keys_bracket_idx ON artist_name_keys(normalization_version) WHERE normalized_key LIKE '%)';
