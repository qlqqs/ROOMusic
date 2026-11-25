CREATE TABLE rating_tracks (
 user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 track_id UUID NOT NULL REFERENCES tracks(id) ON DELETE RESTRICT,
 rating SMALLINT NOT NULL CHECK (rating BETWEEN 1 AND 5),
 rated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (user_id,track_id)
);
CREATE INDEX rating_tracks_track_idx ON rating_tracks(track_id);

CREATE TABLE rating_albums (
 user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 group_id UUID NOT NULL REFERENCES release_groups(id) ON DELETE RESTRICT,
 rating SMALLINT NOT NULL CHECK (rating BETWEEN 1 AND 5),
 rated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 saved_title TEXT NOT NULL DEFAULT '',
 saved_artist TEXT NOT NULL DEFAULT '',
 PRIMARY KEY (user_id,group_id)
);
CREATE INDEX rating_albums_group_idx ON rating_albums(group_id);
CREATE INDEX rating_albums_highest_idx ON rating_albums(user_id,rating DESC,rated_at DESC,group_id);

CREATE TABLE rating_artists (
 user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 artist_id UUID NOT NULL REFERENCES artists(id) ON DELETE RESTRICT,
 resolved_artist_id UUID NOT NULL REFERENCES artists(id) ON DELETE RESTRICT,
 rating SMALLINT NOT NULL CHECK (rating BETWEEN 1 AND 5),
 rated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (user_id,artist_id)
);
CREATE INDEX rating_artists_artist_idx ON rating_artists(artist_id);
CREATE INDEX rating_artists_resolved_idx ON rating_artists(resolved_artist_id);
CREATE INDEX rating_artists_user_resolved_idx ON rating_artists(user_id,resolved_artist_id,rated_at DESC,artist_id);
