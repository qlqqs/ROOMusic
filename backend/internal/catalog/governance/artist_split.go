package governance

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func (g *ArtistGovernance) SplitExecutor(request ArtistSplitRequest) operations.Executor {
	return func(ctx context.Context, tx *sql.Tx, operationID string) (operations.Outcome, error) {
		if err := request.Normalize(); err != nil {
			return operations.Outcome{}, err
		}
		if err := lockArtistCatalog(ctx, tx); err != nil {
			return operations.Outcome{}, err
		}
		ids := []string{request.ArtistID}
		if request.Target.Kind == "restore" {
			ids = append(ids, request.Target.ArtistID)
		}
		identities, err := lockArtists(ctx, tx, ids...)
		if err != nil {
			return operations.Outcome{}, err
		}
		source := identities[request.ArtistID]
		if err := checkArtist(source, request.ExpectedRevision, false); err != nil {
			return operations.Outcome{}, err
		}
		var target artistIdentity
		if request.Target.Kind == "restore" {
			target = identities[request.Target.ArtistID]
			if err := checkArtist(target, request.Target.ExpectedRevision, true); err != nil {
				return operations.Outcome{}, err
			}
			if target.RedirectArtistID == nil || *target.RedirectArtistID != source.ID {
				return operations.Outcome{}, artistScopeChanged()
			}
		}
		plan, err := buildArtistSplitPlan(ctx, tx, source, request.ArtistSplitScope)
		if err != nil {
			return operations.Outcome{}, err
		}
		if plan.preview.Basis != request.Basis {
			return operations.Outcome{}, artistScopeChanged()
		}
		before := emptyArtistState()
		before.Artists = append(before.Artists, source)
		before.Bindings = plan.bindings
		before.Assignments = plan.assignments
		if request.Target.Kind == "restore" {
			before.Artists = append(before.Artists, target)
		}
		if err := boundedArtistState(before); err != nil {
			return operations.Outcome{}, err
		}
		mapping := map[string]string{}
		if request.Target.Kind == "new" {
			var initial string
			err = tx.QueryRowContext(ctx, `SELECT spelling FROM artist_names WHERE key_id=ANY($1::text[]::uuid[]) ORDER BY spelling COLLATE "C",id LIMIT 1`, request.NameKeyIDs).Scan(&initial)
			if err != nil {
				return operations.Outcome{}, err
			}
			if request.Target.DisplayName != nil {
				initial = *request.Target.DisplayName
			}
			target = artistIdentity{ID: uuid.New(), Name: initial, Revision: 1}
			if _, err = tx.ExecContext(ctx, `INSERT INTO artists(id,initial_name) VALUES($1::uuid,$2)`, target.ID, initial); err != nil {
				return operations.Outcome{}, err
			}
			if request.Target.DisplayName != nil {
				if _, err = tx.ExecContext(ctx, `INSERT INTO artist_overrides(artist_id,display_name,name_operation_id) VALUES($1::uuid,$2,$3::uuid)`, target.ID, initial, operationID); err != nil {
					return operations.Outcome{}, err
				}
			}
		} else {
			if _, err = tx.ExecContext(ctx, `UPDATE artist_overrides SET redirect_artist_id=NULL,redirect_operation_id=$2::uuid WHERE artist_id=$1::uuid`, target.ID, operationID); err != nil {
				return operations.Outcome{}, err
			}
			if err := incrementArtists(ctx, tx, []string{target.ID}); err != nil {
				return operations.Outcome{}, err
			}
			mapping[target.ID] = target.ID
		}
		if len(plan.bindings) > 0 {
			keys := []string{}
			for _, b := range plan.bindings {
				keys = append(keys, b.KeyID)
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO artist_name_bindings(key_id,artist_id,operation_id) SELECT unnest($1::text[]::uuid[]),$2::uuid,$3::uuid
    ON CONFLICT(key_id) DO UPDATE SET artist_id=EXCLUDED.artist_id,operation_id=EXCLUDED.operation_id`, keys, target.ID, operationID); err != nil {
				return operations.Outcome{}, err
			}
		}
		if err := writeArtistAssignments(ctx, tx, plan.assignments, target.ID, operationID); err != nil {
			return operations.Outcome{}, err
		}
		if err := incrementArtists(ctx, tx, []string{source.ID}); err != nil {
			return operations.Outcome{}, err
		}
		if err := g.refresh(ctx, tx, plan.releases, []string{source.ID, target.ID}, mapping); err != nil {
			return operations.Outcome{}, err
		}
		updated, err := loadArtistIdentities(ctx, tx, []string{source.ID, target.ID})
		if err != nil {
			return operations.Outcome{}, err
		}
		after := emptyArtistState()
		after.Artists = []artistIdentity{updated[source.ID], updated[target.ID]}
		for _, b := range plan.bindings {
			b.ArtistID = target.ID
			b.OperationID = &operationID
			after.Bindings = append(after.Bindings, b)
		}
		for _, a := range plan.assignments {
			a.ArtistID = target.ID
			a.OperationID = &operationID
			after.Assignments = append(after.Assignments, a)
		}
		after.Changes = map[string]any{"kind": operations.TypeArtistSplit, "source": artistNameSnapshot(source), "target": artistNameSnapshot(updated[target.ID]), "mode": request.Mode, "name_count": len(request.NameKeyIDs), "release_count": plan.preview.Scope.ReleaseCount, "track_count": plan.preview.Scope.TrackCount}
		if err := boundedArtistState(after); err != nil {
			return operations.Outcome{}, err
		}
		return operations.Outcome{TargetType: operations.TargetArtist, TargetID: source.ID, Targets: artistTargets(target.ID), ExpectedRevision: source.Revision, ResultRevision: source.Revision + 1, Before: before, After: after,
			Response: map[string]any{"operation_id": operationID, "artist_id": source.ID, "revision": source.Revision + 1, "target": map[string]any{"id": target.ID, "revision": updated[target.ID].Revision}, "scope_summary": plan.preview.Scope}}, nil
	}
}

func writeArtistAssignments(ctx context.Context, tx *sql.Tx, assignments []artistAssignmentState, target, operation string) error {
	if len(assignments) == 0 {
		return nil
	}
	raw, err := json.Marshal(assignments)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO artist_assignment_overrides(release_id,track_id,key_id,artist_id,operation_id)
 SELECT r.release_id,r.track_id,r.key_id,$2::uuid,$3::uuid
 FROM jsonb_to_recordset($1::jsonb) r(release_id uuid,track_id uuid,key_id uuid)
 ON CONFLICT(track_id,release_id,key_id) DO UPDATE SET artist_id=EXCLUDED.artist_id,operation_id=EXCLUDED.operation_id`, string(raw), target, operation)
	return err
}
