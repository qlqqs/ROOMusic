UPDATE curation_work_states
SET status='try_later', result=result-'retry', retry_count=0,
    next_attempt_at=COALESCE(next_attempt_at,updated_at)
WHERE subject='release' AND (status='query_error' OR result ? 'retry');

ALTER TABLE curation_work_states
    DROP CONSTRAINT curation_work_states_status_check,
    ADD CONSTRAINT curation_work_states_status_check CHECK
        (status IN ('pending','matched','uncertain','no_match','try_later','done','unlisted','no_usable_candidates'));
