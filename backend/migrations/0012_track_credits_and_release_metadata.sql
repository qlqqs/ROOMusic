-- 补齐扫描器已经能够确定的发行字段和逐轨署名；只扩展当前目录模型，
-- 不读取或修改任何音乐源文件。
ALTER TABLE releases ADD COLUMN IF NOT EXISTS edition TEXT;
ALTER TABLE releases ADD COLUMN IF NOT EXISTS label TEXT;
ALTER TABLE releases ADD COLUMN IF NOT EXISTS barcode TEXT;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'releases_edition_text_ck'
          AND conrelid = 'releases'::regclass
    ) THEN
        ALTER TABLE releases ADD CONSTRAINT releases_edition_text_ck
            CHECK (edition IS NULL OR (BTRIM(edition) <> '' AND OCTET_LENGTH(edition) <= 4096));
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'releases_label_text_ck'
          AND conrelid = 'releases'::regclass
    ) THEN
        ALTER TABLE releases ADD CONSTRAINT releases_label_text_ck
            CHECK (label IS NULL OR (BTRIM(label) <> '' AND OCTET_LENGTH(label) <= 4096));
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'releases_barcode_text_ck'
          AND conrelid = 'releases'::regclass
    ) THEN
        ALTER TABLE releases ADD CONSTRAINT releases_barcode_text_ck
            CHECK (barcode IS NULL OR (BTRIM(barcode) <> '' AND OCTET_LENGTH(barcode) <= 4096));
    END IF;
END
$$;

CREATE TABLE IF NOT EXISTS track_credits (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    track_id UUID NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('composer', 'conductor', 'performer', 'producer')),
    name TEXT NOT NULL CHECK (BTRIM(name) <> '' AND OCTET_LENGTH(name) <= 4096),
    position INTEGER NOT NULL CHECK (position > 0),
    UNIQUE(track_id, role, name)
);

CREATE INDEX IF NOT EXISTS track_credits_track_position_index
    ON track_credits(track_id, position, role, name);
