package program

import (
	"context"
	"database/sql"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
)

// 只有当前成品的拥有操作使用了下一轮的派生键，才能补写中断的终态。
// 本地覆盖、模型结果和以往代次的操作都不能替这次工作结项。
type mediaRecovery struct {
	Subject            string `json:"subject"`
	ID                 string `json:"id"`
	Release            string `json:"release_id,omitempty"`
	state              curation.WorkState
	input              string
	imageRank, bioRank int
	resolution         string
}

const releaseMediaBindings = `
 SELECT p.subject,p.id,p.subject step,x.operation_id,x.source,'release_external_artwork' kind,'' file
 FROM p JOIN release_external_artworks x ON x.release_id=p.id WHERE p.subject='artwork'
 UNION ALL
 SELECT p.subject,p.id,p.subject,x.operation_id,x.source,'track_external_lyrics',''
 FROM p JOIN track_external_lyrics x ON x.track_id=p.id WHERE p.subject='lyrics'`

const artistMediaBindings = `
 SELECT p.subject,p.id,'image' step,x.operation_id,x.source,'artist_external_artwork' kind,x.storage_key file
 FROM p JOIN artist_external_artworks x ON x.artist_id=p.id
 UNION ALL
 SELECT p.subject,p.id,'bio',x.operation_id,x.source,'artist_external_bio',''
 FROM p JOIN artist_external_bios x ON x.artist_id=p.id`

func (w *Worker) saveMediaProjection(ctx context.Context, tx *sql.Tx, projected []queueProjection, items []mediaRecovery, bindings string) error {
	if len(items) == 0 {
		return saveQueueProjection(ctx, tx, projected)
	}
	rows, err := tx.QueryContext(ctx, `WITH p AS MATERIALIZED (
 SELECT p.* FROM jsonb_to_recordset($1::jsonb) p(subject text,id uuid,release_id uuid)
 LEFT JOIN curation_stable_releases stable ON stable.id=p.release_id
 WHERE p.release_id IS NULL OR stable.id IS NOT NULL
 ), bindings AS (`+bindings+`)
 SELECT b.subject,b.id,b.step,b.source,b.file,COALESCE(o.after_state->b.step->>'external_id',''),o.idempotency_key
 FROM bindings b JOIN operations o ON o.id=b.operation_id AND o.operation_type=b.kind
 JOIN curation_batches batch ON batch.id=o.batch_id AND batch.tier='program'
 JOIN p ON p.subject=b.subject AND p.id=b.id
 WHERE o.origin->>'source'='curation' AND o.origin->>'provider_kind'='source' AND o.origin->>'provider'=b.source
 AND CASE WHEN b.subject='artist_media' THEN o.target_type='artist' AND o.target_id=p.id
 ELSE o.target_type='release' AND o.target_id=p.release_id END
 AND (b.subject<>'lyrics' OR o.after_state->>'track_id'=b.id::text)`, jsonValue(items))
	if err != nil {
		return err
	}
	defer rows.Close()
	type target struct{ subject, id string }
	pending := make(map[target]mediaRecovery, len(items))
	for _, item := range items {
		pending[target{item.Subject, item.ID}] = item
	}
	generations := make(map[target]int)
	for rows.Next() {
		var t target
		var step, provider, file, externalID, key string
		if err := rows.Scan(&t.subject, &t.id, &step, &provider, &file, &externalID, &key); err != nil {
			return err
		}
		item := pending[t]
		// 写入发生幂等冲突时允许升一代；封面缺文件还可再执行一次写入。
		limit := 2
		switch t.subject {
		case "artwork":
			limit = 4
		case "artist_media":
			// 图片和简介各有一次升代机会，只认当前可见层。
			limit = 3
			rank := item.bioRank
			if step == "image" {
				rank = item.imageRank
				if !w.writer.ArtworkExists(file) {
					continue
				}
			}
			if rank != catalog.ArtistMediaRank(provider) || externalID == "" {
				continue
			}
			step += "/" + provider + "/" + externalID
		}
		u := unit{subject: t.subject, id: t.id, ruleVersion: resourceRuleVersion}
		for advance := 1; advance <= limit; advance++ {
			u.generation = item.state.Generation + advance
			if u.key(step) == key {
				generations[t] = max(generations[t], u.generation)
				break
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	for t := range generations {
		if pending[t].resolution != "" {
			// 先取艺人目录闸门，再更新待定项、工作状态和排队投影。
			if err := catalog.AcquireArtistWriteGate(ctx, tx); err != nil {
				return err
			}
			break
		}
	}
	for t, generation := range generations {
		item := pending[t]
		if item.resolution != "" {
			if err := curation.ResolvePendingItems(ctx, tx, t.id, "artist_identity", item.resolution); err != nil {
				return err
			}
		}
		s := item.state
		s.Subject, s.TargetID, s.InputDigest, s.RuleVersion = t.subject, t.id, item.input, resourceRuleVersion
		s.Status, s.Attempts, s.Generation = "done", s.Attempts+1, generation
		s.RetryCount, s.NextAttemptAt, s.Result = 0, nil, nil
		if err := curation.SaveWorkState(ctx, tx, s); err != nil {
			return err
		}
	}
	for i, row := range projected {
		if _, recovered := generations[target{row.Subject, row.ID}]; recovered {
			projected[i].Need = false
		}
	}
	return saveQueueProjection(ctx, tx, projected)
}
