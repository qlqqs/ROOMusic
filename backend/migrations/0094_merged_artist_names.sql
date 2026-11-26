ALTER TABLE external_links ADD COLUMN artist_primary_name text
    CHECK (artist_primary_name IS NULL OR (object_type='artist' AND length(artist_primary_name)>0));

-- 只在已合并的身份中采用外部主名；保留产生该名字的实际对应，供补齐时复核。
CREATE VIEW artist_merged_primary_links AS
SELECT families.artist_id, chosen.id AS link_id, chosen.source, chosen.external_id, chosen.artist_primary_name
FROM (SELECT DISTINCT redirect_artist_id AS artist_id FROM artist_overrides WHERE redirect_artist_id IS NOT NULL) families
CROSS JOIN LATERAL (
    SELECT l.* FROM (
        SELECT families.artist_id AS member_id
        UNION ALL SELECT o.artist_id FROM artist_overrides o WHERE o.redirect_artist_id=families.artist_id
    ) members JOIN external_links l ON l.object_id=members.member_id
    WHERE l.object_type='artist' AND l.source IN ('musicbrainz','wikidata')
    ORDER BY (l.method IN ('artist_manual','artist_model')) DESC,
        (l.artist_primary_name IS NOT NULL) DESC, (l.source='musicbrainz') DESC,
        (l.object_id=families.artist_id) DESC,l.created_at DESC,l.id
    LIMIT 1
) chosen;
