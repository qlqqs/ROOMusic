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

	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type LinkIntent struct {
	ObjectType      string `json:"object_type"`
	ObjectID        string `json:"object_id"`
	ExternalID      string `json:"external_id"`
	ExternalGroupID string `json:"external_group_id,omitempty"`
}

func (l LinkIntent) key() string { return l.ObjectType + ":" + l.ObjectID }

type ExternalSummary struct {
	Title   string `json:"title"`
	Artist  string `json:"artist"`
	Date    string `json:"date"`
	Country string `json:"country"`
}
type LinkRequest struct {
	ReleaseID               string
	Links                   []LinkIntent
	Source                  Source
	Method, Confidence      string
	External                ExternalSummary
	BatchID, Key, RequestID string
}
type LinkApplied struct {
	OperationID string            `json:"operation_id"`
	Written     []LinkIntent      `json:"written"`
	Skipped     map[string]string `json:"skipped"`
}

type linkState struct {
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
		if (l.ObjectType != "release" && l.ObjectType != "track" && l.ObjectType != "release_group") || !uuid.Valid(l.ObjectID) || !validExternalID(l.ExternalID) || (l.ExternalGroupID != "" && (l.ObjectType != "release" || !validExternalID(l.ExternalGroupID))) || seen[l.key()] {
			return nil, operations.Invalid("invalid_input", "外部对应的对象或编号无效")
		}
		seen[l.key()] = true
	}
	slices.SortFunc(links, func(a, b LinkIntent) int {
		return cmp.Or(cmp.Compare(a.ObjectType, b.ObjectType), cmp.Compare(a.ObjectID, b.ObjectID))
	})
	return links, nil
}

func validateLinkMembers(ctx context.Context, tx *sql.Tx, releaseID string, links []LinkIntent) error {
	raw, err := json.Marshal(links)
	if err != nil {
		return err
	}
	var valid bool
	err = tx.QueryRowContext(ctx, `SELECT bool_and(CASE l.object_type
 WHEN 'release' THEN l.object_id=r.id WHEN 'release_group' THEN l.object_id=r.group_id
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
		return "unchanged", nil
	}
	return "occupied", nil
}

func (w *Writer) ApplyLinks(ctx context.Context, request LinkRequest) (LinkApplied, error) {
	links, err := request.normalizedLinks()
	if err != nil {
		return LinkApplied{}, err
	}
	id := strings.ToLower(request.ReleaseID)
	fingerprint := operations.Fingerprint(map[string]any{"type": operations.TypeExternalLink, "release_id": id, "links": links, "provider": request.Source.Provider, "method": request.Method})
	spec, err := w.sourceSpec(ctx, operations.TypeExternalLink, id, fingerprint, request.Source, request.BatchID, request.Key, request.RequestID)
	if err != nil {
		return LinkApplied{}, err
	}
	applied := LinkApplied{Written: []LinkIntent{}, Skipped: map[string]string{}}
	result, err := operations.Run(ctx, w.db, spec, func(ctx context.Context, tx *sql.Tx, operationID string) (operations.Outcome, error) {
		release, err := governance.LockAutoRelease(ctx, tx, id)
		if err != nil {
			return operations.Outcome{}, err
		}
		if err := checkExternalWriter(ctx, tx, spec); err != nil {
			return operations.Outcome{}, err
		}
		if err := validateLinkMembers(ctx, tx, id, links); err != nil {
			return operations.Outcome{}, err
		}
		for _, intent := range links {
			l := Link{Source: request.Source.Provider, ObjectType: intent.ObjectType, ObjectID: intent.ObjectID, ExternalID: intent.ExternalID, Method: request.Method, Confidence: request.Confidence, OperationID: operationID}
			skip, err := addUnoccupiedLink(ctx, tx, l)
			if err != nil {
				return operations.Outcome{}, err
			}
			if skip != "" {
				applied.Skipped[intent.key()] = skip
				continue
			}
			applied.Written = append(applied.Written, intent)
		}
		if len(applied.Written) == 0 {
			return operations.Outcome{}, operations.Conflict("nothing_to_apply", "没有需要建立的对应")
		}
		after := linkState{request.Source.Provider, request.Method, request.Confidence, request.External, applied.Written}
		out, err := externalOutcome(ctx, release, map[string]any{}, after)
		applied.OperationID = operationID
		out.Response = applied
		return out, err
	})
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
