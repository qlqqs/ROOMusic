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
	ExpectedRevision            int64
	ConfirmedBy, ConversationID string
	OnlyEmpty                   bool
	ReleaseID                   string
	Fields                      map[string]catalog.CorrectionIntent
	Source                      Source
	BatchID                     string
	Key                         string
	RequestID                   string
}

type Applied struct {
	OperationID string            `json:"operation_id"`
	Written     []string          `json:"written"`
	Skipped     map[string]string `json:"skipped"`
}

var providerPattern = regexp.MustCompile(`^[a-z0-9._:-]{1,64}$`)

func (w *Writer) Apply(ctx context.Context, request Request) (Applied, error) {
	return w.applyFields(ctx, request, "", false)
}

type TrackRequest struct {
	TrackID, ReleaseID          string
	ExpectedRevision            int64
	ConfirmedBy, ConversationID string
	OnlyEmpty                   bool
	Fields                      map[string]catalog.CorrectionIntent
	Source                      Source
	BatchID, Key, RequestID     string
}

func (w *Writer) ApplyTrack(ctx context.Context, r TrackRequest) (Applied, error) {
	return w.applyFields(ctx, Request{ReleaseID: r.ReleaseID, ExpectedRevision: r.ExpectedRevision, ConfirmedBy: r.ConfirmedBy, ConversationID: r.ConversationID, OnlyEmpty: r.OnlyEmpty, Fields: r.Fields, Source: r.Source, BatchID: r.BatchID, Key: r.Key, RequestID: r.RequestID}, strings.ToLower(r.TrackID), true)
}

type autoFieldTarget interface {
	Current(string) (catalog.AutoValue, bool)
	EditRevision() int64
	Write(context.Context, string, map[string]catalog.CorrectionIntent) (operations.Outcome, error)
}

func (w *Writer) applyFields(ctx context.Context, request Request, trackID string, track bool) (Applied, error) {
	key, ok := operations.NormalizeKey(request.Key)
	if !ok || (track && !uuid.Valid(trackID)) || ((!track || request.ReleaseID != "") && !uuid.Valid(request.ReleaseID)) || (request.BatchID != "" && !uuid.Valid(request.BatchID)) {
		return Applied{}, operations.Invalid("invalid_input", "发行、批次或幂等键无效")
	}
	source := request.Source
	if (source.Kind != "source" && source.Kind != "model") || !providerPattern.MatchString(source.Provider) ||
		len(source.Basis) > 500 || !utf8.ValidString(source.Basis) || strings.ContainsRune(source.Basis, '\x00') {
		return Applied{}, operations.Invalid("invalid_input", "自动整理来源或依据无效")
	}
	normalize := governance.NormalizeAutoFields
	kind, target, idField, id := operations.TypeReleaseAutoCorrection, "release", "release_id", strings.ToLower(request.ReleaseID)
	definitions := catalog.CorrectableFields
	if track {
		normalize = governance.NormalizeTrackAutoFields
		kind, target, idField, id = governance.TypeTrackAutoCorrection, "track", "track_id", trackID
		definitions = catalog.TrackCorrectableFields
	}
	fields, err := normalize(request.Fields)
	if err != nil {
		return Applied{}, err
	}
	releaseID := strings.ToLower(request.ReleaseID)
	actor, err := w.writerActor(ctx, request.BatchID, request.ConfirmedBy, request.ConversationID)
	if err != nil {
		return Applied{}, err
	}
	fingerprint := map[string]any{"type": kind, idField: id, "fields": fields, "provider": source.Provider}
	if request.ExpectedRevision != 0 {
		fingerprint["expected_revision"] = request.ExpectedRevision
	}
	if request.OnlyEmpty {
		fingerprint["only_empty"] = true
	}
	spec := operations.Spec{
		ActorID: actor, Key: key, Type: kind, BatchID: request.BatchID, RequestID: request.RequestID,
		Origin:      &operations.Origin{ConversationID: request.ConversationID, Source: "curation", ProviderKind: source.Kind, Provider: source.Provider, Basis: source.Basis},
		Fingerprint: operations.Fingerprint(fingerprint),
	}
	var applied Applied
	result, err := operations.Run(ctx, w.db, spec, func(ctx context.Context, tx *sql.Tx, operationID string) (operations.Outcome, error) {
		var locked autoFieldTarget
		var err error
		if track {
			var t *governance.LockedAutoTrack
			t, err = governance.LockAutoTrack(ctx, tx, id)
			if err == nil {
				if releaseID != "" && releaseID != t.ReleaseID() {
					return operations.Outcome{}, operations.Conflict("item_stale", "曲目所属发行已变更")
				}
				releaseID = t.ReleaseID()
				locked = t
			}
		} else {
			locked, err = governance.LockAutoRelease(ctx, tx, releaseID)
		}
		if err != nil {
			return operations.Outcome{}, err
		}
		if err := checkWrite(ctx, tx, spec, request.ConfirmedBy, releaseID, request.ExpectedRevision, locked.EditRevision()); err != nil {
			return operations.Outcome{}, err
		}
		if request.ExpectedRevision != 0 && request.Source.Kind == "model" {
			manual, err := loadManualFields(ctx, tx, target, id)
			if err != nil {
				return operations.Outcome{}, err
			}
			for field := range fields {
				if _, ok := manual[field]; ok {
					return operations.Outcome{}, operations.Conflict("manual_override", "字段已有人工修正")
				}
			}
		}
		var write map[string]catalog.CorrectionIntent
		write, applied, err = filterAutoFields(ctx, tx, target, id, definitions, locked, fields, request.OnlyEmpty)
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

func loadManualFields(ctx context.Context, tx *sql.Tx, target, id string) (map[string]catalog.CorrectionIntent, error) {
	if target == "track" {
		return catalog.LoadTrackCorrectionIntents(ctx, tx, id)
	}
	return catalog.LoadCorrectionIntents(ctx, tx, id)
}

func filterAutoFields(ctx context.Context, tx *sql.Tx, target, id string, definitions []catalog.CorrectableField, locked autoFieldTarget, fields map[string]catalog.CorrectionIntent, onlyEmpty bool) (map[string]catalog.CorrectionIntent, Applied, error) {
	var corrections map[string]catalog.CorrectionIntent
	if onlyEmpty {
		var err error
		corrections, err = loadManualFields(ctx, tx, target, id)
		if err != nil {
			return nil, Applied{}, err
		}
	}
	write := make(map[string]catalog.CorrectionIntent, len(fields))
	result := Applied{Written: []string{}, Skipped: map[string]string{}}
	for _, field := range definitions {
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
		suppressed, err := suppressionExists(ctx, tx, target, id, name, hash)
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
