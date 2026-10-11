-- 播放历史：每个用户各自的真实播放记录（阶段 P）。一条记录表示“这个用户把这首曲目播放到了计次规则要求的程度”，
-- played_seconds 是达到规则那一刻累计的实际出声秒数，played_at 是服务端收到上报的时间（不信任客户端时钟）。
-- idempotency_key 由客户端为每次播放生成，同一用户的同一个键只写一行，所以重试不会重复计数。
-- 不保存 IP、User-Agent、设备或会话信息。track_id 不级联删除：曲目从不物理删除（只标 missing），
-- 将来若有删除曲目的操作，必须先显式处理历史；user_id 级联：用户目前不能删除，若将来可以，个人数据随之删除。
-- 该迁移只新增一张表，不改写已有数据。
CREATE TABLE IF NOT EXISTS play_history (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    track_id UUID NOT NULL REFERENCES tracks(id),
    idempotency_key UUID NOT NULL,
    played_seconds DOUBLE PRECISION NOT NULL,
    played_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT play_history_played_seconds_ck CHECK (played_seconds > 0 AND played_seconds <= 86400),
    CONSTRAINT play_history_user_key_uq UNIQUE (user_id, idempotency_key)
);

-- “最近播放”按用户分组取每首曲目的最后播放时间；按时间倒序的完整列表（M5）再按需要加索引。
CREATE INDEX IF NOT EXISTS play_history_user_track_idx ON play_history (user_id, track_id, played_at DESC);
