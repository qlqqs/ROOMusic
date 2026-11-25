DROP VIEW curation_stable_releases;
DROP VIEW curation_scan_files;
DROP INDEX releases_curation_confirmation_due;
ALTER TABLE releases
    DROP COLUMN curation_member_signature,
    DROP COLUMN curation_changed_at,
    DROP COLUMN curation_confirmed;
