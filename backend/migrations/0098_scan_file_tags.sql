ALTER TABLE tracks ADD COLUMN scan_tags jsonb;

CREATE VIEW curation_eligible_releases AS
SELECT r.id FROM releases r
WHERE EXISTS (
    SELECT FROM media m JOIN tracks t ON t.medium_id = m.id
    WHERE m.release_id = r.id AND t.source_status = 'present'
) AND NOT EXISTS (
    SELECT FROM media m JOIN tracks t ON t.medium_id = m.id
    WHERE m.release_id = r.id AND t.source_status = 'present'
      AND (t.scan_tags @> '{"podcast": true}' OR t.scan_tags @> '{"audiobook": true}')
);
