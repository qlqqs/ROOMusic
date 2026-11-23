package curation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type LyricsRequest struct {
	TrackID                 string
	Text                    string
	Source                  Source
	ExternalID              string
	BatchID, Key, RequestID string
}

type lyricsValue struct {
	externalIdentity
	Hash  string `json:"content_hash"`
	Kind  string `json:"kind"`
	Lines int    `json:"lines"`
}

type lyricsState struct {
	TrackID    string      `json:"track_id"`
	TrackTitle string      `json:"track_title"`
	Lyrics     lyricsValue `json:"lyrics"`
}

func (w *Writer) ApplyLyrics(ctx context.Context, request LyricsRequest) (ExternalApplied, error) {
	normalized, ok := metadata.NormalizeLyrics(request.Text)
	if !ok {
		return ExternalApplied{}, operations.Invalid("invalid_input", "歌词文本无效")
	}
	parsed, ok := catalog.ParseLyrics(normalized)
	if !ok {
		return ExternalApplied{}, operations.Invalid("invalid_input", "歌词没有可显示的行")
	}
	id := strings.ToLower(request.TrackID)
	hash := hashExternalValue(normalized)
	spec, err := w.externalSpec(ctx, operations.TypeTrackExternalLyrics, "track_id", id, hash, request.Source, request.ExternalID, request.BatchID, request.Key, request.RequestID)
	if err != nil {
		return ExternalApplied{}, err
	}
	identity := externalIdentity{request.Source.Provider, request.ExternalID}
	marker := externalSuppression{"track", id, "lyrics", hash, identity}
	var applied ExternalApplied
	result, err := operations.Run(ctx, w.db, spec, func(ctx context.Context, tx *sql.Tx, operationID string) (operations.Outcome, error) {
		release, err := governance.LockAutoReleaseOfTrack(ctx, tx, id)
		if err != nil {
			return operations.Outcome{}, err
		}
		if err := checkExternalWriter(ctx, tx, spec); err != nil {
			return operations.Outcome{}, err
		}
		var title string
		var local sql.NullString
		var occupied bool
		if err := tx.QueryRowContext(ctx, `SELECT t.title,t.lyrics,e.track_id IS NOT NULL
   FROM track_effective t LEFT JOIN track_external_lyrics e ON e.track_id=t.id WHERE t.id=$1::uuid`, id).Scan(&title, &local, &occupied); err != nil {
			return operations.Outcome{}, err
		}
		if _, valid := catalog.ParseLyrics(local.String); valid {
			return applied.skip("local_present")
		}
		if occupied {
			return applied.skip("occupied")
		}
		suppressed, err := marker.exists(ctx, tx)
		if err != nil {
			return operations.Outcome{}, err
		}
		if suppressed {
			return applied.skip("suppressed")
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO track_external_lyrics(track_id,lyrics,content_hash,source,external_id,operation_id)
    VALUES($1::uuid,$2,$3,$4,$5,$6::uuid)`, id, normalized, hash, identity.Source, identity.ExternalID, operationID)
		if err != nil {
			return operations.Outcome{}, err
		}
		after := lyricsState{id, title, lyricsValue{identity, hash, parsed.Kind, len(parsed.Lines)}}
		out, err := externalOutcome(ctx, release, map[string]any{"track_id": id, "lyrics": nil}, after)
		out.Response = ExternalApplied{OperationID: operationID}
		return out, err
	})
	if err != nil {
		return applied, err
	}
	return ExternalApplied{OperationID: result.OperationID}, nil
}

func ExternalLyricsOperations(w *Writer) operations.Kind {
	return externalKind{w, operations.TypeTrackExternalLyrics, "track_external_lyrics", "track_id", decodeLyricsChange}
}

func decodeLyricsChange(record operations.Record, raw json.RawMessage) (externalChange, error) {
	var state lyricsState
	if err := json.Unmarshal(raw, &state); err != nil {
		return externalChange{}, err
	}
	l := state.Lyrics
	if !uuid.Valid(state.TrackID) || l.Hash == "" || l.Source == "" || l.ExternalID == "" ||
		(l.Kind != catalog.LyricsKindSynced && l.Kind != catalog.LyricsKindPlain) || l.Lines <= 0 {
		return externalChange{}, errors.New("歌词操作记录缺少曲目或歌词信息")
	}
	return externalChange{
		marker: externalSuppression{"track", state.TrackID, "lyrics", l.Hash, l.externalIdentity},
		value:  map[string]any{"source": l.Source, "external_id": l.ExternalID, "kind": l.Kind, "lines": l.Lines},
		changes: map[string]any{"kind": operations.TypeTrackExternalLyrics, "source": l.Source, "external_id": l.ExternalID,
			"track": map[string]string{"id": state.TrackID, "title": state.TrackTitle}, "lyrics_kind": l.Kind, "lines": l.Lines},
		emptyState: map[string]any{"track_id": state.TrackID, "lyrics": nil}, preview: map[string]string{"source": l.Source, "external_id": l.ExternalID, "track_title": state.TrackTitle}}, nil
}
