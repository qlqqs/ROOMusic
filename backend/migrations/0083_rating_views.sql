CREATE INDEX rating_tracks_highest_idx ON rating_tracks(user_id,rating DESC,rated_at DESC,track_id);

CREATE INDEX rating_tracks_recent_idx ON rating_tracks(user_id,rated_at DESC,track_id);
CREATE INDEX rating_albums_recent_idx ON rating_albums(user_id,rated_at DESC,group_id);
