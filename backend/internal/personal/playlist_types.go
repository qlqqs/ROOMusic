package personal

import "time"

type PlaylistState struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Revision       int64     `json:"revision"`
	TrackCount     int64     `json:"track_count"`
	TrackLimit     int       `json:"track_limit"`
	OverLimitCount int64     `json:"over_limit_count"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}
type ItemRef struct {
	ID       string    `json:"id"`
	TrackID  string    `json:"track_id"`
	Position int64     `json:"position"`
	AddedAt  time.Time `json:"added_at"`
}
type playlistItem struct {
	ItemRef
	OrderKey int64
}

const playlistColumns = `id::text,name,revision,track_count,created_at,updated_at`

type scanner interface{ Scan(...any) error }

func scanPlaylist(row scanner) (PlaylistState, error) {
	var p PlaylistState
	err := row.Scan(&p.ID, &p.Name, &p.Revision, &p.TrackCount, &p.CreatedAt, &p.UpdatedAt)
	p.TrackLimit = TrackLimit
	p.OverLimitCount = max(p.TrackCount-TrackLimit, 0)
	p.CreatedAt = p.CreatedAt.UTC()
	p.UpdatedAt = p.UpdatedAt.UTC()
	return p, err
}
