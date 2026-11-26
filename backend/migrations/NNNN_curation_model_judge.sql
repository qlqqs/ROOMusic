ALTER TABLE curation_release_match_results
    ADD COLUMN snapshot_id uuid,
    ADD COLUMN candidates jsonb NOT NULL DEFAULT '[]',
    ADD COLUMN selection jsonb;

CREATE TABLE curation_artist_negative_pairs (
    name_low text NOT NULL, name_high text NOT NULL,
    reason text NOT NULL, model text NOT NULL,
    legacy boolean NOT NULL DEFAULT false,
    decided_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(name_low,name_high)
);

CREATE TABLE curation_match_names (
    release_id uuid NOT NULL REFERENCES releases(id) ON DELETE CASCADE,
    target_type text NOT NULL CHECK (target_type IN ('release','track')),
    target_id uuid NOT NULL, field text NOT NULL,
    mb_value text NOT NULL, external_id text NOT NULL,
    basis_link_id uuid NOT NULL REFERENCES external_links(id) ON DELETE CASCADE,
    basis_operation_id uuid NOT NULL REFERENCES operations(id),
    PRIMARY KEY(release_id,target_type,target_id,field,basis_link_id)
);

UPDATE steward_plans p SET status='superseded',updated_at=now()
FROM steward_conversations c WHERE p.conversation_id=c.id AND c.kind='curation'
AND p.background AND p.status='pending';

-- 保留已采纳的操作和历史结论，只撤销旧工作项的继续执行资格。
-- 旧版不同身份的观察以明确 verdict 保存，先留住原文，再由 Go 使用同一套文本字表生成名字键。
WITH negatives AS (
 SELECT coalesce(p.evidence->>'before',p.evidence->'local'->>'name') a,
 coalesce(p.evidence->>'after',p.evidence->'external'->>'credit_name') b,
 coalesce(x.input->>'reason','') reason,x.provider,x.created_at
 FROM curation_model_exceptions x JOIN curation_pending_items p ON p.id=x.pending_item_id
 WHERE x.input->>'verdict'='different' AND p.kind IN ('verify_artist_credit','verify_artist_merge'))
INSERT INTO curation_artist_negative_pairs(name_low,name_high,reason,model,decided_at,legacy)
SELECT least(a,b),greatest(a,b),reason,provider,created_at,true FROM negatives
WHERE nullif(a,'') IS NOT NULL AND nullif(b,'') IS NOT NULL ORDER BY created_at
ON CONFLICT DO NOTHING;

DELETE FROM curation_model_exceptions WHERE status IN ('open','declined')
 OR (status='accepting' AND cardinality(operation_ids)=0 AND NOT EXISTS
 (SELECT FROM operations o WHERE o.origin->>'conversation_id'=curation_model_exceptions.conversation_id::text));

UPDATE curation_pending_items SET status='resolved',resolution='superseded',
 claimed_at=NULL,next_attempt_at=NULL,resolved_at=now(),updated_at=now()
WHERE status<>'resolved' AND (kind IN ('name_patrol','artist_identity') OR route IN ('web','lookup','select'));

UPDATE curation_pending_items SET status='waiting_model',claimed_at=NULL,conversation_id=NULL,
 next_attempt_at=NULL,review_retries=0
WHERE status='in_progress' AND kind IN ('verify_name','verify_artist_credit','verify_artist_merge','release_merge');

-- 删除已失去执行入口的活动项，历史已采纳操作依赖的待办继续保留。
DELETE FROM curation_pending_items p WHERE p.resolution='superseded'
 AND (p.kind IN ('name_patrol','artist_identity') OR p.route='web')
 AND NOT EXISTS(SELECT FROM curation_model_exceptions x WHERE x.pending_item_id=p.id);
