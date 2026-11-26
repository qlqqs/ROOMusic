UPDATE curation_work_states SET result=result-'judge'-'retry',needs_work=true,status='pending',next_attempt_at=NULL
WHERE subject='release' AND result ? 'judge';
DROP TABLE curation_match_names,curation_artist_negative_pairs;
ALTER TABLE curation_release_match_results DROP COLUMN selection,DROP COLUMN candidates,DROP COLUMN snapshot_id;
