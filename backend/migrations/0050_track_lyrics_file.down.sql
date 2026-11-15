-- 外置歌词是重扫可重建的派生事实；先清空，再恢复内嵌来源约束。
UPDATE tracks SET lyrics = NULL, lyrics_source = NULL WHERE lyrics_source = 'lrc_file';
ALTER TABLE tracks DROP CONSTRAINT tracks_lyrics_ck;
ALTER TABLE tracks ADD CONSTRAINT tracks_lyrics_ck
    CHECK (
        (lyrics IS NULL AND lyrics_source IS NULL)
        OR (lyrics IS NOT NULL AND lyrics <> '' AND OCTET_LENGTH(lyrics) <= 65536
            AND lyrics_source IS NOT NULL AND lyrics_source IN ('vorbis_lyrics', 'vorbis_unsyncedlyrics', 'mp4_lyr', 'id3_uslt'))
    );
