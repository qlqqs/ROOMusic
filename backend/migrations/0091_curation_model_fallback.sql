ALTER TABLE curation_settings ADD COLUMN web_daily_token_limit bigint
    CHECK (web_daily_token_limit > 0);
ALTER TABLE curation_model_usage
    ADD COLUMN web_input_tokens bigint NOT NULL DEFAULT 0 CHECK (web_input_tokens >= 0),
    ADD COLUMN web_output_tokens bigint NOT NULL DEFAULT 0 CHECK (web_output_tokens >= 0);

ALTER TABLE curation_pending_items
    ADD COLUMN item_tool_calls integer NOT NULL DEFAULT 0 CHECK (item_tool_calls >= 0),
    ADD COLUMN item_tokens bigint NOT NULL DEFAULT 0 CHECK (item_tokens >= 0),
    DROP CONSTRAINT curation_pending_items_target_ck,
    ADD CONSTRAINT curation_pending_items_target_ck CHECK (
        (target_type='release' AND target_id IS NOT NULL AND artist_id IS NULL AND kind IN
            ('release_match','recording_match','release_merge','verify_artist_credit','verify_artist_merge','verify_name'))
        OR (target_type='artist' AND artist_id IS NOT NULL AND target_id IS NULL AND kind='artist_identity'));

-- 核对项的主键由目标、主张和依据派生，同一发行可有几条相互独立的纠正。
DROP INDEX curation_pending_items_open_idx;
CREATE UNIQUE INDEX curation_pending_items_open_idx ON curation_pending_items(target_type,target_id,kind)
    WHERE status <> 'resolved' AND kind NOT IN ('verify_artist_credit','verify_artist_merge','verify_name');

ALTER TABLE curation_model_exceptions
    ADD COLUMN reason_code text,
    ADD COLUMN evidence jsonb NOT NULL DEFAULT '{}',
    ADD COLUMN suggestion_key uuid UNIQUE,
    ALTER COLUMN plan_item_id DROP NOT NULL,
    ALTER COLUMN review_id DROP NOT NULL,
    ALTER COLUMN review_model DROP NOT NULL,
    ALTER COLUMN review_decision DROP NOT NULL,
    ALTER COLUMN review_source DROP NOT NULL,
    DROP CONSTRAINT curation_model_exceptions_tool_check,
    ADD CONSTRAINT curation_model_exceptions_tool_check CHECK (tool IN
        ('resolve_release_match','correct_release','correct_track','merge_releases','resolve_artist_identity',
         'submit_artist_media','submit_verification','observation')),
    ADD CONSTRAINT curation_model_exceptions_review_ck CHECK (
        (num_nonnulls(review_id,review_model,review_decision,review_source)=4) OR
        (num_nonnulls(review_id,review_model,review_decision,review_source,risk)=0));

UPDATE curation_model_exceptions SET suggestion_key=plan_item_id,reason_code=CASE review_decision
    WHEN 'rejected' THEN 'review_rejected' ELSE 'review_uncertain' END;
ALTER TABLE curation_model_exceptions ALTER COLUMN reason_code SET NOT NULL;

UPDATE curation_pending_items SET status='resolved', resolution='suggestion_recorded',
    resolved_at=COALESCE(resolved_at,now()), claimed_at=NULL, next_attempt_at=NULL, updated_at=now()
    WHERE status='waiting_human';
ALTER TABLE curation_pending_items DROP CONSTRAINT curation_pending_items_status_check,
    ADD CONSTRAINT curation_pending_items_status_check CHECK
    (status IN ('waiting_model','in_progress','resolved','shelved','waiting_route'));

CREATE INDEX curation_pending_suggestions_idx ON curation_pending_items
    (target_type,target_id,artist_id,kind,identity_digest,input_digest)
    WHERE resolution='suggestion_recorded';
