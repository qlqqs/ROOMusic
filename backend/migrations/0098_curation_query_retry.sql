ALTER TABLE curation_work_states
    DROP CONSTRAINT curation_work_states_status_check,
    ADD CONSTRAINT curation_work_states_status_check CHECK
        (status IN ('pending','matched','uncertain','no_match','try_later','done','unlisted','no_usable_candidates','query_error'));

UPDATE curation_work_states
SET retry_count=0, next_attempt_at=LEAST(next_attempt_at, now()+interval '3 minutes')
WHERE subject='release' AND status='try_later';
