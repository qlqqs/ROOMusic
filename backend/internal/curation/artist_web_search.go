package curation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
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

type ArtistWebPosition struct {
	Generation int64 `json:"generation"`
	Position   int   `json:"position"`
	Total      int   `json:"total"`
}
type ArtistWebUsage struct {
	ToolCalls     int   `json:"tool_calls"`
	Tokens        int64 `json:"tokens"`
	ToolCallLimit int   `json:"tool_call_limit"`
	TokenLimit    int   `json:"token_limit"`
}
type ArtistWebSearchStatus struct {
	Configured bool                       `json:"configured"`
	CanStart   bool                       `json:"can_start"`
	CanStop    bool                       `json:"can_stop"`
	Queue      *ArtistWebPosition         `json:"queue"`
	Usage      ArtistWebUsage             `json:"usage"`
	State      string                     `json:"state"`
	Candidates []ArtistWebSearchCandidate `json:"candidates"`
}

func (w *Writer) ArtistWebSearch(ctx context.Context, artistID, actor string) (ArtistWebSearchStatus, error) {
	status := ArtistWebSearchStatus{State: "none", Usage: ArtistWebUsage{ToolCallLimit: ArtistWebCallLimit, TokenLimit: ArtistWebTokenLimit}, Candidates: []ArtistWebSearchCandidate{}}
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
	var linked bool
	err = tx.QueryRowContext(ctx, artistLinkFamilyCTE+` SELECT EXISTS(SELECT FROM media_links WHERE source='musicbrainz')`, canonical).Scan(&linked)
	if err != nil {
		return status, err
	}
	var digest string
	if !linked && canonical == artistID {
		var id string
		var priority int
		var evidence []byte
		err = tx.QueryRowContext(ctx, artistWebInputs, canonical, ArtistWebRuleVersion, actor).Scan(&id, &priority, &evidence, &digest)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return status, err
		}
		status.CanStart = err == nil
	}
	var itemDigest string
	var gen *int64
	err = tx.QueryRowContext(ctx, `SELECT id, CASE WHEN status='resolved' AND EXISTS(SELECT FROM curation_model_exceptions x WHERE x.pending_item_id=p.id AND x.tool='resolve_artist_identity' AND x.status IN ('open','accepting')) THEN 'waiting_human'
 WHEN status IN ('waiting_model','waiting_route') THEN 'queued' WHEN status='in_progress' THEN 'running' WHEN status='resolved' THEN 'none' ELSE status END,
 item_tool_calls,item_tokens,artist_web_generation,input_digest FROM curation_pending_items p WHERE artist_id=$1 AND kind='artist_identity' ORDER BY created_at DESC,id DESC LIMIT 1`, canonical).Scan(&item, &status.State, &status.Usage.ToolCalls, &status.Usage.Tokens, &gen, &itemDigest)
	if errors.Is(err, sql.ErrNoRows) {
		return status, nil
	}
	if err != nil {
		return status, err
	}
	if gen != nil {
		var pos ArtistWebPosition
		var running bool
		err = tx.QueryRowContext(ctx, `SELECT q.generation,q.state='running' AND NOT bool_or(target.artist_web_suspended),count(p.id),count(*) FILTER(WHERE (p.artist_web_priority,p.artist_id)<=(target.artist_web_priority,target.artist_id))
 FROM curation_artist_web_queue q JOIN curation_pending_items target ON target.id=$1 LEFT JOIN curation_pending_items p ON p.artist_web_generation=q.generation GROUP BY q.id`, item).Scan(&pos.Generation, &running, &pos.Total, &pos.Position)
		if err != nil {
			return status, err
		}
		if pos.Generation == *gen {
			status.Queue = &pos
			status.CanStop = running && (status.State == "queued" || status.State == "running")
		}
	}
	status.CanStart = status.CanStart && !status.CanStop && (status.State != "waiting_human" || gen != nil && itemDigest != digest)
	if gen != nil && digest == itemDigest && status.State != "queued" && status.State != "running" {
		status.CanStart = false
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

func (w *Writer) CheckArtistCandidate(ctx context.Context, id string) error {
	if !uuid.Valid(id) {
		return operations.Invalid("invalid_id", "候选标识无效")
	}
	var exists bool
	err := w.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT FROM curation_model_exceptions x JOIN curation_pending_items p ON p.id=x.pending_item_id WHERE x.id=$1 AND x.tool='resolve_artist_identity' AND p.artist_web_generation IS NOT NULL)`, id).Scan(&exists)
	if err == nil && !exists {
		return operations.NotFound("not_found", "艺人候选不存在")
	}
	return err
}
