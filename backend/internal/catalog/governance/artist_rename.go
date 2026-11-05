package governance

import (
	"context"
	"database/sql"

	"github.com/qlqq/roomusic/backend/internal/operations"
)

func (g *ArtistGovernance) RenameExecutor(request ArtistRenameRequest) operations.Executor {
	return func(ctx context.Context, tx *sql.Tx, operationID string) (operations.Outcome, error) {
		if err := request.Normalize(); err != nil {
			return operations.Outcome{}, err
		}
		if err := lockArtistCatalog(ctx, tx); err != nil {
			return operations.Outcome{}, err
		}
		artists, err := lockArtists(ctx, tx, request.ArtistID)
		if err != nil {
			return operations.Outcome{}, err
		}
		beforeArtist := artists[request.ArtistID]
		if err := checkArtist(beforeArtist, request.ExpectedRevision, false); err != nil {
			return operations.Outcome{}, err
		}
		if (request.Mode == "auto" && beforeArtist.DisplayName == nil) || (request.Name != nil && beforeArtist.DisplayName != nil && *request.Name == *beforeArtist.DisplayName) {
			return operations.Outcome{}, operations.Invalid("no_changes", "没有需要修改的内容")
		}
		releases, _, err := affectedArtistReleases(ctx, tx, []string{request.ArtistID})
		if err != nil {
			return operations.Outcome{}, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO artist_overrides(artist_id,display_name,name_operation_id)
   VALUES($1::uuid,$2,$3::uuid) ON CONFLICT(artist_id) DO UPDATE SET display_name=EXCLUDED.display_name,name_operation_id=EXCLUDED.name_operation_id`, request.ArtistID, request.Name, operationID); err != nil {
			return operations.Outcome{}, err
		}
		if err := incrementArtists(ctx, tx, []string{request.ArtistID}); err != nil {
			return operations.Outcome{}, err
		}
		if err := g.refresh(ctx, tx, releases, []string{request.ArtistID}, nil); err != nil {
			return operations.Outcome{}, err
		}
		artists, err = loadArtistIdentities(ctx, tx, []string{request.ArtistID})
		if err != nil {
			return operations.Outcome{}, err
		}
		afterArtist := artists[request.ArtistID]
		before, after := emptyArtistState(), emptyArtistState()
		before.Artists = append(before.Artists, beforeArtist)
		after.Artists = append(after.Artists, afterArtist)
		fromSource, toSource := "auto", "auto"
		if beforeArtist.DisplayName != nil {
			fromSource = "manual"
		}
		if afterArtist.DisplayName != nil {
			toSource = "manual"
		}
		after.Changes = map[string]any{"kind": operations.TypeArtistRename, "from": map[string]string{"name": beforeArtist.Name, "source": fromSource}, "to": map[string]string{"name": afterArtist.Name, "source": toSource}}
		return operations.Outcome{TargetType: operations.TargetArtist, TargetID: request.ArtistID, ExpectedRevision: beforeArtist.Revision, ResultRevision: afterArtist.Revision, Before: before, After: after,
			Response: map[string]any{"operation_id": operationID, "artist_id": request.ArtistID, "revision": afterArtist.Revision, "display_name": afterArtist.Name, "name_source": toSource}}, nil
	}
}
