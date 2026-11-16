ALTER TABLE steward_messages DROP CONSTRAINT steward_messages_provider_blocks_ck, DROP COLUMN provider_blocks;
UPDATE steward_user_settings SET protocol='openai' WHERE protocol='responses';
UPDATE steward_user_settings SET overrides=jsonb_set(overrides,'{protocol}','"openai"'::jsonb)
 WHERE overrides->>'protocol'='responses';
UPDATE steward_user_settings SET probe_result=jsonb_set(probe_result,'{protocol}','"openai"'::jsonb)
 WHERE probe_result->>'protocol'='responses';
UPDATE steward_user_settings SET probe_result=jsonb_set(probe_result,'{failures}',
 COALESCE((SELECT jsonb_agg(failure) FROM jsonb_array_elements(probe_result->'failures') AS failure
  WHERE failure->>'protocol' IS DISTINCT FROM 'responses'), '[]'::jsonb))
 WHERE probe_result ? 'failures';
ALTER TABLE steward_user_settings
 DROP CONSTRAINT steward_user_settings_protocol_check,
 ADD CONSTRAINT steward_user_settings_protocol_check CHECK (protocol IN ('','openai','messages'));
UPDATE steward_user_settings SET probe_result = probe_result - 'native_search' - 'responses_available',
 overrides = overrides - 'native_search';
ALTER TABLE steward_user_settings
 DROP CONSTRAINT steward_search_key_ck,
 DROP COLUMN search_source,
 DROP COLUMN searxng_url,
 DROP COLUMN search_key_ciphertext,
 DROP COLUMN search_key_last_four;
