-- 删除设备时，其自身播放区与队列按外键级联删除。
DELETE FROM playback_devices WHERE kind='upnp';
ALTER TABLE playback_devices
    DROP CONSTRAINT playback_devices_kind_check,
    DROP CONSTRAINT playback_devices_upnp_settings_check,
    DROP CONSTRAINT playback_devices_user_external_key,
    DROP COLUMN external_id,
    DROP COLUMN max_sample_rate,
    DROP COLUMN volume_display_only,
    ADD CONSTRAINT playback_devices_kind_check CHECK (kind IN ('browser', 'native'));
