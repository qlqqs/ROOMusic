CREATE TABLE curation_artist_web_queue (
    id boolean PRIMARY KEY CHECK (id),
    generation bigint NOT NULL DEFAULT 0,
    state text NOT NULL DEFAULT 'idle' CHECK (state IN ('idle','running','stopped')),
    actor_id uuid REFERENCES users(id) ON DELETE SET NULL,
    started_at timestamptz,
    stopped_at timestamptz
);
INSERT INTO curation_artist_web_queue(id) VALUES (true);
ALTER TABLE curation_pending_items ADD COLUMN artist_web_generation bigint,
    ADD COLUMN artist_web_actor uuid REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN artist_web_suspended boolean NOT NULL DEFAULT false,
    ADD COLUMN artist_web_priority integer;
CREATE INDEX curation_artist_web_items ON curation_pending_items(artist_web_generation,artist_web_priority,artist_id)
    WHERE artist_web_generation IS NOT NULL;
