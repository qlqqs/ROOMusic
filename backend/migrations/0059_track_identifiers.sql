CREATE TABLE track_identifiers (
    track_id UUID NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('mb_release','mb_release_group','mb_recording','mb_track','mb_artist',
        'mb_album_artist','mb_work','mb_disc','acoustid','isrc')),
    value TEXT NOT NULL CHECK (length(value) BETWEEN 1 AND 64),
    ordinal SMALLINT NOT NULL,
    PRIMARY KEY (track_id, kind, value)
);
CREATE INDEX track_identifiers_value_idx ON track_identifiers (kind, value);
