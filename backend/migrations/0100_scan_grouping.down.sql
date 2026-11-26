UPDATE tracks SET position=sort_position WHERE position=0 AND sort_position IS NOT NULL;
DROP VIEW track_effective;
ALTER TABLE tracks DROP COLUMN sort_position;
CREATE VIEW track_effective AS
SELECT t.id, t.medium_id, t.position,
       COALESCE(ct.text_value, atitle.text_value, t.title) AS title,
       COALESCE(ca.text_value, aa.text_value, t.artist) AS artist,
       t.disc_number, t.source_root_id, t.relative_path, t.source_status, t.observed_at,
       t.source_kind, t.source_identity, t.duration_seconds, t.codec, t.sample_rate, t.channels, t.bitrate,
       t.cue_sheet_path, t.cue_parent_relative_path, t.cue_referenced_file, t.cue_index_frames,
       t.cue_end_frames, t.cue_isrc, t.bit_depth, t.content_fingerprint, t.source_size, t.source_modified_at,
       t.lyrics, t.lyrics_source, t.work_title, t.work_catalog, t.work_index,
       t.movement_number, t.movement_title, t.work_source, t.edit_revision
FROM tracks t
LEFT JOIN track_corrections ct ON ct.track_id=t.id AND ct.field='title'
LEFT JOIN track_corrections ca ON ca.track_id=t.id AND ca.field='artist'
LEFT JOIN track_auto_values atitle ON atitle.track_id=t.id AND atitle.field='title'
LEFT JOIN track_auto_values aa ON aa.track_id=t.id AND aa.field='artist';
DROP TABLE release_attachments;
ALTER TABLE releases DROP COLUMN scan_version_terms;
ALTER TABLE artist_profiles DROP COLUMN list_sort_key;
DROP FUNCTION catalog_list_sort_key(text);
UPDATE catalog_projection_state
SET target_generation=target_generation+1, build_id=gen_random_uuid(), status='preparing',
    catalog_epoch=catalog_epoch+1, manifest_sealed=false, live_updated=0, started_at=now(), updated_at=now()
WHERE capability='artists' AND EXISTS (SELECT FROM artist_profiles);
