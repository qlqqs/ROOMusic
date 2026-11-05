package governance

import (
	"context"
	"database/sql"

	"github.com/qlqq/roomusic/backend/internal/operations"
)

func (g *ArtistGovernance) MergeExecutor(request ArtistMergeRequest) operations.Executor {
	return func(ctx context.Context, tx *sql.Tx, operationID string) (operations.Outcome, error) {
		if err := request.Normalize(); err != nil {
			return operations.Outcome{}, err
		}
		if err := lockArtistCatalog(ctx, tx); err != nil {
			return operations.Outcome{}, err
		}
		if request.ArtistID == request.SourceArtistID {
			return operations.Outcome{}, operations.Conflict("same_artist", "不能合并同一位艺人")
		}
		identities, err := lockArtists(ctx, tx, request.ArtistID, request.SourceArtistID)
		if err != nil {
			return operations.Outcome{}, err
		}
		target, source := identities[request.ArtistID], identities[request.SourceArtistID]
		if err := checkArtist(target, request.ExpectedRevision, false); err != nil {
			return operations.Outcome{}, err
		}
		if err := checkArtist(source, request.SourceExpectedRevision, false); err != nil {
			return operations.Outcome{}, err
		}
		before, err := loadArtistMappings(ctx, tx, source.ID)
		if err != nil {
			return operations.Outcome{}, err
		}
		nameKeys := map[string]bool{}
		for _, binding := range before.Bindings {
			nameKeys[binding.KeyID] = true
		}
		for _, assignment := range before.Assignments {
			nameKeys[assignment.KeyID] = true
		}
		if len(nameKeys) > maxArtistBindings {
			return operations.Outcome{}, artistScopeTooLarge()
		}
		before.Artists = []artistIdentity{target, source}
		// 更早身份一并扁平化；保留它们自己的显示名覆盖和人工版本。
		rows, err := tx.QueryContext(ctx, `SELECT artist_id::text FROM artist_overrides WHERE redirect_artist_id=$1::uuid ORDER BY artist_id LIMIT 10001`, source.ID)
		if err != nil {
			return operations.Outcome{}, err
		}
		redirects := []string{}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return operations.Outcome{}, err
			}
			redirects = append(redirects, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return operations.Outcome{}, err
		}
		if len(redirects) > maxArtistOverrides {
			return operations.Outcome{}, artistScopeTooLarge()
		}
		if len(redirects) > 0 {
			more, err := loadArtistIdentities(ctx, tx, redirects)
			if err != nil {
				return operations.Outcome{}, err
			}
			for _, id := range redirects {
				before.Artists = append(before.Artists, more[id])
			}
		}
		releases, _, err := affectedArtistReleases(ctx, tx, []string{target.ID, source.ID})
		if err != nil {
			return operations.Outcome{}, err
		}
		sourceReleases, sourceTracks, err := affectedArtistReleases(ctx, tx, []string{source.ID})
		if err != nil {
			return operations.Outcome{}, err
		}
		if err := boundedArtistState(before); err != nil {
			return operations.Outcome{}, err
		}
		keys := make([]string, 0, len(before.Bindings))
		for _, b := range before.Bindings {
			keys = append(keys, b.KeyID)
		}
		if len(keys) > 0 {
			_, err = tx.ExecContext(ctx, `INSERT INTO artist_name_bindings(key_id,artist_id,operation_id)
   SELECT unnest($1::text[]::uuid[]),$2::uuid,$3::uuid ON CONFLICT(key_id) DO UPDATE SET artist_id=EXCLUDED.artist_id,operation_id=EXCLUDED.operation_id`, keys, target.ID, operationID)
			if err != nil {
				return operations.Outcome{}, err
			}
		}
		oldIDs := append([]string{source.ID}, redirects...)
		if _, err = tx.ExecContext(ctx, `UPDATE artist_assignment_overrides SET artist_id=$1::uuid,operation_id=$2::uuid WHERE artist_id=ANY($3::text[]::uuid[])`, target.ID, operationID, oldIDs); err != nil {
			return operations.Outcome{}, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO artist_overrides(artist_id,redirect_artist_id,redirect_operation_id)
   SELECT unnest($1::text[]::uuid[]),$2::uuid,$3::uuid ON CONFLICT(artist_id) DO UPDATE
   SET redirect_artist_id=EXCLUDED.redirect_artist_id,redirect_operation_id=EXCLUDED.redirect_operation_id`, oldIDs, target.ID, operationID); err != nil {
			return operations.Outcome{}, err
		}
		if err := incrementArtists(ctx, tx, []string{source.ID, target.ID}); err != nil {
			return operations.Outcome{}, err
		}
		mapping := map[string]string{}
		for _, id := range oldIDs {
			mapping[id] = target.ID
		}
		if err := g.refresh(ctx, tx, releases, append([]string{target.ID}, oldIDs...), mapping); err != nil {
			return operations.Outcome{}, err
		}
		after := emptyArtistState()
		updated, err := loadArtistIdentities(ctx, tx, append([]string{target.ID}, oldIDs...))
		if err != nil {
			return operations.Outcome{}, err
		}
		for _, a := range before.Artists {
			after.Artists = append(after.Artists, updated[a.ID])
		}
		for _, b := range before.Bindings {
			b.ArtistID = target.ID
			b.OperationID = &operationID
			after.Bindings = append(after.Bindings, b)
		}
		for _, a := range before.Assignments {
			a.ArtistID = target.ID
			a.OperationID = &operationID
			after.Assignments = append(after.Assignments, a)
		}
		after.Changes = map[string]any{"kind": operations.TypeArtistMerge, "source": artistNameSnapshot(source), "target": artistNameSnapshot(target), "name_count": len(nameKeys), "release_count": len(sourceReleases), "track_count": sourceTracks}
		if err := boundedArtistState(after); err != nil {
			return operations.Outcome{}, err
		}
		return operations.Outcome{TargetType: operations.TargetArtist, TargetID: target.ID, Targets: artistTargets(oldIDs...), ExpectedRevision: target.Revision, ResultRevision: target.Revision + 1, Before: before, After: after,
			Response: map[string]any{"operation_id": operationID, "artist_id": target.ID, "revision": target.Revision + 1, "source": map[string]any{"id": source.ID, "revision": source.Revision + 1, "redirect_artist_id": target.ID}}}, nil
	}
}
