ALTER TABLE releases
    ADD COLUMN curation_member_signature bytea,
    ADD COLUMN curation_changed_at timestamptz,
    ADD COLUMN curation_confirmed boolean NOT NULL DEFAULT true;

-- 归并后的发行按实际展示的成员判断，CUE 多音轨共用一个父文件。
CREATE VIEW curation_scan_files AS
SELECT DISTINCT m.release_id, t.source_root_id AS root_id, f.path, f.size
FROM tracks t JOIN media m ON m.id=t.medium_id
CROSS JOIN LATERAL (VALUES
    (COALESCE(t.cue_parent_relative_path,t.relative_path),t.source_size),
    (t.cue_sheet_path,NULL::bigint)
) AS f(path,size)
WHERE t.source_status='present' AND f.path IS NOT NULL;

-- 存量用原有扫描大小建立基线，直接可整理。
UPDATE releases r SET curation_member_signature=s.signature
FROM (SELECT release_id,
    sha256(convert_to(jsonb_agg(jsonb_build_array(root_id,path,size)
        ORDER BY root_id,path COLLATE "C",size)::text,'UTF8')) AS signature
    FROM curation_scan_files GROUP BY release_id) s
WHERE r.id=s.release_id;

CREATE VIEW curation_stable_releases AS
SELECT id FROM releases WHERE curation_confirmed
AND (curation_changed_at IS NULL OR curation_changed_at<=now()-interval '60 seconds');

CREATE INDEX releases_curation_confirmation_due ON releases(curation_changed_at)
WHERE NOT curation_confirmed;
