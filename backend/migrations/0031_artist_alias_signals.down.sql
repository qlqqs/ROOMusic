DROP INDEX artist_name_keys_bracket_idx;
DROP TABLE artist_alias_signals;
ALTER TABLE artist_candidate_signals DROP CONSTRAINT artist_candidate_signals_kind_check;
ALTER TABLE artist_candidate_signals ADD CONSTRAINT artist_candidate_signals_kind_check CHECK(kind IN ('same_track_role','album_track_one_to_one'));
-- 旧版本发现投影版本不一致后会重建已清理的同曲同身份信号；忽略记录不变。
