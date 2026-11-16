ALTER TABLE steward_user_settings
 ADD COLUMN search_source text NOT NULL DEFAULT 'none' CHECK (search_source IN ('none','searxng','tavily','native')),
 ADD COLUMN searxng_url text NOT NULL DEFAULT '',
 ADD COLUMN search_key_ciphertext bytea,
 ADD COLUMN search_key_last_four text NOT NULL DEFAULT '' CHECK (char_length(search_key_last_four) <= 4),
 ADD CONSTRAINT steward_search_key_ck CHECK (search_key_ciphertext IS NOT NULL OR search_key_last_four = '');
UPDATE steward_user_settings SET probe_result = probe_result || '{"native_search":"unknown","responses_available":false}'::jsonb
 WHERE probe_result IS NOT NULL;

ALTER TABLE steward_user_settings
 DROP CONSTRAINT steward_user_settings_protocol_check,
 ADD CONSTRAINT steward_user_settings_protocol_check CHECK (protocol IN ('','openai','messages','responses'));
ALTER TABLE steward_messages
 ADD COLUMN provider_blocks jsonb,
 ADD CONSTRAINT steward_messages_provider_blocks_ck CHECK
  (provider_blocks IS NULL OR (role = 'assistant' AND jsonb_typeof(provider_blocks) = 'object'));
