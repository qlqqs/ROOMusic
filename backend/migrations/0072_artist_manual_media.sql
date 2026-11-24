CREATE TABLE artist_manual_artworks (
    artist_id UUID PRIMARY KEY REFERENCES artists(id) ON DELETE CASCADE,
    storage_key TEXT NOT NULL,
    mime TEXT NOT NULL CHECK (mime IN ('image/jpeg','image/png','image/webp')),
    width INTEGER NOT NULL CHECK (width > 0),
    height INTEGER NOT NULL CHECK (height > 0),
    operation_id UUID NOT NULL REFERENCES operations(id) DEFERRABLE INITIALLY DEFERRED,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX artist_manual_artworks_storage_key_idx ON artist_manual_artworks(storage_key);

CREATE TABLE artist_manual_bios (
    artist_id UUID PRIMARY KEY REFERENCES artists(id) ON DELETE CASCADE,
    text TEXT NOT NULL CHECK (char_length(text) BETWEEN 1 AND 4000),
    lang TEXT NOT NULL CHECK (lang IN ('zh','en')),
    operation_id UUID NOT NULL REFERENCES operations(id) DEFERRABLE INITIALLY DEFERRED,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE artwork_retained (
    storage_key TEXT NOT NULL,
    operation_id UUID NOT NULL REFERENCES operations(id) DEFERRABLE INITIALLY DEFERRED,
    PRIMARY KEY (storage_key, operation_id)
);

ALTER TABLE artist_external_artworks
    ADD COLUMN basis_source TEXT CHECK (basis_source IN ('musicbrainz','wikidata')),
    ADD COLUMN basis_id TEXT,
    ADD CHECK ((basis_source IS NULL) = (basis_id IS NULL));
ALTER TABLE artist_external_bios
    ADD COLUMN basis_source TEXT CHECK (basis_source IN ('musicbrainz','wikidata')),
    ADD COLUMN basis_id TEXT,
    ADD CHECK ((basis_source IS NULL) = (basis_id IS NULL));

-- 旧操作的指纹是摘要；原始 MBID 在 origin.basis。缺依据时只接受家族内唯一的 MBID。
WITH family AS (
    SELECT a.id,COALESCE(o.redirect_artist_id,a.id) AS canonical_id
    FROM artists a LEFT JOIN artist_overrides o ON o.artist_id=a.id
), unique_links AS (
    SELECT f.canonical_id,min(lower(l.external_id)) AS mbid
    FROM external_links l JOIN family f ON f.id=l.object_id
    WHERE l.source='musicbrainz' AND l.object_type='artist'
      AND l.external_id ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
    GROUP BY f.canonical_id HAVING count(DISTINCT lower(l.external_id))=1
), basis AS (
    SELECT op.id,COALESCE(lower(substring(op.origin->>'basis' FROM
        '^MusicBrainz 艺人 ([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})$')),l.mbid) AS mbid
    FROM operations op JOIN family f ON f.id=op.target_id
    LEFT JOIN unique_links l USING(canonical_id)
    WHERE op.operation_type IN ('artist_external_artwork','artist_external_bio')
), images AS (
    UPDATE artist_external_artworks m SET basis_source='musicbrainz',basis_id=b.mbid
    FROM basis b WHERE b.id=m.operation_id AND b.mbid IS NOT NULL
)
UPDATE artist_external_bios m SET basis_source='musicbrainz',basis_id=b.mbid
FROM basis b WHERE b.id=m.operation_id AND b.mbid IS NOT NULL;

ALTER TABLE operations DROP CONSTRAINT operations_type_ck;
ALTER TABLE operations ADD CONSTRAINT operations_type_ck CHECK (operation_type IN
    ('release_correction','release_auto_correction','track_correction','track_auto_correction','release_external_artwork','track_external_lyrics','external_link',
     'release_group_change','release_group_default','release_issue_review','release_claim','revert',
     'artist_merge','artist_split','artist_rename','artist_candidate_ignore','release_merge','release_auto_merge','artist_external_artwork','artist_external_bio',
     'artist_manual_image','artist_manual_bio','artist_link'));
