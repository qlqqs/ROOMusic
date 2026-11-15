-- 外置歌词沿用歌词原文约束，新增文件来源键。
ALTER TABLE tracks DROP CONSTRAINT tracks_lyrics_ck;
ALTER TABLE tracks ADD CONSTRAINT tracks_lyrics_ck
    CHECK (
        (lyrics IS NULL AND lyrics_source IS NULL)
        OR (lyrics IS NOT NULL AND lyrics <> '' AND OCTET_LENGTH(lyrics) <= 65536
            AND lyrics_source IS NOT NULL AND lyrics_source IN ('vorbis_lyrics', 'vorbis_unsyncedlyrics', 'mp4_lyr', 'id3_uslt', 'lrc_file'))
    );
