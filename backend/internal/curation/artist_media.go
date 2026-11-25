package curation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

const PrimaryArtistRolesSQL = `('release_artist','album_artist','track_artist')`

// MusicBrainz 默认开启。管理员关闭后艺人可直接走联网识别，来源限流期间仍等待发行结论。
const ReleaseRecognitionPendingSQL = `(NOT EXISTS (
 SELECT FROM curation_source_settings WHERE source='musicbrainz' AND NOT enabled)
 AND (recognition.target_id IS NULL OR recognition.needs_work OR recognition.status IN ('pending','try_later')))`

// 从有效的主署名读取艺人与发行的双向关系。散曲和多艺人专辑沿用相同的主署名合同。
const ArtistReleaseCreditsSQL = `WITH ` + catalog.ArtistMediaFamilyCTE + `, credits AS NOT MATERIALIZED (
 SELECT DISTINCT member.canonical_id artist_id,COALESCE(m.release_id,c.release_id) release_id
 FROM artist_effective_credits c JOIN artist_media_family member ON member.artist_id=c.artist_id
 ` + catalog.CurationCreditJoins + `
 LEFT JOIN tracks t ON t.id=c.track_id LEFT JOIN media m ON m.id=t.medium_id
 WHERE c.role IN ` + PrimaryArtistRolesSQL + ` AND ` + catalog.UsableCurationCredit + `
 AND (c.track_id IS NULL OR t.source_status='present')
 ) `

// ArtistMediaLinksCTE 读取 family 中可用于补资料的对应。管理员指定或模型确认的首选条目
// 覆盖同源并入方；只含旧发行对应的家族仍可逐个尝试原有的 MBID。
const ArtistMediaLinksCTE = `media_links AS (
 SELECT canonical_id, source, external_id FROM (
   SELECT f.canonical_id, l.source,
          CASE WHEN l.source='musicbrainz' THEN lower(l.external_id) ELSE l.external_id END AS external_id,
          row_number() OVER by_source AS position,
          first_value(l.method IN ('artist_manual','artist_model')) OVER by_source AS specified
   FROM family f JOIN external_links l ON l.object_id=f.artist_id
   WHERE l.object_type='artist' AND l.source IN ('musicbrainz','wikidata')
   WINDOW by_source AS (PARTITION BY f.canonical_id,l.source ORDER BY ` + catalog.ArtistMediaOrder + `)
 ) ranked WHERE position=1 OR NOT specified
)`

type ArtistMediaLink struct {
	Source     string `json:"source"`
	ExternalID string `json:"external_id"`
}

func (l ArtistMediaLink) normalized(mbid string) ArtistMediaLink {
	if l.Source == "" && l.ExternalID == "" && mbid != "" {
		l = ArtistMediaLink{Source: "musicbrainz", ExternalID: mbid}
	}
	if l.Source == "musicbrainz" {
		l.ExternalID = strings.ToLower(l.ExternalID)
	}
	return l
}

type ArtistImageRequest struct {
	ArtistID, MBID, BatchID, Key, RequestID string
	Link                                    ArtistMediaLink
	Name                                    string
	Citations                               []catalog.Citation
	Image                                   sources.ArtistImage
}

type ArtistBioRequest struct {
	ArtistID, MBID, BatchID, Key, RequestID string
	Link                                    ArtistMediaLink
	Name                                    string
	Citations                               []catalog.Citation
	Bio                                     sources.ArtistBio
}

// 快照保存来源和实际写入的值。图像字节由内容寻址键引用。
type artistMediaValue struct {
	externalIdentity
	Link       ArtistMediaLink    `json:"link"`
	Name       string             `json:"name,omitempty"`
	Citations  []catalog.Citation `json:"citations,omitempty"`
	SourceURL  string             `json:"source_url"`
	License    string             `json:"license,omitempty"`
	LicenseURL string             `json:"license_url,omitempty"`
	Author     string             `json:"author,omitempty"`
	Resource   string             `json:"resource,omitempty"`
	MIME       string             `json:"mime,omitempty"`
	Width      int                `json:"width,omitempty"`
	Height     int                `json:"height,omitempty"`
	Text       string             `json:"text,omitempty"`
	Lang       string             `json:"lang,omitempty"`
}

func mediaAttribution(a sources.Attribution, id string) artistMediaValue {
	return artistMediaValue{externalIdentity: externalIdentity{a.Source, id}, SourceURL: a.SourceURL, Author: a.Author, License: a.License, LicenseURL: a.LicenseURL}
}

type artistMediaWrite struct {
	kind                              artistMediaKind
	artistID, batchID, key, requestID string
	value                             artistMediaValue
}

