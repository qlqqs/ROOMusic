package curation

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

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
	Image                                   sources.ArtistImage
}

type ArtistBioRequest struct {
	ArtistID, MBID, BatchID, Key, RequestID string
	Link                                    ArtistMediaLink
	Bio                                     sources.ArtistBio
}

// 快照保存来源和实际写入的值。图像字节由内容寻址键引用。
type artistMediaValue struct {
	externalIdentity
	Link       ArtistMediaLink `json:"link"`
	SourceURL  string          `json:"source_url"`
	License    string          `json:"license,omitempty"`
	LicenseURL string          `json:"license_url,omitempty"`
	Author     string          `json:"author,omitempty"`
	Resource   string          `json:"resource,omitempty"`
	MIME       string          `json:"mime,omitempty"`
	Width      int             `json:"width,omitempty"`
	Height     int             `json:"height,omitempty"`
	Text       string          `json:"text,omitempty"`
	Lang       string          `json:"lang,omitempty"`
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
	case "theaudiodb", "fanarttv":
	default:
		if !strings.HasPrefix(image.Source, "plugin:") || !externalProviderPattern.MatchString(image.Source) {
			return ExternalApplied{}, operations.Invalid("invalid_input", "艺人图片来源无效")
		}
	}
	stored, err := w.artworks.Describe(image.Data)
	if err != nil {
		return ExternalApplied{}, operations.Invalid("invalid_input", "艺人图片无效")
	}
	value := mediaAttribution(image.Attribution, image.ID)
	value.Resource, value.MIME, value.Width, value.Height = stored.Key, stored.MIMEType, stored.Width, stored.Height
	value.Link = r.Link.normalized(r.MBID)
	write := artistMediaWrite{artistImageKind(w), strings.ToLower(r.ArtistID), r.BatchID, r.Key, r.RequestID, value}
	wrote := false
	applied, err := write.apply(ctx, func(ctx context.Context, tx *sql.Tx, operationID string) error {
		var err error
		wrote, err = w.artworks.WriteIfChanged(ctx, tx, image.Data, stored)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO artist_external_artworks
		(artist_id,storage_key,mime,width,height,source,source_url,author,license,license_url,operation_id,basis_source,basis_id)
		VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),NULLIF($9,''),NULLIF($10,''),$11,$12,$13)`,
			write.artistID, value.Resource, value.MIME, value.Width, value.Height, value.Source, value.SourceURL, value.Author, value.License, value.LicenseURL, operationID, value.Link.Source, value.Link.ExternalID)
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
	if (bio.Source != "theaudiodb" && bio.Source != "wikipedia" && !strings.HasPrefix(bio.Source, "plugin:")) || (bio.Lang != "zh" && bio.Lang != "en") ||
		strings.TrimSpace(bio.Text) == "" || !utf8.ValidString(bio.Text) || utf8.RuneCountInString(bio.Text) > 4000 {
		return ExternalApplied{}, operations.Invalid("invalid_input", "艺人简介来源、语言或文本无效")
	}
	value := mediaAttribution(bio.Attribution, bio.ID)
	value.Text, value.Lang = bio.Text, bio.Lang
	value.Link = r.Link.normalized(r.MBID)
	write := artistMediaWrite{artistBioKind(w), strings.ToLower(r.ArtistID), r.BatchID, r.Key, r.RequestID, value}
	return write.apply(ctx, func(ctx context.Context, tx *sql.Tx, operationID string) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO artist_external_bios(artist_id,text,lang,source,source_url,license,license_url,operation_id,basis_source,basis_id)
		VALUES($1,$2,$3,$4,$5,NULLIF($6,''),NULLIF($7,''),$8,$9,$10)`, write.artistID, value.Text, value.Lang, value.Source, value.SourceURL, value.License, value.LicenseURL, operationID, value.Link.Source, value.Link.ExternalID)
		return err
	})
}

func (r artistMediaWrite) apply(ctx context.Context, insert func(context.Context, *sql.Tx, string) error) (ExternalApplied, error) {
	link := r.value.Link
	validMB := link.Source == "musicbrainz" && uuid.Valid(link.ExternalID)
	validWD := link.Source == "wikidata" && sources.ValidWikidataID(link.ExternalID)
	if (!validMB && !validWD) || r.value.SourceURL == "" || r.value.ExternalID == "" {
		return ExternalApplied{}, operations.Invalid("invalid_input", "艺人资料缺少外部条目标识或链接")
	}
	basis := "MusicBrainz 艺人 " + link.ExternalID
	if validWD {
		basis = "Wikidata " + link.ExternalID
	}
	source := Source{Kind: "source", Provider: r.value.Source, Basis: basis}
	fp := operations.Fingerprint(map[string]any{"type": r.kind.Type(), "artist_id": r.artistID, "value": r.value})
	w := r.kind.writer
	spec, err := w.sourceSpec(ctx, r.kind.Type(), r.artistID, fp, source, r.batchID, r.key, r.requestID, "", "")
	if err != nil {
		return ExternalApplied{}, err
	}
	var applied ExternalApplied
	result, err := operations.Run(ctx, w.db, spec, func(ctx context.Context, tx *sql.Tx, operationID string) (operations.Outcome, error) {
		if err := lockArtistMedia(ctx, tx, r.artistID); err != nil {
			return operations.Outcome{}, err
		}
		if err := checkExternalWriter(ctx, tx, spec); err != nil {
			return operations.Outcome{}, err
		}
		var linked, occupied, suppressed bool
		err := tx.QueryRowContext(ctx, `WITH `+catalog.ArtistMediaFamilyCTE+`, family AS (
		    SELECT * FROM artist_media_family WHERE canonical_id=$1::uuid
		), `+ArtistMediaLinksCTE+` SELECT EXISTS (SELECT 1 FROM media_links
		    WHERE source=$2 AND external_id=$3),
		    EXISTS (SELECT 1 FROM family JOIN `+r.kind.table+` m USING(artist_id)),
		    EXISTS (SELECT 1 FROM family JOIN curation_suppressions s ON s.target_id=family.artist_id
		        WHERE s.target_type='artist' AND s.subject=$4 AND s.value_hash=$5)`,
			r.artistID, link.Source, link.ExternalID, r.kind.subject, r.value.identityHash()).Scan(&linked, &occupied, &suppressed)
		if err != nil {
			return operations.Outcome{}, err
		}
		if !linked {
			return operations.Outcome{}, operations.Conflict("evidence_changed", "艺人的外部对应已变化")
		}
		if occupied {
			return applied.skip("occupied")
		}
		if suppressed {
			return applied.skip("suppressed")
		}
		if err := insert(ctx, tx, operationID); err != nil {
			return operations.Outcome{}, err
		}
		out, err := artistMediaOutcome(ctx, tx, r.artistID, map[string]any{r.kind.field: nil}, map[string]any{r.kind.field: r.value})
		out.Response = ExternalApplied{OperationID: operationID}
		return out, err
	})
	if err != nil {
		return applied, err
	}
	return ExternalApplied{OperationID: result.OperationID}, nil
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
