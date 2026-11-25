package auth

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/secret"
)

const passwordPurpose = "subsonic-password"

var (
	ErrDisabled   = errors.New("subsonic disabled")
	errSealFailed = errors.New("subsonic credential encryption failed")
)

type Service struct {
	db       *sql.DB
	secrets  *secret.Store
	throttle *identity.LoginThrottle
	logger   *slog.Logger
	cache    passwordCache
}

func New(db *sql.DB, secrets *secret.Store, throttle *identity.LoginThrottle, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{db: db, secrets: secrets, throttle: throttle, logger: logger}
}

func (s *Service) Enabled(ctx context.Context) (bool, error) {
	var enabled bool
	err := s.db.QueryRowContext(ctx, "SELECT subsonic_enabled FROM subsonic_settings WHERE id=1").Scan(&enabled)
	return enabled, err
}

func (s *Service) RequireEnabled(ctx context.Context) error {
	enabled, err := s.Enabled(ctx)
	if err != nil {
		return err
	}
	if !enabled {
		return ErrDisabled
	}
	return nil
}

func (s *Service) SetEnabled(ctx context.Context, enabled bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "UPDATE subsonic_settings SET subsonic_enabled=$1 WHERE id=1", enabled); err != nil {
		return err
	}
	if !enabled {
		if _, err := tx.ExecContext(ctx, "DELETE FROM subsonic_credentials"); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if !enabled {
		s.cache.clear()
	}
	return nil
}

type Status struct {
	Enabled       bool    `json:"enabled"`
	Ready         bool    `json:"ready"`
	Username      string  `json:"username"`
	JukeboxZoneID *string `json:"jukebox_zone_id"`
}

func (s *Service) Status(ctx context.Context, user identity.User) (Status, error) {
	return s.status(ctx, s.db, user)
}

func (s *Service) status(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, user identity.User) (Status, error) {
	state := Status{Username: user.Username}
	var sealed []byte
	err := db.QueryRowContext(ctx, `SELECT subsonic_enabled, sealed, `+jukeboxZoneSelection+` FROM subsonic_settings
        LEFT JOIN subsonic_credentials ON user_id=$1 WHERE id=1`, user.ID).Scan(&state.Enabled, &sealed, &state.JukeboxZoneID)
	if err == nil && state.Enabled && sealed != nil {
		_, openErr := s.secrets.Open(user.ID, passwordPurpose, sealed)
		state.Ready = openErr == nil
	}
	return state, err
}

// ObservePassword 与关闭开关共用单行设置锁，保证清空副本后没有并发写回。
func (s *Service) ObservePassword(ctx context.Context, tx *sql.Tx, id, password string) error {
	err := s.savePassword(ctx, tx, id, password)
	if errors.Is(err, errSealFailed) {
		s.logger.WarnContext(ctx, "Subsonic 密码副本加密失败", "event", "subsonic.credential.seal_failed",
			"module", "subsonic", "user_id", id, "error_kind", "secret_unavailable")
		return nil
	}
	if errors.Is(err, ErrDisabled) {
		return nil
	}
	return err
}

func (s *Service) savePassword(ctx context.Context, tx *sql.Tx, id, password string) error {
	var enabled bool
	if err := tx.QueryRowContext(ctx, "SELECT subsonic_enabled FROM subsonic_settings WHERE id=1 FOR SHARE").Scan(&enabled); err != nil {
		return err
	}
	if !enabled {
		return ErrDisabled
	}
	sealed, err := s.secrets.Seal(id, passwordPurpose, []byte(password))
	if err != nil {
		return errSealFailed
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO subsonic_credentials (user_id,sealed) VALUES ($1,$2)
        ON CONFLICT (user_id) DO UPDATE SET sealed=EXCLUDED.sealed, updated_at=now()`, id, sealed)
	return err
}

func (s *Service) Confirm(ctx context.Context, user identity.User, tokenHash []byte, password string) error {
	if err := s.RequireEnabled(ctx); err != nil {
		return err
	}
	return identity.ConfirmPassword(ctx, s.db, s.throttle, user.ID, tokenHash, password, s.savePassword)
}

// Forget 在身份变更已提交后调用；每次请求仍会重新读取账号状态和当前哈希。
func (s *Service) Forget(id string) {
	s.cache.forget(id)
}
