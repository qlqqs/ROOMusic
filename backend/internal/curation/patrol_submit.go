package curation

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
	"github.com/qlqq/roomusic/backend/internal/operations"
)

type PatrolResult struct {
	ID         string `json:"id"`
	Canonical  string `json:"canonical"`
	Confidence string `json:"confidence"`
}
type PatrolIssue struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Suggested string `json:"suggested"`
}
type PatrolSubmission struct {
	ItemID  string         `json:"item_id"`
	Results []PatrolResult `json:"results"`
	Issues  []PatrolIssue  `json:"issues"`
}
type patrolAnswer struct {
	original, latin, confidence string
	issues                      []PatrolIssue
}

func (in PatrolSubmission) answers(batch patrolBatch) (map[string]*patrolAnswer, error) {
	invalid := operations.Invalid("invalid_input", "巡检结果须与本批名字、规范身份格式及问题类型一致")
	answers := map[string]*patrolAnswer{}
	albumTitles := map[string]bool{}
	for _, name := range batch.Names {
		answers[name.ID] = &patrolAnswer{issues: []PatrolIssue{}}
		if batch.Kind == "album" {
			albumTitles[name.Name] = true
			for _, ref := range name.Albums {
				albumTitles[ref] = true
			}
		}
	}
	if batch.Kind == "artist" && len(in.Results) != len(batch.Names) || batch.Kind == "album" && len(in.Results) != 0 {
		return nil, invalid
	}
	for _, r := range in.Results {
		a := answers[r.ID]
		orig, latin, ok := strings.Cut(r.Canonical, "|")
		orig, latin = strings.TrimSpace(orig), strings.TrimSpace(latin)
		if a == nil || a.confidence != "" || !ok || orig == "" || strings.ContainsRune(r.Canonical, 0) || strings.Contains(latin, "|") || len(orig) > 4096 || len(latin) > 4096 || (r.Confidence != "low" && r.Confidence != "high") {
			return nil, invalid
		}
		a.original, a.latin, a.confidence = orig, latin, r.Confidence
	}
	for _, issue := range in.Issues {
		a := answers[issue.ID]
		issue.Suggested = strings.TrimSpace(issue.Suggested)
		if a == nil || issue.Suggested == "" || strings.ContainsRune(issue.Suggested, 0) || len(issue.Suggested) > 4096 {
			return nil, invalid
		}
		switch issue.Kind {
		case "artist_alias":
			if batch.Kind != "artist" {
				return nil, invalid
			}
		case "album_variant":
			if batch.Kind != "album" || !albumTitles[issue.Suggested] {
				return nil, invalid
			}
		case "typo":
		default:
			return nil, invalid
		}
		a.issues = append(a.issues, issue)
	}
	for _, n := range batch.Names {
		a := answers[n.ID]
		if batch.Kind == "album" && len(a.issues) > 0 {
			// 同一个标题可有多条问题；只有建议一致时才提供检索名。
			a.original = a.issues[0].Suggested
			for _, issue := range a.issues[1:] {
				if issue.Suggested != a.original {
					a.original = ""
					break
				}
			}
		}
		if a.confidence == "low" {
			a.original, a.latin = n.Name, ""
		}
	}
	return answers, nil
}

