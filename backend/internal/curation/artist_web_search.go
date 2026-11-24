package curation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/operations"
)

func ParseArtistWebURL(address string) (*url.URL, error) {
	u, err := url.ParseRequestURI(address)
	if err != nil || len(address) > 2048 || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || strings.HasSuffix(u.Host, ":") {
		return nil, operations.Invalid("invalid_input", "需要不含用户名和密码的 HTTP(S) 网址")
	}
	return u, nil
}

type ArtistWebSearchCandidate struct {
	ExceptionID string             `json:"exception_id"`
	Source      string             `json:"source"`
	ExternalID  string             `json:"external_id"`
	Name        string             `json:"name"`
	Description *string            `json:"description"`
	Note        string             `json:"note"`
	Citations   []catalog.Citation `json:"citations"`
}

type ArtistWebSearchStatus struct {
	State      string                     `json:"state"`
	Candidates []ArtistWebSearchCandidate `json:"candidates"`
}

func (w *Writer) ArtistWebSearch(ctx context.Context, artistID string) (ArtistWebSearchStatus, error) {
	status := ArtistWebSearchStatus{State: "none", Candidates: []ArtistWebSearchCandidate{}}
	tx, err := w.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return status, err
	}
	defer tx.Rollback()
	canonical, err := effectiveArtist(ctx, tx, artistID)
	if err != nil {
		return status, err
	}
	var item string
	err = tx.QueryRowContext(ctx, `SELECT id, CASE status WHEN 'waiting_model' THEN 'queued' WHEN 'in_progress' THEN 'running' ELSE status END
 FROM curation_pending_items WHERE artist_id=$1 AND target_type='artist' AND kind='artist_identity' AND status<>'resolved'`, canonical).Scan(&item, &status.State)
	if errors.Is(err, sql.ErrNoRows) {
		return status, nil
	}
	if err != nil {
		return status, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,input->>'source',input->>'external_id',preview#>>'{changes,0,after,name}',
 preview#>>'{changes,0,after,description}',input#>>'{basis,note}',input#>'{basis,citations}'
 FROM curation_model_exceptions WHERE pending_item_id=$1 AND tool='resolve_artist_identity' AND status IN ('open','accepting')
 ORDER BY created_at,id`, item)
	if err != nil {
		return status, err
	}
	defer rows.Close()
	for rows.Next() {
		var c ArtistWebSearchCandidate
		var raw []byte
		if err := rows.Scan(&c.ExceptionID, &c.Source, &c.ExternalID, &c.Name, &c.Description, &c.Note, &raw); err != nil {
			return status, err
		}
		var urls []string
		if err := json.Unmarshal(raw, &urls); err != nil {
			return status, err
		}
		c.Citations = make([]catalog.Citation, 0, len(urls))
		for _, u := range urls {
			c.Citations = append(c.Citations, catalog.Citation{URL: u})
		}
		status.Candidates = append(status.Candidates, c)
	}
	if err = rows.Err(); err != nil {
		return status, err
	}
	return status, tx.Commit()
}
