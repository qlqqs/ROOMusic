package dspsettings

import (
	"context"
	"database/sql"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

type Preset struct {
	ID        string                 `json:"preset_id"`
	Name      string                 `json:"name"`
	PEQ       zones.SettingsDocument `json:"peq"`
	CreatedAt time.Time              `json:"created_at"`
	UpdatedAt time.Time              `json:"updated_at"`
}

type PresetPatch struct {
	Name *string
	PEQ  *zones.SettingsDocument
}

func (s *Service) ListPresets(ctx context.Context, userID string) ([]Preset, error) {
	if !uuid.Valid(userID) {
		return nil, ErrInvalidInput
	}
	return (presetStore{s.database}).list(ctx, userID)
}

func (s *Service) CreatePreset(ctx context.Context, userID, name string, document zones.SettingsDocument) (Preset, error) {
	normalized, err := normalizePresetName(name)
	if err != nil || !uuid.Valid(userID) {
		return Preset{}, ErrInvalidInput
	}
	document, err = validatePEQ(document)
	if err != nil {
		return Preset{}, err
	}
	tx, err := s.presetTx(ctx, userID)
	if err != nil {
		return Preset{}, err
	}
	defer tx.Rollback()
	store := presetStore{tx}
	preset := Preset{ID: uuid.New(), Name: normalized, PEQ: document}
	taken, err := store.nameTaken(ctx, userID, normalized, preset.ID)
	if err != nil {
		return Preset{}, err
	}
	if taken {
		return Preset{}, ErrPresetNameTaken
	}
	count, err := store.count(ctx, userID)
	if err != nil {
		return Preset{}, err
	}
	if count >= PresetLimit {
		return Preset{}, ErrPresetLimit
	}
	result, err := store.create(ctx, userID, preset)
	if err != nil {
		return Preset{}, err
	}
	if err := tx.Commit(); err != nil {
		return Preset{}, err
	}
	return result, nil
}

func (s *Service) UpdatePreset(ctx context.Context, userID, id string, patch PresetPatch) (Preset, error) {
	if !uuid.Valid(userID) || !uuid.Valid(id) || patch.Name == nil && patch.PEQ == nil {
		return Preset{}, ErrInvalidInput
	}
	var name string
	var document zones.SettingsDocument
	var err error
	if patch.Name != nil {
		name, err = normalizePresetName(*patch.Name)
		if err != nil {
			return Preset{}, err
		}
	}
	if patch.PEQ != nil {
		document, err = validatePEQ(*patch.PEQ)
		if err != nil {
			return Preset{}, err
		}
	}
	tx, err := s.presetTx(ctx, userID)
	if err != nil {
		return Preset{}, err
	}
	defer tx.Rollback()
	store := presetStore{tx}
	preset, err := store.get(ctx, userID, id)
	if err != nil {
		return Preset{}, err
	}
	if patch.Name != nil {
		preset.Name = name
	}
	if patch.PEQ != nil {
		preset.PEQ = document
	}
	result, err := store.update(ctx, userID, preset)
	if err != nil {
		return Preset{}, err
	}
	if err := tx.Commit(); err != nil {
		return Preset{}, err
	}
	return result, nil
}

func (s *Service) DeletePreset(ctx context.Context, userID, id string) error {
	if !uuid.Valid(userID) || !uuid.Valid(id) {
		return ErrInvalidInput
	}
	tx, err := s.presetTx(ctx, userID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := (presetStore{tx}).delete(ctx, userID, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) presetTx(ctx context.Context, userID string) (*sql.Tx, error) {
	tx, err := s.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, err
	}
	if err := (presetStore{tx}).lockOwner(ctx, strings.ToLower(userID)); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func normalizePresetName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !utf8.ValidString(name) || strings.ContainsRune(name, 0) || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 64 {
		return "", ErrInvalidInput
	}
	return name, nil
}

func validatePEQ(document zones.SettingsDocument) (zones.SettingsDocument, error) {
	raw, err := document.MarshalJSON()
	if err != nil {
		return zones.SettingsDocument{}, ErrInvalidInput
	}
	normalized, err := zones.ParsePEQDocument(raw)
	if err != nil {
		return zones.SettingsDocument{}, ErrInvalidInput
	}
	return normalized, nil
}
