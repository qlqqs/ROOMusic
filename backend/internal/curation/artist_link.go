package curation

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

type ArtistLinkRequest struct {
	ArtistID, ActorID, Key, RequestID    string
	ExternalSource, ExternalID           string
	ExpectedRevision                     int64
	Source                               Source
	BatchID, ConversationID, ConfirmedBy string
}

type ArtistLinkApplied struct {
	OperationID string `json:"operation_id"`
	ArtistID    string `json:"artist_id"`
	Revision    int64  `json:"revision"`
}

type artistLinkRow struct {
	ID          string    `json:"id"`
	Source      string    `json:"source"`
	ObjectType  string    `json:"object_type"`
	ObjectID    string    `json:"object_id"`
	ExternalID  string    `json:"external_id"`
	Method      string    `json:"method"`
	Confidence  string    `json:"confidence"`
	OperationID *string   `json:"operation_id"`
	CreatedAt   time.Time `json:"created_at"`
}

type artistLinkState struct {
	Links  []artistLinkRow   `json:"links"`
	Images []json.RawMessage `json:"images,omitempty"`
	Bios   []json.RawMessage `json:"bios,omitempty"`
}

func readArtistLinkRows(ctx context.Context, q operations.Queryer, id string) ([]artistLinkRow, error) {
	var raw []byte
	err := q.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(l) ORDER BY source),'[]') FROM external_links l
	WHERE object_type='artist' AND object_id=$1 AND source IN ('musicbrainz','wikidata')`, id).Scan(&raw)
	if err != nil {
		return nil, err
	}
	var links []artistLinkRow
	err = json.Unmarshal(raw, &links)
	return links, err
}

func (w *Writer) artistLinkSpec(ctx context.Context, r ArtistLinkRequest) (operations.Spec, error) {
	key, ok := operations.NormalizeKey(r.Key)
	if !ok || !uuid.Valid(r.ArtistID) || r.ExpectedRevision < 1 ||
		!(r.ExternalSource == "musicbrainz" && uuid.Valid(r.ExternalID) || r.ExternalSource == "wikidata" && sources.ValidWikidataID(r.ExternalID)) {
		return operations.Spec{}, operations.Invalid("invalid_input", "艺人、外部对应、版本或幂等键无效")
	}
	fp := operations.Fingerprint(map[string]any{"type": operations.TypeArtistLink, "artist_id": r.ArtistID, "source": r.ExternalSource, "external_id": r.ExternalID, "expected_revision": r.ExpectedRevision, "writer": r.Source, "batch_id": r.BatchID, "conversation_id": r.ConversationID, "confirmed_by": r.ConfirmedBy})
	if r.Source.Kind == "model" {
		if !uuid.Valid(r.ConversationID) || (r.ConfirmedBy == "" && !uuid.Valid(r.BatchID)) {
			return operations.Spec{}, operations.Invalid("invalid_input", "模型写入需要批次与会话")
		}
		return w.sourceSpec(ctx, operations.TypeArtistLink, r.ArtistID, fp, r.Source, r.BatchID, key, r.RequestID, r.ConfirmedBy, r.ConversationID)
	}
	if r.Source.Kind != "" || !uuid.Valid(r.ActorID) || r.BatchID != "" || r.ConversationID != "" || r.ConfirmedBy != "" {
		return operations.Spec{}, operations.Invalid("invalid_input", "艺人对应的写入者无效")
	}
	return operations.Spec{Type: operations.TypeArtistLink, ActorID: r.ActorID, Key: key, RequestID: r.RequestID, Fingerprint: fp}, nil
}

func (w *Writer) ApplyArtistLink(ctx context.Context, gate *Sources, r ArtistLinkRequest) (ArtistLinkApplied, error) {
	r.ArtistID = strings.ToLower(r.ArtistID)
	if r.ExternalSource == "musicbrainz" {
		r.ExternalID = strings.ToLower(r.ExternalID)
	}
	spec, err := w.artistLinkSpec(ctx, r)
	if err != nil {
		return ArtistLinkApplied{}, err
	}
	// 外部请求在事务和目录写锁之外进行；已有幂等键直接交给 Run 比对并重放。
	var recorded bool
	err = w.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT FROM operations WHERE actor_id=$1 AND idempotency_key=$2)`, spec.ActorID, spec.Key).Scan(&recorded)
	if err != nil {
		return ArtistLinkApplied{}, err
	}
	var p ArtistLinkPreview
	if !recorded {
		p, _, err = readArtistLink(ctx, gate, externalIdentity{r.ExternalSource, r.ExternalID})
		if err != nil {
			return ArtistLinkApplied{}, err
		}
	}
	res, err := operations.Run(ctx, w.db, spec, func(ctx context.Context, tx *sql.Tx, op string) (operations.Outcome, error) {
		id, revision, err := lockEffectiveArtist(ctx, tx, r.ArtistID)
		if err != nil {
			return operations.Outcome{}, err
		}
		if err = checkArtistRevision(revision, r.ExpectedRevision); err != nil {
			return operations.Outcome{}, err
		}
		if spec.Origin != nil {
			if err := checkWrite(ctx, tx, spec, r.ConfirmedBy, id, r.ExpectedRevision, revision); err != nil {
				return operations.Outcome{}, err
			}
		}
		before, after, err := writeArtistLinks(ctx, tx, id, op, p, spec.Origin != nil)
		if err != nil {
			return operations.Outcome{}, err
		}
		out, err := artistEditOutcome(ctx, tx, id, before, after)
		out.Response = ArtistLinkApplied{OperationID: op, ArtistID: id, Revision: out.ResultRevision}
		return out, err
	})
	if err != nil {
		return ArtistLinkApplied{}, err
	}
	var applied ArtistLinkApplied
	err = json.Unmarshal(res.Response, &applied)
	return applied, err
}