func (w *Writer) ApplyArtistImage(ctx context.Context, r ArtistImageRequest) (ExternalApplied, error) {
	image := r.Image
	switch image.Source {
	case "commons":
		if strings.TrimSpace(image.License) == "" {
			return ExternalApplied{Skipped: "license_missing"}, operations.Conflict("nothing_to_apply", "图片缺少可用许可")
		}
	case "theaudiodb", "fanarttv", "web_search":
	default:
		if !strings.HasPrefix(image.Source, "plugin:") || !externalProviderPattern.MatchString(image.Source) {
			return ExternalApplied{}, operations.Invalid("invalid_input", "艺人图片来源无效")
		}
	}
	stored, err := w.artworks.Describe(image.Data)
	if err != nil {
		return ExternalApplied{}, operations.Invalid("invalid_input", "艺人图片无效")
	}
	if image.Source == "web_search" && min(stored.Width, stored.Height) < 200 {
		return ExternalApplied{}, operations.Invalid("invalid_input", "网络搜索图片的短边至少需要 200 像素")
	}
	value := mediaAttribution(image.Attribution, image.ID)
	value.Resource, value.MIME, value.Width, value.Height = stored.Key, stored.MIMEType, stored.Width, stored.Height
	value.Link, value.Name, value.Citations = r.Link.normalized(r.MBID), r.Name, r.Citations
	write := artistMediaWrite{artistImageKind(w), strings.ToLower(r.ArtistID), r.BatchID, r.Key, r.RequestID, value}
	wrote := false
	applied, err := write.apply(ctx, func(ctx context.Context, tx *sql.Tx, operationID string) error {
		var err error
		wrote, err = w.artworks.WriteIfChanged(ctx, tx, image.Data, stored)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO artist_external_artworks
		(artist_id,storage_key,mime,width,height,source,source_url,author,license,license_url,operation_id,basis_source,basis_id,citations)
		VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),NULLIF($9,''),NULLIF($10,''),$11,NULLIF($12,''),NULLIF($13,''),$14)`,
			write.artistID, value.Resource, value.MIME, value.Width, value.Height, value.Source, value.SourceURL, value.Author, value.License, value.LicenseURL, operationID, value.Link.Source, value.Link.ExternalID, value.citationJSON())
		return err
	})
	if err != nil && wrote {
		w.discardArtistImage(ctx, stored.Key, write.artistID)
	}
	return applied, err
}

func (w *Writer) discardArtistImage(ctx context.Context, key, artistID string) {
	ctx = context.WithoutCancel(ctx)
	if err := w.artworks.Discard(ctx, w.db, key); err != nil {
		w.logger.WarnContext(ctx, "未能清理自动整理的艺人图片", "event", "curation.artwork.discard_failed", "module", "curation", "artist_id", artistID)
	}
}

func (w *Writer) ApplyArtistBio(ctx context.Context, r ArtistBioRequest) (ExternalApplied, error) {
	bio := r.Bio
	if (bio.Source != "theaudiodb" && bio.Source != "wikipedia" && bio.Source != "web_search" && !strings.HasPrefix(bio.Source, "plugin:")) || (bio.Lang != "zh" && bio.Lang != "en") ||
		strings.TrimSpace(bio.Text) == "" || !utf8.ValidString(bio.Text) || utf8.RuneCountInString(bio.Text) > 4000 {
		return ExternalApplied{}, operations.Invalid("invalid_input", "艺人简介来源、语言或文本无效")
	}
	value := mediaAttribution(bio.Attribution, bio.ID)
	value.Text, value.Lang = bio.Text, bio.Lang
	value.Link, value.Name, value.Citations = r.Link.normalized(r.MBID), r.Name, r.Citations
	write := artistMediaWrite{artistBioKind(w), strings.ToLower(r.ArtistID), r.BatchID, r.Key, r.RequestID, value}
	return write.apply(ctx, func(ctx context.Context, tx *sql.Tx, operationID string) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO artist_external_bios(artist_id,text,lang,source,source_url,license,license_url,operation_id,basis_source,basis_id,citations)
		VALUES($1,$2,$3,$4,$5,NULLIF($6,''),NULLIF($7,''),$8,NULLIF($9,''),NULLIF($10,''),$11)`, write.artistID, value.Text, value.Lang, value.Source, value.SourceURL, value.License, value.LicenseURL, operationID, value.Link.Source, value.Link.ExternalID, value.citationJSON())
		return err
	})
}

func (v artistMediaValue) citationJSON() json.RawMessage {
	if len(v.Citations) == 0 {
		return json.RawMessage("[]")
	}
	b, _ := json.Marshal(v.Citations)
	return b
}

