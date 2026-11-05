package governance

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/qlqq/roomusic/backend/internal/operations"
)

// 候选的归属必须按证据作用域解析；不能把名字键的全局归属当作曲目的归属。
const artistCandidateResolvedSQL = `WITH resolved AS (
 SELECT s.id,s.left_key_id,s.right_key_id,s.kind,s.release_id,s.track_id,s.role,s.left_name,s.right_name,
 COALESCE(lo.redirect_artist_id,lt.artist_id,lr.artist_id,lb.artist_id,lk.initial_artist_id) AS left_artist_id,
 COALESCE(ro.redirect_artist_id,rt.artist_id,rr.artist_id,rb.artist_id,rk.initial_artist_id) AS right_artist_id
 FROM artist_candidate_signals s
 JOIN artist_name_keys lk ON lk.id=s.left_key_id JOIN artist_name_keys rk ON rk.id=s.right_key_id
 LEFT JOIN artist_name_bindings lb ON lb.key_id=lk.id LEFT JOIN artist_name_bindings rb ON rb.key_id=rk.id
 LEFT JOIN artist_assignment_overrides lt ON lt.track_id=s.track_id AND lt.key_id=lk.id
 LEFT JOIN artist_assignment_overrides rt ON rt.track_id=s.track_id AND rt.key_id=rk.id
 LEFT JOIN artist_assignment_overrides lr ON lr.release_id=s.release_id AND lr.key_id=lk.id
 LEFT JOIN artist_assignment_overrides rr ON rr.release_id=s.release_id AND rr.key_id=rk.id
 LEFT JOIN artist_overrides lo ON lo.artist_id=COALESCE(lt.artist_id,lr.artist_id,lb.artist_id,lk.initial_artist_id)
 LEFT JOIN artist_overrides ro ON ro.artist_id=COALESCE(rt.artist_id,rr.artist_id,rb.artist_id,rk.initial_artist_id)
 WHERE (s.track_id IS NULL OR EXISTS(SELECT 1 FROM tracks t WHERE t.id=s.track_id AND t.source_status='present'))
 AND EXISTS(SELECT 1 FROM tracks t JOIN media m ON m.id=t.medium_id WHERE m.release_id=s.release_id AND t.source_status='present')
 AND lt.id IS NULL AND rt.id IS NULL AND lr.id IS NULL AND rr.id IS NULL
 AND (s.track_id IS NOT NULL OR NOT EXISTS (
 SELECT 1 FROM artist_assignment_overrides scoped JOIN tracks st ON st.id=scoped.track_id
 JOIN media sm ON sm.id=st.medium_id WHERE sm.release_id=s.release_id AND st.source_status='present'
 AND scoped.key_id IN (s.left_key_id,s.right_key_id)))
)`

