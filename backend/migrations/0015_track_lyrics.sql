-- 曲目内嵌歌词：扫描时从音频标签提取规范化后的原文，lyrics_source 记录取自哪种标签。
-- 两列同时有值或同时为空；歌词不超过 64 KiB。该迁移只增加列与约束，不改写已有数据，也不修改音乐源文件。
-- 约束里显式写 IS NOT NULL：CHECK 在结果为未知（NULL）时会放行，只靠 IN 判断挡不住“有歌词、没有来源”。
ALTER TABLE tracks ADD COLUMN IF NOT EXISTS lyrics TEXT;
ALTER TABLE tracks ADD COLUMN IF NOT EXISTS lyrics_source TEXT;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'tracks_lyrics_ck'
          AND conrelid = 'tracks'::regclass
    ) THEN
        ALTER TABLE tracks ADD CONSTRAINT tracks_lyrics_ck
            CHECK (
                (lyrics IS NULL AND lyrics_source IS NULL)
                OR (lyrics IS NOT NULL AND lyrics <> '' AND OCTET_LENGTH(lyrics) <= 65536
                    AND lyrics_source IS NOT NULL AND lyrics_source IN ('vorbis_lyrics', 'vorbis_unsyncedlyrics', 'mp4_lyr', 'id3_uslt'))
            );
    END IF;
END
$$;
