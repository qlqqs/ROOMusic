package stewardtools

import (
	"context"

	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
)

func issueReader(store *governance.Store) func(context.Context, input) (any, error) {
	return func(ctx context.Context, p input) (any, error) {
		issues, err := store.LoadReleaseIssues(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		fields := make([]any, 0, len(issues.Fields))
		for _, field := range issues.Fields {
			fields = append(fields, map[string]any{
				"field": field.Field, "value": field.Value, "reason_code": field.ReasonCode,
				"candidates": field.Candidates, "reviewable": field.Reviewable,
				"basis": field.Basis, "status": field.Status, "review": presentReview(field.Review),
			})
		}
		var grouping, missing any
		if g := issues.Grouping; g != nil {
			grouping = map[string]any{
				"reason_codes": g.ReasonCodes, "source_refs": g.SourceRefs, "truncated": g.Truncated,
				"basis": g.Basis, "siblings": g.Siblings, "status": g.Status, "review": presentReview(g.Review),
			}
		}
		if m := issues.MissingTracks; m != nil {
			start, end := pageBounds(p, len(m.Tracks))
			missing = map[string]any{
				"basis": m.Basis, "count": m.Count, "tracks": m.Tracks[start:end], "truncated": m.Truncated,
				"status": m.Status, "review": presentReview(m.Review), "pagination": pagination(p, int64(len(m.Tracks))),
			}
		}
		// 核对所需的依据令牌进入工具结果，核对人的身份仍不外发。
		return map[string]any{
			"release_id": issues.ReleaseID, "group_id": issues.GroupID, "revision": issues.Revision,
			"release": issues.Release, "edition": issues.Edition, "fields": fields,
			"grouping": grouping, "missing_tracks": missing,
		}, nil
	}
}

func presentReview(review *governance.IssueReviewInfo) any {
	if review == nil {
		return nil
	}
	return map[string]any{"operation_id": review.OperationID, "reviewed_at": review.ReviewedAt}
}