func (g *ArtistGovernance) CandidateIgnoreExecutor(request ArtistCandidateIgnoreRequest) operations.Executor {
	return func(ctx context.Context, tx *sql.Tx, operationID string) (operations.Outcome, error) {
		if err := request.Normalize(); err != nil {
			return operations.Outcome{}, err
		}
		if err := lockArtistCatalog(ctx, tx); err != nil {
			return operations.Outcome{}, err
		}
		if request.ArtistID == request.OtherArtistID {
			return operations.Outcome{}, operations.Conflict("same_artist", "不能选择同一位艺人")
		}
		identities, err := lockArtists(ctx, tx, request.ArtistID, request.OtherArtistID)
		if err != nil {
			return operations.Outcome{}, err
		}
		artist, other := identities[request.ArtistID], identities[request.OtherArtistID]
		if err := checkArtist(artist, request.ExpectedRevision, false); err != nil {
			return operations.Outcome{}, err
		}
		if err := checkArtist(other, request.OtherExpectedRevision, false); err != nil {
			return operations.Outcome{}, err
		}
		raw, err := json.Marshal(request.KeyPairs)
		if err != nil {
			return operations.Outcome{}, err
		}
		var keysExist bool
		if err := tx.QueryRowContext(ctx, `SELECT NOT EXISTS (SELECT 1 FROM jsonb_to_recordset($1::jsonb) p(left_key_id uuid,right_key_id uuid)
   LEFT JOIN artist_name_keys l ON l.id=p.left_key_id LEFT JOIN artist_name_keys r ON r.id=p.right_key_id WHERE l.id IS NULL OR r.id IS NULL)`, string(raw)).Scan(&keysExist); err != nil {
			return operations.Outcome{}, err
		}
		if !keysExist {
			return operations.Outcome{}, operations.NotFound("not_found", "名字不存在")
		}
		var valid bool
		if err := tx.QueryRowContext(ctx, artistCandidateResolvedSQL+` SELECT NOT EXISTS (
   SELECT 1 FROM jsonb_to_recordset($1::jsonb) p(left_key_id uuid,right_key_id uuid) WHERE NOT EXISTS(
    SELECT 1 FROM resolved r WHERE r.left_key_id=p.left_key_id AND r.right_key_id=p.right_key_id
    AND ((r.left_artist_id=$2::uuid AND r.right_artist_id=$3::uuid) OR (r.left_artist_id=$3::uuid AND r.right_artist_id=$2::uuid))))`, string(raw), artist.ID, other.ID).Scan(&valid); err != nil {
			return operations.Outcome{}, err
		}
		if !valid {
			return operations.Outcome{}, artistScopeChanged()
		}
		before, after := emptyArtistState(), emptyArtistState()
		before.Artists = []artistIdentity{artist, other}
		rows, err := tx.QueryContext(ctx, `SELECT p.left_key_id::text,p.right_key_id::text,i.operation_id::text
   FROM jsonb_to_recordset($1::jsonb) p(left_key_id uuid,right_key_id uuid)
   LEFT JOIN artist_candidate_ignores i ON i.left_key_id=p.left_key_id AND i.right_key_id=p.right_key_id ORDER BY p.left_key_id,p.right_key_id`, string(raw))
		if err != nil {
			return operations.Outcome{}, err
		}
		changed := false
		for rows.Next() {
			var s artistIgnoreState
			if err := rows.Scan(&s.LeftKeyID, &s.RightKeyID, &s.OperationID); err != nil {
				rows.Close()
				return operations.Outcome{}, err
			}
			before.Ignores = append(before.Ignores, s)
			if (s.OperationID != nil) != *request.Ignored {
				changed = true
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return operations.Outcome{}, err
		}
		if !changed {
			return operations.Outcome{}, operations.Invalid("no_changes", "没有需要修改的内容")
		}
		if *request.Ignored {
			_, err = tx.ExecContext(ctx, `INSERT INTO artist_candidate_ignores(left_key_id,right_key_id,operation_id)
    SELECT p.left_key_id,p.right_key_id,$2::uuid FROM jsonb_to_recordset($1::jsonb) p(left_key_id uuid,right_key_id uuid)
    ON CONFLICT(left_key_id,right_key_id) DO NOTHING`, string(raw), operationID)
		} else {
			_, err = tx.ExecContext(ctx, `DELETE FROM artist_candidate_ignores i USING jsonb_to_recordset($1::jsonb) p(left_key_id uuid,right_key_id uuid)
    WHERE i.left_key_id=p.left_key_id AND i.right_key_id=p.right_key_id`, string(raw))
		}
		if err != nil {
			return operations.Outcome{}, err
		}
		if err := incrementArtists(ctx, tx, []string{artist.ID, other.ID}); err != nil {
			return operations.Outcome{}, err
		}
		for _, a := range before.Artists {
			a.Revision++
			after.Artists = append(after.Artists, a)
		}
		for _, i := range before.Ignores {
			if *request.Ignored && i.OperationID == nil {
				i.OperationID = &operationID
			}
			if !*request.Ignored {
				i.OperationID = nil
			}
			after.Ignores = append(after.Ignores, i)
		}
		after.Changes = map[string]any{"kind": operations.TypeArtistCandidateIgnore, "other_artist": artistNameSnapshot(other), "ignored": *request.Ignored}
		return operations.Outcome{TargetType: operations.TargetArtist, TargetID: artist.ID, Targets: artistTargets(other.ID), ExpectedRevision: artist.Revision, ResultRevision: artist.Revision + 1, Before: before, After: after,
			Response: map[string]any{"operation_id": operationID, "artist_id": artist.ID, "revision": artist.Revision + 1, "other_revision": other.Revision + 1, "ignored": *request.Ignored}}, nil
	}
}
