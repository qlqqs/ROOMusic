DROP INDEX curation_unmatched_due_idx;
UPDATE curation_work_states SET status=CASE WHEN status='conflict' THEN 'uncertain'
    WHEN result->>'reason'='candidates_revoked' THEN 'no_usable_candidates' ELSE 'unlisted' END
WHERE subject='release' AND status IN ('unmatched','conflict');
UPDATE curation_work_states SET requery_match_version=NULL
WHERE subject='release' AND requery_match_version=105;
ALTER TABLE curation_work_states DROP CONSTRAINT curation_work_states_status_check,
    ADD CONSTRAINT curation_work_states_status_check CHECK (status IN
    ('pending','matched','uncertain','no_match','try_later','done','unlisted','no_usable_candidates'));
CREATE INDEX curation_unlisted_due_idx ON curation_work_states(next_attempt_at)
    WHERE status='unlisted' AND next_attempt_at IS NOT NULL;
-- 旧版按当前 scan 署名恢复有效身份；资料与候选由已有的目录重建器发布。
CREATE TEMP TABLE rollback_credit_roles ON COMMIT DROP AS
SELECT DISTINCT release_id,track_id,role FROM release_match_credit_parts;
DELETE FROM release_match_credit_parts;
DELETE FROM artist_credits f USING rollback_credit_roles p
WHERE f.layer='auto' AND f.role=p.role AND
      (p.track_id=f.track_id OR p.track_id IS NULL AND p.release_id=f.release_id);
INSERT INTO artist_effective_credits(credit_id,release_id,track_id,role,name_id,artist_id,position)
SELECT f.id,f.release_id,f.track_id,f.role,f.name_id,coalesce(o.redirect_artist_id,route.artist_id),f.position
FROM rollback_credit_roles p JOIN artist_credits f ON f.role=p.role AND f.layer='scan' AND
      (p.track_id=f.track_id OR p.track_id IS NULL AND p.release_id=f.release_id)
JOIN artist_names n ON n.id=f.name_id JOIN artist_name_keys k ON k.id=n.key_id
LEFT JOIN artist_assignment_overrides ot ON ot.track_id=f.track_id AND ot.key_id=k.id
LEFT JOIN artist_assignment_overrides ore ON ore.release_id=p.release_id AND ore.key_id=k.id
LEFT JOIN artist_name_bindings b ON b.key_id=k.id
CROSS JOIN LATERAL(SELECT coalesce(ot.artist_id,ore.artist_id,b.artist_id,k.initial_artist_id) AS artist_id) route
LEFT JOIN artist_overrides o ON o.artist_id=route.artist_id;
UPDATE catalog_projection_state SET target_generation=target_generation+1,build_id=gen_random_uuid(),
       status='preparing',manifest_sealed=false,catalog_epoch=catalog_epoch+1
WHERE capability='artists' AND EXISTS(SELECT FROM rollback_credit_roles);
DROP TABLE release_match_credit_parts;
DROP FUNCTION curation_remove_credit_part();
DROP TABLE curation_release_match_results;
ALTER TABLE curation_work_states DROP COLUMN file_digest;
DROP FUNCTION curation_release_file_digest(uuid);
