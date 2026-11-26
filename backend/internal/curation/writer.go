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
	WriterLevel catalog.AutoWriterLevel `json:"-"`
	Kind        string
	Provider    string
	Basis       string
}

type Request struct {
	ArtistCredit *ReleaseArtistCredit

	local                       *localSubmission
	verification                *verificationWrite
	ExpectedRevision            int64
	ConfirmedBy, ConversationID string
	OnlyEmpty                   bool
	ExpectedScan                map[string]json.RawMessage
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
	ArtistCredit                *ReleaseArtistCredit
	TrackID, ReleaseID          string
	ExpectedRevision            int64
	ConfirmedBy, ConversationID string
	OnlyEmpty                   bool
	ExpectedScan                map[string]json.RawMessage
	Fields                      map[string]catalog.CorrectionIntent
	Source                      Source
	BatchID, Key, RequestID     string
}

func (w *Writer) ApplyTrack(ctx context.Context, r TrackRequest) (Applied, error) {
	return w.applyFields(ctx, Request{ArtistCredit: r.ArtistCredit, ReleaseID: r.ReleaseID, ExpectedRevision: r.ExpectedRevision, ConfirmedBy: r.ConfirmedBy, ConversationID: r.ConversationID, OnlyEmpty: r.OnlyEmpty, ExpectedScan: r.ExpectedScan, Fields: r.Fields, Source: r.Source, BatchID: r.BatchID, Key: r.Key, RequestID: r.RequestID}, strings.ToLower(r.TrackID), true)
}

type autoFieldTarget interface {
	Current(string) (catalog.AutoValue, bool)
	EditRevision() int64
	Write(context.Context, string, map[string]catalog.CorrectionIntent, catalog.AutoWriterLevel, map[string]json.RawMessage) (operations.Outcome, error)
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
	level := source.WriterLevel
	if level == 0 {
		level = catalog.LevelModelLocal
		if source.Kind == "source" {
			level = catalog.LevelProgramExternal
		}
	}
	if level < catalog.LevelModelLocal || level > catalog.LevelProgramExternal || (source.Kind == "source") != (level == catalog.LevelProgramExternal) {
		return Applied{}, operations.Invalid("invalid_input", "写入方级别与来源不符")
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
	legacyFingerprint := operations.Fingerprint(fingerprint)
	fingerprint["writer_level"] = level
	if len(request.ExpectedScan) > 0 {
		inputs := map[string]json.RawMessage{}
		for field := range fields {
			inputs[field] = request.ExpectedScan[field]
		}
		fingerprint["scan_basis"] = inputs
	}
	spec := operations.Spec{
		ActorID: actor, Key: key, Type: kind, BatchID: request.BatchID, RequestID: request.RequestID,
		Origin:      &operations.Origin{ConversationID: request.ConversationID, Source: "curation", ProviderKind: source.Kind, Provider: source.Provider, Basis: source.Basis},
		Fingerprint: operations.Fingerprint(fingerprint),
	}
	if request.local != nil {
		spec.Origin.Mode = "local_cleanup"
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
		if request.local != nil {
			if _, err := request.local.read(ctx, tx); err != nil {
				return operations.Outcome{}, err
			}
		}
		if request.verification != nil {
			if err := request.verification.check(ctx, tx); err != nil {
				return operations.Outcome{}, err
			}
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
		write, applied, err = filterAutoFields(ctx, tx, target, id, definitions, locked, fields, request.OnlyEmpty, level)
		if err != nil {
			return operations.Outcome{}, err
		}
		if len(write) == 0 {
			return operations.Outcome{}, operations.Conflict("nothing_to_apply", "字段已占用、相同、已有更高级别的值或已标记为不再提出")
		}
		out, err := locked.Write(ctx, operationID, write, level, request.ExpectedScan)
		if err != nil {
			return operations.Outcome{}, err
		}
		applied.OperationID = operationID
		out.Response = applied
		artistField := "album_artist"
		if track {
			artistField = "artist"
		}
		if _, changed := write[artistField]; changed {
			proof, err := fieldArtistCredit(ctx, tx, request.ArtistCredit, level, releaseID, trackID)
			if err != nil {
				return operations.Outcome{}, err
			}
			if proof != nil {
				out.Response = struct {
					Applied
					ArtistCredit *fieldCreditProof `json:"artist_credit"`
				}{applied, proof}
				for _, link := range proof.Links {
					out.Targets = append(out.Targets, operations.Target{Type: operations.TargetArtist, ID: link.ObjectID})
				}
			}
			if err := refreshFieldArtistLinks(ctx, tx, releaseID, operationID, proof); err != nil {
				return operations.Outcome{}, err
			}
		}
		if request.verification != nil {
			err = request.verification.finish(ctx, tx, operationID)
		}
		return out, err
	})
	if err != nil {
		var failure *operations.Failure
		if errors.As(err, &failure) {
			switch failure.Code {
			case "nothing_to_apply":
				return applied, err
			case "idempotency_conflict":
				return w.legacyAutoReplay(ctx, request, spec, level, legacyFingerprint, err)
			}
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

func filterAutoFields(ctx context.Context, tx *sql.Tx, target, id string, definitions []catalog.CorrectableField, locked autoFieldTarget, fields map[string]catalog.CorrectionIntent, onlyEmpty bool, level catalog.AutoWriterLevel) (map[string]catalog.CorrectionIntent, Applied, error) {
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
		current, exists := locked.Current(name)
		if exists && level < current.EffectiveLevel() {
			result.Skipped[name] = "lower_priority"
			continue
		}
		if onlyEmpty {
			_, manual := corrections[name]
			if manual || exists {
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
		if exists {
			_, currentHash, err := governance.AutoValueKey(name, current.Intent())
			if err != nil {
				return nil, Applied{}, err
			}
			if currentHash == hash && current.EffectiveLevel() == level {
				result.Skipped[name] = "unchanged"
				continue
			}
		}
		write[name] = intent
		result.Written = append(result.Written, name)
	}
	return write, result, nil
}

// 0088 之前的操作指纹没有级别。只重放旧写入入口原本就能产生的请求。
func (w *Writer) legacyAutoReplay(ctx context.Context, r Request, spec operations.Spec, level catalog.AutoWriterLevel, fingerprint string, conflict error) (Applied, error) {
	legacyLevel := catalog.LevelProgramExternal
	if r.Source.Kind == "model" {
		legacyLevel = catalog.LevelModelLocal
		if r.OnlyEmpty {
			legacyLevel = catalog.LevelModelExternal
		}
	}
	if level != legacyLevel {
		return Applied{}, conflict
	}
	var raw []byte
	err := w.db.QueryRowContext(ctx, `SELECT response FROM operations WHERE actor_id=$1 AND idempotency_key=$2
	AND fingerprint=$3 AND origin->>'provider_kind'=$4`, spec.ActorID, spec.Key, fingerprint, r.Source.Kind).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Applied{}, conflict
	}
	if err != nil {
		return Applied{}, err
	}
	var result Applied
	err = json.Unmarshal(raw, &result)
	return result, err
}
