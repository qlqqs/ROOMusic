package curation

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type LinkIntent struct {
	ArtistPrimaryName string `json:"-"`
	ObjectType        string `json:"object_type"`
	ObjectID          string `json:"object_id"`
	ExternalID        string `json:"external_id"`
	ExternalGroupID   string `json:"external_group_id,omitempty"`
}

func (l LinkIntent) key() string { return l.ObjectType + ":" + l.ObjectID }

type ExternalSummary struct {
	DiscCount  int    `json:"disc_count,omitempty"`
	TrackCount int    `json:"track_count,omitempty"`
	Title      string `json:"title"`
	Artist     string `json:"artist"`
	Date       string `json:"date"`
	Country    string `json:"country"`
}
type LinkRequest struct {
	verification                *verificationWrite
	RequireAll                  bool
	CheckEvidence               func(context.Context, *sql.Tx) error
	ApprovedReviewID            string
	ExpectedRevision            int64
	ConfirmedBy, ConversationID string
	ExternalSource              string
	ReleaseID                   string
	Links                       []LinkIntent
	Source                      Source
	Method, Confidence          string
	External                    ExternalSummary
	BatchID, Key, RequestID     string
}
type LinkApplied struct {
	OperationID string            `json:"operation_id"`
	Written     []LinkIntent      `json:"written"`
	Skipped     map[string]string `json:"skipped"`
}

type linkState struct {
	Split      *CreditSplit    `json:"split,omitempty"`
	Provider   string          `json:"provider,omitempty"`
	Source     string          `json:"source"`
	Method     string          `json:"method"`
	Confidence string          `json:"confidence"`
	External   ExternalSummary `json:"external"`
	Links      []LinkIntent    `json:"links"`
}

var linkCode = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)

func (r LinkRequest) normalizedLinks() ([]LinkIntent, error) {
	if len(r.Links) == 0 || !linkCode.MatchString(r.Method) || !linkCode.MatchString(r.Confidence) {
		return nil, operations.Invalid("invalid_input", "对应、匹配方法或置信度无效")
	}
	for _, value := range []string{r.External.Title, r.External.Artist, r.External.Date, r.External.Country} {
		if len(value) > 500 || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
			return nil, operations.Invalid("invalid_input", "外部展示信息无效")
		}
	}
	links := slices.Clone(r.Links)
	seen := make(map[string]bool, len(links))
	for i := range links {
		l := &links[i]
		l.ObjectID = strings.ToLower(l.ObjectID)
		if (l.ObjectType != "release" && l.ObjectType != "track" && l.ObjectType != "release_group_match" && l.ObjectType != "release_group" && l.ObjectType != "artist") || !uuid.Valid(l.ObjectID) || !validExternalID(l.ExternalID) || (l.ExternalGroupID != "" && (l.ObjectType != "release" || !validExternalID(l.ExternalGroupID))) || seen[l.key()] {
			return nil, operations.Invalid("invalid_input", "外部对应的对象或编号无效")
		}
		if r.Source.Kind != "source" || r.Source.Provider != "musicbrainz" {
			switch l.ObjectType {
			case "artist":
				if r.verification == nil || r.verification.item.Kind != "verify_artist_credit" {
					return nil, operations.Invalid("invalid_input", "艺人对应缺少 MusicBrainz 署名依据")
				}
			case "release_group_match":
				reviewed := r.Method == "independent_review" && (uuid.Valid(r.ApprovedReviewID) || r.ConfirmedBy != "")
				if r.Source.Kind != "model" || r.ExternalSource != "musicbrainz" || r.Method != "album_structure" && r.Method != "model" && !reviewed || r.CheckEvidence == nil {
					return nil, operations.Invalid("invalid_input", "发行组级对应缺少 MusicBrainz 结构核对")
				}
			}
		}
		seen[l.key()] = true
	}
	slices.SortFunc(links, func(a, b LinkIntent) int {
		return cmp.Or(cmp.Compare(a.ObjectType, b.ObjectType), cmp.Compare(a.ObjectID, b.ObjectID))
	})
	return links, nil
}