func ValidateArtistMediaCitations(citations []catalog.Citation) error {
	if len(citations) < 1 || len(citations) > 5 {
		return operations.Invalid("invalid_input", "网络搜索资料需要一至五个引用网页")
	}
	for _, citation := range citations {
		if _, err := ParseArtistWebURL(citation.URL); err != nil {
			return err
		}
		title := citation.Title
		// 与艺人页的标题长度校验使用同一单位。
		if !utf8.ValidString(title) || strings.ContainsRune(title, 0) || len(utf16.Encode([]rune(title))) > 500 {
			return operations.Invalid("invalid_input", "引用标题需要为至多 500 字的纯文本")
		}
	}
	return nil
}

func (r artistMediaWrite) apply(ctx context.Context, insert func(context.Context, *sql.Tx, string) error) (ExternalApplied, error) {
	link := r.value.Link
	validMB := link.Source == "musicbrainz" && uuid.Valid(link.ExternalID)
	validWD := link.Source == "wikidata" && sources.ValidWikidataID(link.ExternalID)
	if r.value.SourceURL == "" || r.value.ExternalID == "" {
		return ExternalApplied{}, operations.Invalid("invalid_input", "艺人资料缺少外部条目标识或链接")
	}
	var conversation, itemID, model string
	source := Source{Kind: "source", Provider: r.value.Source}
	switch {
	case r.value.Source == "web_search":
		if link != (ArtistMediaLink{}) {
			return ExternalApplied{}, operations.Invalid("invalid_input", "网络搜索资料依据须为已领取的艺人待定项")
		}
		if err := ValidateArtistMediaCitations(r.value.Citations); err != nil {
			return ExternalApplied{}, err
		}
		run, ok := ModelRunFrom(ctx)
		if !ok || !uuid.Valid(run.PendingItemID) || !uuid.Valid(run.ConversationID) || !uuid.Valid(r.batchID) || run.ActorID == "" {
			return ExternalApplied{}, operations.Conflict("out_of_batch", "网络搜索资料需要已领取的艺人待定项")
		}
		conversation, itemID, model = run.ConversationID, run.PendingItemID, run.Model
		source.Kind, source.Basis = "model", "艺人身份待定项 "+itemID
	case validMB:
		source.Basis = "MusicBrainz 艺人 " + link.ExternalID
	case validWD:
		source.Basis = "Wikidata " + link.ExternalID
	case link == (ArtistMediaLink{}) && r.value.Name != "" && strings.HasPrefix(r.value.Source, "plugin:"):
		source.Basis = "按本地艺人名查询"
	default:
		return ExternalApplied{}, operations.Invalid("invalid_input", "艺人资料缺少有效依据")
	}
	fp := operations.Fingerprint(map[string]any{"type": r.kind.Type(), "artist_id": r.artistID, "value": r.value})
	if run, ok := ModelRunFrom(ctx); ok && r.value.Source == "web_search" && run.ToolInputDigest != "" {
		fp = run.ArtistToolFingerprint(r.kind.Type())
	}
	w := r.kind.writer
	spec, err := w.sourceSpec(ctx, r.kind.Type(), r.artistID, fp, source, r.batchID, r.key, r.requestID, "", conversation)
	if err != nil {
		return ExternalApplied{}, err
	}
	var applied ExternalApplied
	result, err := operations.Run(ctx, w.db, spec, func(ctx context.Context, tx *sql.Tx, operationID string) (operations.Outcome, error) {
		canonical, _, err := lockEffectiveArtist(ctx, tx, r.artistID)
		if err != nil {
			return operations.Outcome{}, err
		}
		if canonical != r.artistID {
			return operations.Outcome{}, operations.Conflict("evidence_changed", "艺人已合并")
		}
		if err := checkExternalWriter(ctx, tx, spec); err != nil {
			return operations.Outcome{}, err
		}
		if itemID != "" {
			run, _ := ModelRunFrom(ctx)
			if err := checkClaimedTarget(ctx, tx, run, r.artistID, "artist_id", "artist_identity"); err != nil {
				return operations.Outcome{}, err
			}
		}
		rowsSQL := catalog.ArtistImageRowsSQL
		if r.kind.field == "bio" {
			rowsSQL = catalog.ArtistBioRowsSQL
		}
		var valid, occupied, suppressed bool
		err = tx.QueryRowContext(ctx, artistLinkFamilyCTE+` SELECT
		    CASE WHEN $2='' THEN NOT EXISTS (SELECT FROM media_links) AND
		         ($7='' OR $7=(SELECT COALESCE(p.effective_display_name,a.initial_name) FROM artists a LEFT JOIN artist_profiles p ON p.artist_id=a.id WHERE a.id=$1))
		         ELSE EXISTS (SELECT FROM media_links WHERE source=$2 AND external_id=$3) END,
		    EXISTS (SELECT FROM family JOIN (`+rowsSQL+`) m USING(artist_id) WHERE `+catalog.ArtistMediaRankSQL+` >= $6),
		    EXISTS (SELECT FROM family JOIN curation_suppressions s ON s.target_id=family.artist_id
		        WHERE s.target_type='artist' AND s.subject=$4 AND s.value_hash=$5)`,
			r.artistID, link.Source, link.ExternalID, r.kind.subject, r.value.identityHash(), catalog.ArtistMediaRank(r.value.Source), r.value.Name).Scan(&valid, &occupied, &suppressed)
		if err != nil {
			return operations.Outcome{}, err
		}
		if !valid {
			return operations.Outcome{}, operations.Conflict("evidence_changed", "艺人的外部对应已变化")
		}
		if occupied {
			return applied.skip("occupied")
		}
		if suppressed {
			return applied.skip("suppressed")
		}
		replaced, resources, err := r.removeLowerRank(ctx, tx)
		if err != nil {
			return operations.Outcome{}, err
		}
		if err := insert(ctx, tx, operationID); err != nil {
			return operations.Outcome{}, err
		}
		after := map[string]any{r.kind.field: r.value}
		if len(replaced) > 0 {
			after["replaced"] = replaced
		}
		if model != "" {
			after["model"] = model
		}
		out, err := artistMediaOutcome(ctx, tx, r.artistID, map[string]any{r.kind.field: nil}, after)
		if len(resources) > 0 {
			out.AfterCommit = func() {
				for _, key := range resources {
					w.discardArtistImage(ctx, key, r.artistID)
				}
			}
		}
		out.Response = ExternalApplied{OperationID: operationID}
		return out, err
	})
	if err != nil {
		return applied, err
	}
	return ExternalApplied{OperationID: result.OperationID}, nil
}

