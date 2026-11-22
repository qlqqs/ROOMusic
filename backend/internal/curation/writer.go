package curation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type Source struct {
	Kind     string
	Provider string
	Basis    string
}

type Request struct {
	OnlyEmpty bool
	ReleaseID string
	Fields    map[string]catalog.CorrectionIntent
	Source    Source
	BatchID   string
	Key       string
	RequestID string
}

type Applied struct {
	OperationID string            `json:"operation_id"`
	Written     []string          `json:"written"`
	Skipped     map[string]string `json:"skipped"`
}

var providerPattern = regexp.MustCompile(`^[a-z0-9._:-]{1,64}$`)

func (w *Writer) Apply(ctx context.Context, request Request) (Applied, error) {
	key, ok := operations.NormalizeKey(request.Key)
	if !ok || !uuid.Valid(request.ReleaseID) || (request.BatchID != "" && !uuid.Valid(request.BatchID)) {
		return Applied{}, operations.Invalid("invalid_input", "发行、批次或幂等键无效")
	}
	source := request.Source
	if (source.Kind != "source" && source.Kind != "model") || !providerPattern.MatchString(source.Provider) ||
		len(source.Basis) > 500 || !utf8.ValidString(source.Basis) || strings.ContainsRune(source.Basis, '\x00') {
		return Applied{}, operations.Invalid("invalid_input", "自动整理来源或依据无效")
	}
	fields, err := governance.NormalizeAutoFields(request.Fields)
	if err != nil {
		return Applied{}, err
	}
	releaseID := strings.ToLower(request.ReleaseID)
	var actor *string
	if err := w.db.QueryRowContext(ctx, "SELECT enabled_by::text FROM curation_settings WHERE id=1").Scan(&actor); err != nil {
		return Applied{}, err
	}
	if actor == nil {
		return Applied{}, operations.Conflict("curation_disabled", "自动整理开关已关闭")
	}
	fingerprint := map[string]any{"type": operations.TypeReleaseAutoCorrection, "release_id": releaseID, "fields": fields, "provider": source.Provider}
	if request.OnlyEmpty {
		fingerprint["only_empty"] = true
	}
	spec := operations.Spec{
		ActorID: *actor, Key: key, Type: operations.TypeReleaseAutoCorrection, BatchID: request.BatchID, RequestID: request.RequestID,
		Origin:      &operations.Origin{Source: "curation", ProviderKind: source.Kind, Provider: source.Provider, Basis: source.Basis},
		Fingerprint: operations.Fingerprint(fingerprint),
	}
	var applied Applied
	result, err := operations.Run(ctx, w.db, spec, func(ctx context.Context, tx *sql.Tx, operationID string) (operations.Outcome, error) {
		locked, err := governance.LockAutoRelease(ctx, tx, releaseID)
		if err != nil {
			return operations.Outcome{}, err
		}
		enabler, err := activeEnabler(ctx, tx)
		if err != nil {
			return operations.Outcome{}, err
		}
		if enabler != *actor {
			return operations.Outcome{}, operations.Conflict("curation_disabled", "自动整理开启者已变更，请重试")
		}
		if request.BatchID != "" {
			if err := requireOpenBatch(ctx, tx, request.BatchID); err != nil {
				return operations.Outcome{}, err
			}
		}
		var write map[string]catalog.CorrectionIntent
		write, applied, err = filterFields(ctx, tx, releaseID, locked, fields, request.OnlyEmpty)
		if err != nil {
			return operations.Outcome{}, err
		}
		if len(write) == 0 {
			return operations.Outcome{}, operations.Conflict("nothing_to_apply", "所有字段均已相同或已标记为不再提出")
		}
		out, err := locked.Write(ctx, operationID, write)
		if err != nil {
			return operations.Outcome{}, err
		}
		applied.OperationID = operationID
		out.Response = applied
		return out, nil
	})
	if err != nil {
		var failure *operations.Failure
		if errors.As(err, &failure) && failure.Code == "nothing_to_apply" {
			return applied, err
		}
		return Applied{}, err
	}
	if err := json.Unmarshal(result.Response, &applied); err != nil {
		return Applied{}, err
	}
	return applied, nil
}

func filterFields(ctx context.Context, tx *sql.Tx, releaseID string, locked *governance.LockedAutoRelease, fields map[string]catalog.CorrectionIntent, onlyEmpty bool) (map[string]catalog.CorrectionIntent, Applied, error) {
	var corrections map[string]catalog.CorrectionIntent
	if onlyEmpty {
		var err error
		corrections, err = catalog.LoadCorrectionIntents(ctx, tx, releaseID)
		if err != nil {
			return nil, Applied{}, err
		}
	}
	write := make(map[string]catalog.CorrectionIntent, len(fields))
	result := Applied{Written: []string{}, Skipped: map[string]string{}}
	for _, field := range catalog.CorrectableFields {
		name := field.Name
		intent, ok := fields[name]
		if !ok {
			continue
		}
		if onlyEmpty {
			_, manual := corrections[name]
			_, automatic := locked.Current(name)
			if manual || automatic {
				result.Skipped[name] = "occupied"
				continue
			}
		}
		_, hash, err := governance.AutoValueKey(name, intent)
		if err != nil {
			return nil, Applied{}, err
		}
		suppressed, err := suppressionExists(ctx, tx, "release", releaseID, name, hash)
		if err != nil {
			return nil, Applied{}, err
		}
		if suppressed {
			result.Skipped[name] = "suppressed"
			continue
		}
		if current, exists := locked.Current(name); exists {
			_, currentHash, err := governance.AutoValueKey(name, current.Intent())
			if err != nil {
				return nil, Applied{}, err
			}
			if currentHash == hash {
				result.Skipped[name] = "unchanged"
				continue
			}
		}
		write[name] = intent
		result.Written = append(result.Written, name)
	}
	return write, result, nil
}
