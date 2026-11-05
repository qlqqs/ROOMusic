package identity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

var (
	ErrInvalidPassword      = errors.New("password does not meet the rules")
	ErrWrongCurrentPassword = errors.New("current password is incorrect")
	ErrUserNotFound         = errors.New("user not found")
	ErrSessionEnded         = errors.New("session is no longer active")
)

// PasswordRuleMessage 是 HTTP 与命令行共用的新密码规则提示。
var PasswordRuleMessage = fmt.Sprintf("新密码需要 %d 到 %d 位英文字母、数字或符号", MinPasswordLength, MaxPasswordLength)

// PasswordHashError 区分哈希生成失败与数据库错误，供传输层映射为 500。
type PasswordHashError struct{ Err error }

func (e *PasswordHashError) Error() string { return "generate password hash: " + e.Err.Error() }
func (e *PasswordHashError) Unwrap() error { return e.Err }

// ResetResult 报告被重置账号是否停用；重置不改变停用状态。
type ResetResult struct{ Disabled bool }

// ChangeOwnPassword 验证当前密码，原子修改密码并保留当前会话、撤销其他会话。
func ChangeOwnPassword(ctx context.Context, db *sql.DB, userID string, sessionTokenHash []byte, current, next string) error {
	if !ValidPassword(next) {
		return ErrInvalidPassword
	}
	var previousHash string
	err := db.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id=$1::uuid AND disabled_at IS NULL`, userID).Scan(&previousHash)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrSessionEnded
	}
	if err != nil {
		return fmt.Errorf("read current password: %w", err)
	}
	if !VerifyPassword(previousHash, current) {
		return ErrWrongCurrentPassword
	}
	hash, err := HashPassword(next)
	if err != nil {
		return &PasswordHashError{Err: err}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin password change: %w", err)
	}
	defer tx.Rollback()
	// 慢哈希在事务外完成；锁定用户和当前会话后，再检查期间是否发生了变化。
	var lockedHash string
	err = tx.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id=$1::uuid AND disabled_at IS NULL FOR UPDATE`, userID).Scan(&lockedHash)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrSessionEnded
	}
	if err != nil {
		return fmt.Errorf("lock password user: %w", err)
	}
	var active int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM sessions WHERE user_id=$1::uuid AND token_hash=$2 AND revoked_at IS NULL AND expires_at>NOW() FOR UPDATE`, userID, sessionTokenHash).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrSessionEnded
	}
	if err != nil {
		return fmt.Errorf("lock password session: %w", err)
	}
	if lockedHash != previousHash {
		return ErrWrongCurrentPassword
	}
	if err := setPasswordTx(ctx, tx, userID, hash, sessionTokenHash); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit password change: %w", err)
	}
	return nil
}

// ResetPassword 按用户 ID 重置密码，同时撤销该用户的所有会话。
func ResetPassword(ctx context.Context, db *sql.DB, userID, next string) (ResetResult, error) {
	return resetPassword(ctx, db, userID, next, false)
}

// ResetPasswordByUsername 精确匹配用户名并重置密码，同时撤销所有会话。
func ResetPasswordByUsername(ctx context.Context, db *sql.DB, username, next string) (ResetResult, error) {
	return resetPassword(ctx, db, username, next, true)
}

func resetPassword(ctx context.Context, db *sql.DB, target, next string, byUsername bool) (ResetResult, error) {
	var result ResetResult
	if !ValidPassword(next) {
		return result, ErrInvalidPassword
	}
	hash, err := HashPassword(next)
	if err != nil {
		return result, &PasswordHashError{Err: err}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return result, fmt.Errorf("begin password reset: %w", err)
	}
	defer tx.Rollback()
	userID := target
	if byUsername {
		err = tx.QueryRowContext(ctx, `SELECT id::text,disabled_at IS NOT NULL FROM users WHERE username=$1 FOR UPDATE`, target).Scan(&userID, &result.Disabled)
	} else {
		err = tx.QueryRowContext(ctx, `SELECT disabled_at IS NOT NULL FROM users WHERE id=$1::uuid FOR UPDATE`, target).Scan(&result.Disabled)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ResetResult{}, ErrUserNotFound
	}
	if err != nil {
		return ResetResult{}, fmt.Errorf("lock password reset user: %w", err)
	}
	if err := setPasswordTx(ctx, tx, userID, hash, nil); err != nil {
		return ResetResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return ResetResult{}, fmt.Errorf("commit password reset: %w", err)
	}
	return result, nil
}

// setPasswordTx 在调用方已锁定用户后更新密码，并按需保留一个会话。
func setPasswordTx(ctx context.Context, tx *sql.Tx, userID, hash string, keepTokenHash []byte) error {
	result, err := tx.ExecContext(ctx, `UPDATE users SET password_hash=$2 WHERE id=$1::uuid`, userID, hash)
	if err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count password updates: %w", err)
	}
	if affected != 1 {
		return fmt.Errorf("update password affected %d users: %w", affected, sql.ErrNoRows)
	}
	if keepTokenHash == nil {
		_, err = tx.ExecContext(ctx, `UPDATE sessions SET revoked_at=NOW() WHERE user_id=$1::uuid AND revoked_at IS NULL`, userID)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE sessions SET revoked_at=NOW() WHERE user_id=$1::uuid AND revoked_at IS NULL AND token_hash<>$2`, userID, keepTokenHash)
	}
	if err != nil {
		return fmt.Errorf("revoke password sessions: %w", err)
	}
	return nil
}
