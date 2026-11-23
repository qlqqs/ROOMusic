package curation

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type JoinRequest struct {
	ExpectedRevision            int64
	ConfirmedBy, ConversationID string
	ExternalSource              string
	ReleaseID, GroupID          string
	Source                      Source
	BatchID, Key, RequestID     string
}

func (w *Writer) JoinGroup(ctx context.Context, request JoinRequest) (governance.GroupChangeResult, error) {
	id, group := strings.ToLower(request.ReleaseID), strings.ToLower(request.GroupID)
	_, sourceErr := musicBrainzSource(request.ExternalSource, request.Source)
	if !uuid.Valid(group) || sourceErr != nil {
		return governance.GroupChangeResult{}, operations.Invalid("invalid_input", "目标专辑或归组来源无效")
	}
	fp := map[string]any{"type": operations.TypeReleaseGroupChange, "release_id": id, "action": governance.GroupJoin, "group_id": group, "provider": request.Source.Provider}
	if request.ExpectedRevision != 0 {
		fp["expected_revision"] = request.ExpectedRevision
	}
	fingerprint := operations.Fingerprint(fp)
	spec, err := w.sourceSpec(ctx, operations.TypeReleaseGroupChange, id, fingerprint, request.Source, request.BatchID, request.Key, request.RequestID, request.ConfirmedBy, request.ConversationID)
	if err != nil {
		return governance.GroupChangeResult{}, err
	}
	result, err := operations.Run(ctx, w.db, spec, func(ctx context.Context, tx *sql.Tx, operationID string) (operations.Outcome, error) {
		release, err := governance.LockAutoRelease(ctx, tx, id)
		if err != nil {
			return operations.Outcome{}, err
		}
		if err := checkWrite(ctx, tx, spec, request.ConfirmedBy, id, request.ExpectedRevision, release.EditRevision()); err != nil {
			return operations.Outcome{}, err
		}
		if err := checkGroupEvidence(ctx, tx, id, group); err != nil {
			return operations.Outcome{}, err
		}
		var manual bool
		err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT (o.origin->>'source') IS DISTINCT FROM 'curation'
 FROM operations o WHERE o.operation_type='release_group_change' AND o.target_type='release' AND o.target_id=$1
 AND NOT EXISTS(SELECT 1 FROM operations r WHERE r.reverts_operation_id=o.id)
 ORDER BY o.result_revision DESC LIMIT 1),false)`, id).Scan(&manual)
		if err != nil {
			return operations.Outcome{}, err
		}
		if manual {
			return operations.Outcome{}, operations.Conflict("manual_structure", "发行最近的归组由人工决定")
		}
		blocked, err := suppressionExists(ctx, tx, "release", id, "group_join", hashExternalValue(group))
		if err != nil {
			return operations.Outcome{}, err
		}
		if blocked {
			return operations.Outcome{}, operations.Conflict("nothing_to_apply", "已标记不再归入这个专辑")
		}
		change := governance.GroupChangeRequest{ReleaseID: id, ExpectedRevision: release.EditRevision(), Action: governance.GroupJoin, GroupID: group}
		return governance.GroupChangeExecutor(change)(ctx, tx, operationID)
	})
	if err != nil {
		return governance.GroupChangeResult{}, err
	}
	var response governance.GroupChangeResult
	err = json.Unmarshal(result.Response, &response)
	return response, err
}

func checkGroupEvidence(ctx context.Context, tx *sql.Tx, releaseID, groupID string) error {
	var strong bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(
 SELECT 1 FROM external_links l
 JOIN operations o ON o.id=l.operation_id AND o.operation_type='external_link' AND o.target_type='release' AND o.target_id=l.object_id
 CROSS JOIN LATERAL jsonb_to_recordset(o.after_state->'links') AS item(object_type text,object_id uuid,external_id text,external_group_id text)
 JOIN external_links g ON g.source='musicbrainz' AND g.object_type='release_group' AND g.object_id=$2 AND g.external_id=item.external_group_id
 WHERE l.source='musicbrainz' AND l.object_type='release' AND l.object_id=$1
 AND item.object_type=l.object_type AND item.object_id=l.object_id AND item.external_id=l.external_id AND item.external_group_id<>''
 FOR SHARE OF l,g)`, releaseID, groupID).Scan(&strong)
	if err == nil && !strong {
		return operations.Conflict("no_strong_evidence", "发行与目标专辑缺少一致的外部对应")
	}
	return err
}

func (w *Writer) SuppressGroupJoin(ctx context.Context, tx *sql.Tx, releaseID, groupID, revertID, actorID string) error {
	return insertSuppression(ctx, tx, "release", releaseID, "group_join", hashExternalValue(groupID), map[string]string{"group_id": groupID}, revertID, actorID)
}
