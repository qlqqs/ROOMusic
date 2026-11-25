DROP INDEX curation_pending_suggestions_idx;
ALTER TABLE curation_pending_items DROP CONSTRAINT curation_pending_items_status_check,
    ADD CONSTRAINT curation_pending_items_status_check CHECK
    (status IN ('waiting_model','in_progress','resolved','waiting_human','shelved','waiting_route'));

DELETE FROM curation_model_exceptions WHERE review_id IS NULL OR plan_item_id IS NULL OR tool IN
    ('submit_artist_media','submit_verification','observation');
DELETE FROM curation_pending_items WHERE kind IN ('verify_artist_credit','verify_artist_merge','verify_name');

-- 一个目标有多代未采纳历史时，旧版的开放项唯一索引只允许最新一代回到原状态。
WITH latest AS (
    SELECT DISTINCT ON (target_type,COALESCE(target_id,artist_id),kind) id FROM curation_pending_items p
    WHERE resolution='suggestion_recorded' AND EXISTS (
        SELECT FROM curation_model_exceptions x WHERE x.pending_item_id=p.id AND x.status IN ('open','accepting'))
    AND NOT EXISTS (SELECT FROM curation_pending_items open WHERE open.status<>'resolved'
        AND open.kind=p.kind AND open.target_type=p.target_type
        AND COALESCE(open.target_id,open.artist_id)=COALESCE(p.target_id,p.artist_id))
    ORDER BY target_type,COALESCE(target_id,artist_id),kind,created_at DESC,id DESC)
UPDATE curation_pending_items SET status='waiting_human',resolution=NULL,resolved_at=NULL
WHERE id IN (SELECT id FROM latest);

ALTER TABLE curation_model_exceptions
    DROP CONSTRAINT curation_model_exceptions_tool_check,
    ADD CONSTRAINT curation_model_exceptions_tool_check CHECK (tool IN
        ('resolve_release_match','correct_release','correct_track','merge_releases','resolve_artist_identity')),
    DROP CONSTRAINT curation_model_exceptions_review_ck,
    ALTER COLUMN plan_item_id SET NOT NULL,
    ALTER COLUMN review_id SET NOT NULL,
    ALTER COLUMN review_model SET NOT NULL,
    ALTER COLUMN review_decision SET NOT NULL,
    ALTER COLUMN review_source SET NOT NULL,
    DROP COLUMN suggestion_key, DROP COLUMN evidence, DROP COLUMN reason_code;

DROP INDEX curation_pending_items_open_idx;
CREATE UNIQUE INDEX curation_pending_items_open_idx ON curation_pending_items(target_type,target_id,kind)
    WHERE status <> 'resolved';
ALTER TABLE curation_pending_items DROP COLUMN item_tokens, DROP COLUMN item_tool_calls,
    DROP CONSTRAINT curation_pending_items_target_ck,
    ADD CONSTRAINT curation_pending_items_target_ck CHECK (
        (target_type='release' AND target_id IS NOT NULL AND artist_id IS NULL AND kind IN
            ('release_match','recording_match','release_merge'))
        OR (target_type='artist' AND artist_id IS NOT NULL AND target_id IS NULL AND kind='artist_identity'));
ALTER TABLE curation_model_usage DROP COLUMN web_input_tokens, DROP COLUMN web_output_tokens;
ALTER TABLE curation_settings DROP COLUMN web_daily_token_limit;