func validateLinkMembers(ctx context.Context, tx *sql.Tx, releaseID string, links []LinkIntent) error {
	if slices.ContainsFunc(links, func(l LinkIntent) bool { return l.ObjectType == "artist" }) {
		credits, err := catalog.LoadCurationArtistCredits(ctx, tx, releaseID)
		if errors.Is(err, catalog.ErrArtistCatalogPreparing) {
			return operations.Conflict("artist_catalog_not_ready", "艺人资料正在整理，请稍后再试")
		}
		if err != nil {
			return err
		}
		members := map[string]bool{}
		add := func(group []catalog.CurationArtistCredit) {
			for _, c := range group {
				members[c.ArtistID] = true
			}
		}
		add(credits.ReleaseArtists)
		add(credits.AlbumArtists)
		for _, group := range credits.Tracks {
			add(group)
		}
		for _, l := range links {
			if l.ObjectType == "artist" && !members[l.ObjectID] {
				return operations.Invalid("invalid_input", "艺人没有在这个发行署名")
			}
		}
	}
	raw, err := json.Marshal(links)
	if err != nil {
		return err
	}
	var valid bool
	err = tx.QueryRowContext(ctx, `SELECT bool_and(CASE l.object_type
 WHEN 'artist' THEN true WHEN 'release' THEN l.object_id=r.id WHEN 'release_group_match' THEN l.object_id=r.id WHEN 'release_group' THEN l.object_id=r.group_id
 WHEN 'track' THEN EXISTS(SELECT 1 FROM tracks t JOIN media m ON m.id=t.medium_id WHERE t.id=l.object_id AND m.release_id=r.id)
 ELSE false END)
 FROM releases r CROSS JOIN jsonb_to_recordset($2::jsonb) AS l(object_type text,object_id uuid) WHERE r.id=$1`, releaseID, raw).Scan(&valid)
	if err == nil && !valid {
		return operations.Invalid("invalid_input", "对应的对象不属于这个发行")
	}
	return err
}

// 唯一约束裁定并发插入；遇到已有行时保留其原有写入者。
func addUnoccupiedLink(ctx context.Context, tx *sql.Tx, l Link) (string, error) {
	blocked, err := LinkSuppressed(ctx, tx, l.ObjectType, l.ObjectID, l.Source, l.ExternalID)
	if err != nil {
		return "", err
	}
	if blocked {
		return "suppressed", nil
	}
	result, err := tx.ExecContext(ctx, insertLinkSQL+" ON CONFLICT(source,object_type,object_id) DO NOTHING", l.insertArgs()...)
	if err != nil {
		return "", err
	}
	n, err := result.RowsAffected()
	if err != nil || n > 0 {
		return "", err
	}
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT external_id FROM external_links WHERE source=$1 AND object_type=$2 AND object_id=$3`, l.Source, l.ObjectType, l.ObjectID).Scan(&existing)
	if err != nil {
		return "", err
	}
	if existing == l.ExternalID {
		// 独立的有效署名可以接管仅由拆分支撑的对应。
		if l.ObjectType == "artist" && l.Method != "split_credit" {
			r, err := tx.ExecContext(ctx, `UPDATE external_links SET method=$3,operation_id=$4 WHERE object_type='artist' AND object_id=$1 AND external_id=$2 AND source='musicbrainz' AND method='split_credit'`, l.ObjectID, l.ExternalID, l.Method, l.OperationID)
			if err != nil {
				return "", err
			}
			if n, err := r.RowsAffected(); err != nil || n > 0 {
				return "", err
			}
		}
		return "unchanged", nil
	}
	return "occupied", nil
}

func (w *Writer) ApplyLinks(ctx context.Context, request LinkRequest) (LinkApplied, error) {
	return w.applyLinks(ctx, nil, request)
}
func (w *Writer) ApplyArtistReleaseLinks(ctx context.Context, tx *sql.Tx, item PendingItem, request LinkRequest) (LinkApplied, error) {
	// 艺人项和艺人写锁由外层对应事务持有，发行必须属于该项领取时的证据。
	var allowed bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT FROM curation_pending_items p, jsonb_array_elements(p.evidence->'albums') a
 WHERE p.id=$1 AND p.artist_id=$2 AND p.artist_web_generation IS NOT NULL AND p.input_digest=$3 AND a->>'id'=$4
 AND (p.status='in_progress' AND p.conversation_id=$5 OR p.resolution='suggestion_recorded' AND $6))`, item.ID, item.ArtistID, item.InputDigest, request.ReleaseID, request.ConversationID, request.ConfirmedBy != "").Scan(&allowed)
	if err != nil {
		return LinkApplied{}, err
	}
	if !allowed {
		return LinkApplied{}, operations.Conflict("item_stale", "发行不属于本次艺人核对")
	}
	return w.applyLinks(ctx, tx, request)
}
func (w *Writer) applyLinks(ctx context.Context, parent *sql.Tx, request LinkRequest) (LinkApplied, error) {
	external, err := musicBrainzSource(request.ExternalSource, request.Source)
	if err != nil {
		return LinkApplied{}, err
	}
	links, err := request.normalizedLinks()
	if err != nil {
		return LinkApplied{}, err
	}
	id := strings.ToLower(request.ReleaseID)
	fp := map[string]any{"type": operations.TypeExternalLink, "release_id": id, "links": links, "provider": request.Source.Provider, "method": request.Method}
	if request.RequireAll {
		fp["require_all"] = true
	}
	if request.ExpectedRevision != 0 {
		fp["expected_revision"] = request.ExpectedRevision
	}
	fingerprint := operations.Fingerprint(fp)
	spec, err := w.sourceSpec(ctx, operations.TypeExternalLink, id, fingerprint, request.Source, request.BatchID, request.Key, request.RequestID, request.ConfirmedBy, request.ConversationID)
	if err != nil {
		return LinkApplied{}, err
	}
	applied := LinkApplied{Written: []LinkIntent{}, Skipped: map[string]string{}}
	execute := func(ctx context.Context, tx *sql.Tx, operationID string) (operations.Outcome, error) {
		release, err := governance.LockAutoRelease(ctx, tx, id)
		if err != nil {
			return operations.Outcome{}, err
		}
		if parent == nil {
			if err := checkWrite(ctx, tx, spec, request.ConfirmedBy, id, request.ExpectedRevision, release.EditRevision()); err != nil {
				return operations.Outcome{}, err
			}
		}
		if request.verification != nil {
			if err := request.verification.check(ctx, tx); err != nil {
				return operations.Outcome{}, err
			}
		}
		if request.CheckEvidence != nil {
			if err := request.CheckEvidence(ctx, tx); err != nil {
				return operations.Outcome{}, err
			}
		}
		if err := validateLinkMembers(ctx, tx, id, links); err != nil {
			return operations.Outcome{}, err
		}
		if err := checkReleaseGroupCompatibility(ctx, tx, id, links); err != nil {
			return operations.Outcome{}, err
		}
		var linkedArtists []string
		for _, intent := range links {
			l := Link{ArtistPrimaryName: intent.ArtistPrimaryName, Source: external, ObjectType: intent.ObjectType, ObjectID: intent.ObjectID, ExternalID: intent.ExternalID, Method: request.Method, Confidence: request.Confidence, OperationID: operationID}
			skip, err := addUnoccupiedLink(ctx, tx, l)
			if err != nil {
				return operations.Outcome{}, err
			}
			if skip != "" {
				if (request.RequireAll || request.Source.Kind == "model" || intent.ObjectType == "release_group_match") && skip != "unchanged" {
					return operations.Outcome{}, operations.Conflict(skip, "外部对应已有其他目标或已标记不再提出")
				}
				applied.Skipped[intent.key()] = skip
				continue
			}
			if intent.ObjectType == "artist" {
				linkedArtists = append(linkedArtists, intent.ObjectID)
			}
			applied.Written = append(applied.Written, intent)
		}
		if len(applied.Written) == 0 {
			return operations.Outcome{}, operations.Conflict("nothing_to_apply", "没有需要建立的对应")
		}
		if err := catalog.RefreshMergedArtistProfiles(ctx, tx, linkedArtists); err != nil {
			return operations.Outcome{}, err
		}
		after := linkState{Source: external, Method: request.Method, Confidence: request.Confidence, External: request.External, Links: applied.Written}
		if request.ExternalSource != "" {
			after.Provider = request.Source.Provider
		}
		out, err := externalOutcome(ctx, release, map[string]any{}, after)
		applied.OperationID = operationID
		out.Response = applied
		if err == nil && request.verification != nil {
			err = request.verification.finish(ctx, tx, operationID)
		}
		return out, err
	}
	var result operations.Result
	if parent == nil {
		result, err = operations.Run(ctx, w.db, spec, execute)
	} else {
		result, _, err = operations.RunInTx(ctx, parent, spec, execute)
	}
	if err != nil {
		var failure *operations.Failure
		if errors.As(err, &failure) && failure.Code == "nothing_to_apply" {
			return applied, err
		}
		return LinkApplied{}, err
	}
	var response LinkApplied
	err = json.Unmarshal(result.Response, &response)
	return response, err
}