func (w *Writer) SubmitPatrol(ctx context.Context, item PendingItem, in PatrolSubmission) error {
	run, ok := ModelRunFrom(ctx)
	if !ok || run.TaskKind != "patrol_local" || run.PendingItemID != item.ID || item.Kind != "name_patrol" || in.ItemID != item.ID {
		return operations.Conflict("out_of_batch", "巡检项不属于当前运行")
	}
	if strings.TrimSpace(run.Model) == "" {
		return operations.Invalid("invalid_input", "巡检模型无效")
	}
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = patrolLock(ctx, tx); err != nil {
		return err
	}
	if err = CheckMatchItem(ctx, tx, item, run.ConversationID, false); err != nil {
		return err
	}
	settings, err := loadSettings(ctx, tx)
	if err != nil {
		return err
	}
	if !settings.Active || settings.EnabledBy.ID != run.ActorID || run.ClaimDigests[item.ID] != item.ModelClaimDigest() {
		return operations.Conflict("item_stale", "巡检领取依据已变化")
	}
	if settings.Paused {
		return operations.Conflict("curation_paused", "自动整理已暂停")
	}
	var scanning bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT FROM scan_runs WHERE status='running')`).Scan(&scanning); err != nil {
		return err
	}
	if scanning {
		return operations.Conflict("scan_running", "曲库扫描进行中")
	}
	var batch patrolBatch
	if err = json.Unmarshal(item.Evidence, &batch); err != nil {
		return err
	}
	answers, err := in.answers(batch)
	if err != nil {
		return err
	}
	// 提交时再与当前名录对齐。变化的名字可重新入队，其余名字照常保存。
	rows, err := tx.QueryContext(ctx, patrolBatchCatalog+`SELECT DISTINCT n.id,p.original_name,COALESCE(p.confidence,'') FROM names n JOIN curation_patrol_names p ON p.id=n.id WHERE p.pending_id=$1`, item.ID)
	if err != nil {
		return err
	}
	previousSearch := map[string]string{}
	for rows.Next() {
		var id, original, confidence string
		if err = rows.Scan(&id, &original, &confidence); err != nil {
			rows.Close()
			return err
		}
		if confidence == "low" {
			original = ""
		}
		previousSearch[id] = original
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	var changed []string
	for _, n := range batch.Names {
		previous, live := previousSearch[n.ID]
		if !live {
			continue
		}
		a := answers[n.ID]
		issues, err := json.Marshal(a.issues)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE curation_patrol_names SET original_name=$3,latin_name=$4,
 original_key=$5,latin_key=$6,confidence=NULLIF($7,''),issues=$8,model=$9,prompt_version=$10,patrolled_at=clock_timestamp(),needs_patrol=false
 WHERE id=$1 AND pending_id=$2`, n.ID, item.ID, a.original, a.latin, textnorm.Text(a.original), textnorm.Text(a.latin), a.confidence, issues, run.Model, item.RuleVersion)
		if err != nil {
			return err
		}
		current := a.original
		if a.confidence == "low" || current == n.Name {
			current = ""
		}
		if previous == n.Name {
			previous = ""
		}
		if previous != current {
			changed = append(changed, n.ID)
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE curation_patrol_names SET pending_id=NULL WHERE pending_id=$1 AND needs_patrol`, item.ID); err != nil {
		return err
	}
	if err = w.patrolVerifications(ctx, tx, item.ID); err != nil {
		return err
	}
	if len(changed) > 0 {
		// 先锁待定项，再锁发行工作状态，锁序与程序结论和模型结项一致。
		const affected = patrolMembership + `, affected AS (SELECT DISTINCT m.release_id FROM members m
 JOIN curation_work_states s ON s.target_id=m.release_id AND s.subject='release'
 WHERE m.name_id=ANY($1::text[]) AND s.status IN ('uncertain','no_match','try_later','unlisted','pending')) `
		_, err = tx.ExecContext(ctx, affected+`UPDATE curation_pending_items p SET status='resolved',resolution='patrol_requery',resolved_at=now(),updated_at=now()
 FROM affected a WHERE p.target_id=a.release_id AND p.kind IN ('release_match','recording_match')
 AND p.status IN ('waiting_model','waiting_route','shelved')`, changed)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, affected+`UPDATE curation_work_states s SET needs_work=true,patrol_revision=patrol_revision+1,requery_match_version=match_rule_version,
 next_attempt_at=CASE WHEN status IN ('try_later','unlisted') THEN now() ELSE next_attempt_at END,updated_at=now()
 FROM affected a WHERE s.subject='release' AND s.target_id=a.release_id AND s.status IN ('uncertain','no_match','try_later','unlisted','pending')`, changed)
		if err != nil {
			return err
		}
	}
	if err = ResolvePendingItem(ctx, tx, item.ID, run.ConversationID, "patrolled"); err != nil {
		return err
	}
	return tx.Commit()
}

// 用批内名字的投影查受影响的发行，再在真实有效署名、扫描标题上复核。
const patrolBatchCatalog = patrolMembership + `,scope AS (
 SELECT DISTINCT m.release_id AS id FROM members m JOIN curation_patrol_names p ON p.id=m.name_id WHERE p.pending_id=$1)` + patrolFacts

// 核对项只引用当前发行和署名。专辑写法和明确错字交 E 判断，跨块身份按规范键连通。
func (w *Writer) patrolVerifications(ctx context.Context, tx *sql.Tx, batch string) error {
	var candidates []VerificationCandidate
	rows, err := tx.QueryContext(ctx, patrolBatchCatalog+`SELECT DISTINCT n.release_id,COALESCE(c.track_id::text,''),
 CASE WHEN n.kind='album' THEN 'title' WHEN c.track_id IS NOT NULL THEN 'artist' ELSE 'album_artist' END,
 n.name,i->>'suggested'
 FROM names n JOIN curation_patrol_names p USING(id)
 CROSS JOIN LATERAL jsonb_array_elements(p.issues) i
 LEFT JOIN credits c ON n.kind='artist' AND c.artist_id=n.artist_id AND c.release_id=n.release_id AND c.name=n.name
 AND c.role IN ('album_artist','track_artist','release_artist')
 WHERE p.pending_id=$1 AND p.patrolled_at IS NOT NULL AND i->>'kind' IN ('typo','album_variant')
 AND (n.kind='album' OR c.artist_id IS NOT NULL) ORDER BY 1,2,3,4,5`, batch)
	if err != nil {
		return err
	}
	for rows.Next() {
		var v VerificationCandidate
		if err = rows.Scan(&v.ReleaseID, &v.TrackID, &v.Field, &v.Before, &v.After); err != nil {
			rows.Close()
			return err
		}
		candidates = append(candidates, v)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	rows, err = tx.QueryContext(ctx, `WITH RECURSIVE `+patrolMembershipCTEs+`, connected(seed,id) AS (
 SELECT id,id FROM curation_patrol_names p WHERE pending_id=$1 AND confidence='high' AND NOT needs_patrol AND patrolled_at IS NOT NULL
 AND EXISTS (SELECT FROM members m WHERE m.name_id=p.id)
 UNION
 SELECT c.seed,b.id FROM connected c JOIN curation_patrol_names a ON a.id=c.id JOIN curation_patrol_names b ON
 (a.original_key<>'' AND a.original_key=b.original_key OR a.latin_key<>'' AND a.latin_key=b.latin_key)
 WHERE b.confidence='high' AND NOT b.needs_patrol AND b.patrolled_at IS NOT NULL AND EXISTS(SELECT FROM members m WHERE m.name_id=b.id)
 ), pairs AS (SELECT DISTINCT ON (a.artist_id,b.artist_id) m.release_id,a.name AS before,b.name AS after,a.artist_id first_artist,b.artist_id second_artist
 FROM connected ca JOIN connected cb USING(seed) JOIN curation_patrol_names a ON a.id=ca.id JOIN curation_patrol_names b ON b.id=cb.id
 JOIN LATERAL (SELECT release_id FROM members WHERE name_id=a.id ORDER BY release_id LIMIT 1) m ON true
 WHERE a.artist_id<b.artist_id ORDER BY a.artist_id,b.artist_id,a.name,b.name)
 SELECT p.release_id,p.before,p.after,p.first_artist,p.second_artist,COALESCE((SELECT min(l.external_id)
 FROM external_links l WHERE l.object_type='artist' AND l.source='musicbrainz' AND l.object_id IN (p.first_artist,p.second_artist)
 HAVING count(DISTINCT l.external_id)=1),'') FROM pairs p`, batch)
	if err != nil {
		return err
	}
	for rows.Next() {
		v := VerificationCandidate{Field: "artist_merge"}
		if err = rows.Scan(&v.ReleaseID, &v.Before, &v.After, &v.ArtistA, &v.ArtistB, &v.ExternalID); err != nil {
			rows.Close()
			return err
		}
		candidates = append(candidates, v)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	if len(candidates) == 0 {
		return nil
	}
	// 分页读取真实目录输入，避免对热门艺人的全部曲目同时保留完整证据。
	var ids []string
	perRelease := map[string][]VerificationCandidate{}
	for _, v := range candidates {
		if len(perRelease[v.ReleaseID]) == 0 {
			ids = append(ids, v.ReleaseID)
		}
		perRelease[v.ReleaseID] = append(perRelease[v.ReleaseID], v)
	}
	for start := 0; start < len(ids); start += 32 {
		page := ids[start:min(start+32, len(ids))]
		evidence, err := catalog.LoadCurationEvidence(ctx, tx, page, w.ArtworkExists)
		if err != nil {
			return err
		}
		for _, id := range page {
			e, ok := evidence[id]
			if !ok {
				continue
			}
			digest := ReleaseIdentityDigest(e)
			trackArtists := make(map[string]string, len(e.Tracks))
			for _, t := range e.Tracks {
				trackArtists[t.ID] = t.Artist
			}
			for _, v := range perRelease[id] {
				kind := "verify_name"
				switch v.Field {
				case "artist_merge":
					kind = "verify_artist_merge"
				case "album_artist", "artist":
					field := e.AlbumArtist
					if v.TrackID != "" {
						field = trackArtists[v.TrackID]
					}
					if v.Before != field {
						// 人名可能只是合署名的一部分；核对项保留其余原文及分隔符。
						if strings.Count(field, v.Before) != 1 || !slices.Contains(metadata.CanonicalArtistNames(field), v.Before) {
							continue
						}
						v.After = strings.Replace(field, v.Before, v.After, 1)
						v.Before = field
					}
				}
				if err = QueueVerification(ctx, tx, kind, digest, v); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