// 在同一资料事务里移除家族的旧自动行。替换只留审计快照，不占用图片保留引用。
func (r artistMediaWrite) removeLowerRank(ctx context.Context, tx *sql.Tx) ([]json.RawMessage, []string, error) {
	rows, err := tx.QueryContext(ctx, `WITH `+catalog.ArtistMediaFamilyCTE+`, removed AS (
		DELETE FROM `+r.kind.table+` m USING artist_media_family f WHERE f.canonical_id=$1 AND f.artist_id=m.artist_id
		AND `+catalog.ArtistMediaRankSQL+` < $2 RETURNING m.*
	) SELECT to_jsonb(m), COALESCE(to_jsonb(m)->>'storage_key','') FROM removed m ORDER BY artist_id`, r.artistID, catalog.ArtistMediaRank(r.value.Source))
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var replaced []json.RawMessage
	var keys []string
	for rows.Next() {
		var snapshot json.RawMessage
		var key string
		if err := rows.Scan(&snapshot, &key); err != nil {
			return nil, nil, err
		}
		replaced = append(replaced, snapshot)
		if key != "" {
			keys = append(keys, key)
		}
	}
	return replaced, keys, rows.Err()
}

func lockArtistMedia(ctx context.Context, tx *sql.Tx, id string) error {
	if err := catalog.AcquireArtistWriteGate(ctx, tx); err != nil {
		return err
	}
	resolved, err := catalog.NewStore(tx).ResolveArtists(ctx, []string{id})
	if errors.Is(err, catalog.ErrArtistCatalogPreparing) {
		return operations.Conflict("artist_catalog_not_ready", "艺人资料正在整理")
	}
	if err != nil {
		return err
	}
	if resolved[id] == "" {
		return operations.NotFound("not_found", "艺人不存在")
	}
	_, err = tx.ExecContext(ctx, `SELECT id FROM artists WHERE id=$1::uuid FOR UPDATE`, id)
	return err
}

func artistMediaOutcome(ctx context.Context, tx *sql.Tx, id string, before, after any) (operations.Outcome, error) {
	out := operations.Outcome{TargetType: operations.TargetArtist, TargetID: id, Before: before, After: after}
	err := tx.QueryRowContext(ctx, `UPDATE artists SET auto_revision=auto_revision+1 WHERE id=$1::uuid RETURNING auto_revision-1,auto_revision,edit_revision`, id).
		Scan(&out.ExpectedRevision, &out.ResultRevision, &out.ResponseRevision)
	return out, err
}