// 调用方已锁住发行，已有的版本身份与独立确认的组身份必须相容。
func checkReleaseGroupCompatibility(ctx context.Context, tx *sql.Tx, id string, links []LinkIntent) error {
	for _, l := range links {
		var conflict bool
		switch l.ObjectType {
		case "release_group_match":
			err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT FROM external_links r WHERE r.source='musicbrainz' AND r.object_type='release' AND r.object_id=$1
   AND NOT EXISTS(SELECT FROM operations o CROSS JOIN LATERAL jsonb_to_recordset(o.after_state->'links') AS i(object_type text,object_id uuid,external_id text,external_group_id text)
   WHERE o.id=r.operation_id AND o.operation_type='external_link' AND o.target_id=r.object_id AND i.object_type=r.object_type AND i.object_id=r.object_id AND i.external_id=r.external_id AND i.external_group_id=$2))`, id, l.ExternalID).Scan(&conflict)
			if err != nil {
				return err
			}
		case "release":
			err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT FROM external_links WHERE source='musicbrainz' AND object_type='release_group_match' AND object_id=$1 AND external_id<>$2)`, id, l.ExternalGroupID).Scan(&conflict)
			if err != nil {
				return err
			}
		}
		if conflict {
			return operations.Conflict("existing_link", "已有对应与发行所属的外部组不符")
		}
	}
	return nil
}
