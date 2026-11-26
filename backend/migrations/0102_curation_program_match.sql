CREATE FUNCTION curation_release_file_digest(release_uuid uuid) RETURNS text
LANGUAGE sql STABLE AS $$
    SELECT encode(sha256(convert_to(jsonb_build_array(
        encode(r.curation_member_signature,'hex'),
        (SELECT jsonb_agg(jsonb_build_array(f.root_id,f.path,f.size,f.mtime)
                          ORDER BY f.root_id,f.path COLLATE "C",f.size,f.mtime)
         FROM (SELECT DISTINCT t.source_root_id AS root_id,
                      COALESCE(t.cue_parent_relative_path,t.relative_path) AS path,
                      t.source_size AS size,
                      (extract(epoch FROM t.source_modified_at)*1000000)::bigint AS mtime
               FROM media m JOIN tracks t ON t.medium_id=m.id
               WHERE m.release_id=r.id AND t.source_status='present') f)
    )::text,'UTF8')),'hex') FROM releases r WHERE r.id=release_uuid
$$;

ALTER TABLE curation_work_states ADD COLUMN file_digest text;
UPDATE curation_work_states SET file_digest=curation_release_file_digest(target_id)
WHERE subject='release';

CREATE TABLE curation_release_match_results (
    release_id uuid NOT NULL REFERENCES releases(id) ON DELETE CASCADE,
    source text NOT NULL,
    status text NOT NULL CHECK (status IN ('matched','unmatched','conflict')),
    checked_at timestamptz,
    reason text NOT NULL DEFAULT '',
    conflicts text[] NOT NULL DEFAULT '{}',
    PRIMARY KEY (release_id, source)
);

CREATE TABLE release_match_credit_parts (
    release_id uuid NOT NULL REFERENCES releases(id) ON DELETE CASCADE,
    track_id uuid REFERENCES tracks(id) ON DELETE CASCADE,
    role text NOT NULL CHECK (role IN ('release_artist','album_artist','track_artist')),
    position integer NOT NULL CHECK (position>0),
    local_text text NOT NULL,
    name text NOT NULL,
    artist_mbid uuid NOT NULL,
    artist_link_id uuid REFERENCES external_links(id) ON DELETE SET NULL,
    basis_link_id uuid NOT NULL REFERENCES external_links(id) ON DELETE CASCADE,
    operation_id uuid NOT NULL REFERENCES operations(id) DEFERRABLE INITIALLY DEFERRED,
    CHECK ((role='track_artist')=(track_id IS NOT NULL)),
    UNIQUE NULLS NOT DISTINCT (release_id,track_id,role,position)
);

-- 多个角色或发行可以共用艺人对应。最后一个拆分依据撤回时只移除本方法建立的对应。
CREATE INDEX release_match_parts_artist_link_idx ON release_match_credit_parts(artist_link_id);
CREATE FUNCTION curation_remove_credit_part() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    DELETE FROM external_links l WHERE l.id=OLD.artist_link_id AND l.method='split_credit'
      AND NOT EXISTS(SELECT FROM release_match_credit_parts p WHERE p.artist_link_id=l.id);
    RETURN NULL;
END $$;
CREATE TRIGGER curation_remove_credit_part AFTER DELETE ON release_match_credit_parts
FOR EACH ROW EXECUTE FUNCTION curation_remove_credit_part();

ALTER TABLE curation_work_states DROP CONSTRAINT curation_work_states_status_check,
    ADD CONSTRAINT curation_work_states_status_check CHECK (status IN
    ('pending','matched','uncertain','no_match','try_later','done','unlisted','no_usable_candidates','unmatched','conflict'));

INSERT INTO curation_release_match_results(release_id,source,status,checked_at,reason,conflicts)
SELECT s.target_id,'musicbrainz',
       CASE WHEN s.status='matched' THEN 'matched'
            WHEN s.status='uncertain' THEN 'conflict' ELSE 'unmatched' END,
       COALESCE((s.result->>'rechecked_at')::timestamptz,(s.result->>'unlisted_since')::timestamptz),
       CASE WHEN s.status='no_usable_candidates' THEN 'candidates_revoked' ELSE '' END,
       ARRAY(SELECT jsonb_array_elements_text(CASE WHEN jsonb_typeof(s.result->'conflicts')='array'
                                             THEN s.result->'conflicts' ELSE '[]'::jsonb END))
FROM curation_work_states s JOIN releases r ON r.id=s.target_id
WHERE s.subject='release' AND
(s.status IN ('matched','unlisted','no_usable_candidates') OR
 s.status IN ('uncertain','no_match') AND COALESCE(s.result->>'method','')<>''
 AND NOT COALESCE((s.result->'conflicts') ? 'insufficient_evidence',false));

UPDATE curation_work_states s SET status=q.status,
    next_attempt_at=CASE WHEN q.status='unmatched' AND q.reason='' THEN q.checked_at + interval '90 days' END,
    result=CASE WHEN q.reason<>'' THEN COALESCE(s.result,'{}'::jsonb)||jsonb_build_object('reason',q.reason) ELSE s.result END
FROM curation_release_match_results q WHERE s.subject='release' AND s.target_id=q.release_id AND s.status<>'matched';

DROP INDEX curation_unlisted_due_idx;
CREATE INDEX curation_unmatched_due_idx ON curation_work_states(next_attempt_at)
    WHERE status='unmatched' AND next_attempt_at IS NOT NULL;