func writeArtistLinks(ctx context.Context, tx *sql.Tx, id, op string, p ArtistLinkPreview, model bool) (artistLinkState, artistLinkState, error) {
	var before, after artistLinkState
	var err error
	before.Links, err = readArtistLinkRows(ctx, tx, id)
	if err != nil {
		return before, after, err
	}
	method := "artist_manual"
	if model {
		method = "artist_model"
	}
	changed := false
	for _, item := range []struct {
		source     string
		externalID *string
	}{{"musicbrainz", p.MBID}, {"wikidata", p.WikidataID}} {
		if item.externalID == nil {
			continue
		}
		var current string
		var suppressed bool
		identity := externalIdentity{item.source, *item.externalID}
		err = tx.QueryRowContext(ctx, `WITH `+catalog.ArtistMediaFamilyCTE+`, family AS (
			SELECT * FROM artist_media_family WHERE canonical_id=$1
		) SELECT COALESCE((SELECT external_id FROM family f JOIN external_links l ON l.object_id=f.artist_id
			WHERE object_type='artist' AND source=$2 ORDER BY `+catalog.ArtistMediaOrder+` LIMIT 1),''),
			EXISTS(SELECT FROM family f JOIN curation_suppressions s ON s.target_id=f.artist_id
			    WHERE target_type='artist' AND subject='external_link' AND value_hash=$3)`, id, item.source, identity.identityHash()).Scan(&current, &suppressed)
		if err != nil {
			return before, after, err
		}
		if model && current != "" {
			return before, after, operations.Conflict("occupied", "艺人已有同源对应")
		}
		if model && suppressed {
			return before, after, operations.Conflict("suppressed", "此艺人对应已标记不再提出")
		}
		if current == identity.ExternalID || item.source == "musicbrainz" && strings.EqualFold(current, identity.ExternalID) {
			continue
		}
		_, err = PutLink(ctx, tx, Link{Source: item.source, ObjectType: "artist", ObjectID: id, ExternalID: identity.ExternalID, Method: method, Confidence: "confirmed", OperationID: op})
		if err != nil {
			return before, after, err
		}
		changed = true
	}
	if !changed {
		return before, after, operations.Conflict("nothing_to_apply", "艺人已有相同的对应")
	}
	after.Links, err = readArtistLinkRows(ctx, tx, id)
	if err != nil {
		return before, after, err
	}
	before.Images, err = clearUnlinkedMedia(ctx, tx, id, op, "artist_external_artworks")
	if err != nil {
		return before, after, err
	}
	before.Bios, err = clearUnlinkedMedia(ctx, tx, id, op, "artist_external_bios")
	return before, after, err
}

const artistLinkFamilyCTE = `WITH ` + catalog.ArtistMediaFamilyCTE + `, family AS (
	SELECT canonical_id, artist_id, merged_at FROM artist_media_family WHERE canonical_id=$1
), ` + ArtistMediaLinksCTE

func clearUnlinkedMedia(ctx context.Context, tx *sql.Tx, id, op, table string) ([]json.RawMessage, error) {
	query := artistLinkFamilyCTE + `, removed AS (
		DELETE FROM ` + table + ` m USING family f WHERE m.artist_id=f.artist_id AND m.basis_source IS NOT NULL AND NOT EXISTS (
			SELECT FROM media_links l WHERE l.source=m.basis_source AND l.external_id=m.basis_id)
		RETURNING m.*
	)`
	args := []any{id}
	if table == "artist_external_artworks" {
		query += `, retained AS (INSERT INTO artwork_retained(storage_key,operation_id)
			SELECT DISTINCT storage_key,$2::uuid FROM removed ON CONFLICT DO NOTHING)`
		args = append(args, op)
	}
	var raw []byte
	err := tx.QueryRowContext(ctx, query+` SELECT COALESCE(jsonb_agg(to_jsonb(removed) ORDER BY artist_id),'[]') FROM removed`, args...).Scan(&raw)
	if err != nil {
		return nil, err
	}
	var removed []json.RawMessage
	err = json.Unmarshal(raw, &removed)
	return removed, err
}
