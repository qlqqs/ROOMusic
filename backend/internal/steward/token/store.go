package token

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// Store 使用应用提供的连接池；userID 必须来自已认证身份。
type Store struct {
	db    *sql.DB
	users UserReader
}

// NewStore 构造存储，不执行 I/O。
func NewStore(db *sql.DB, users UserReader) *Store { return &Store{db: db, users: users} }

const entryColumns = `id::text,name,scope,token_tail,created_at,expires_at,last_used_at`

type scanner interface{ Scan(...any) error }

func scanEntry(row scanner) (Entry, error) {
	var entry Entry
	err := row.Scan(&entry.ID, &entry.Name, &entry.Scope, &entry.Tail, &entry.CreatedAt, &entry.ExpiresAt, &entry.LastUsedAt)
	entry.CreatedAt = entry.CreatedAt.UTC()
	if entry.ExpiresAt != nil {
		*entry.ExpiresAt = entry.ExpiresAt.UTC()
	}
	if entry.LastUsedAt != nil {
		*entry.LastUsedAt = entry.LastUsedAt.UTC()
	}
	return entry, err
}

// Issue 在用户配额锁内计数并签发；明文只随本次成功返回，不写库。
func (s *Store) Issue(ctx context.Context, userID string, input IssueInput) (Entry, string, error) {
	input.Name = strings.TrimSpace(input.Name)
	if !utf8.ValidString(input.Name) || strings.ContainsRune(input.Name, 0) || utf8.RuneCountInString(input.Name) < 1 || utf8.RuneCountInString(input.Name) > 64 ||
		(input.Scope != ScopeRead && input.Scope != ScopeReadWrite) || (input.ExpiresAt != nil && !input.ExpiresAt.After(time.Now())) {
		return Entry{}, "", ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Entry{}, "", err
	}
	defer tx.Rollback()
	// STOK 是令牌配额的独立锁空间；计数在取得锁后的下一条语句读取。
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1,hashtext($2))`, int32(0x53544f4b), userID); err != nil {
		return Entry{}, "", err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM steward_access_tokens WHERE user_id=$1::uuid AND revoked_at IS NULL`, userID).Scan(&count); err != nil {
		return Entry{}, "", err
	}
	if count >= MaxActive {
		return Entry{}, "", ErrLimitReached
	}
	plaintext := Generate()
	entry, err := scanEntry(tx.QueryRowContext(ctx, `INSERT INTO steward_access_tokens
 (id,user_id,name,scope,token_hash,token_tail,expires_at) VALUES ($1::uuid,$2::uuid,$3,$4,$5,$6,$7)
 RETURNING `+entryColumns, uuid.New(), userID, input.Name, input.Scope, Hash(plaintext), plaintext[len(plaintext)-4:], input.ExpiresAt))
	if err != nil {
		return Entry{}, "", err
	}
	if err := tx.Commit(); err != nil {
		return Entry{}, "", err
	}
	return entry, plaintext, nil
}

// List 返回本人未吊销的令牌，按创建时间及标识稳定排序，数量由签发配额限制。
func (s *Store) List(ctx context.Context, userID string) ([]Entry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+entryColumns+` FROM steward_access_tokens
 WHERE user_id=$1::uuid AND revoked_at IS NULL ORDER BY created_at DESC,id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := make([]Entry, 0)
	for rows.Next() {
		entry, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

// Revoke 保留令牌行并写入吊销时间；不存在、已吊销和他人的令牌统一返回 ErrNotFound。
func (s *Store) Revoke(ctx context.Context, userID, id string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE steward_access_tokens SET revoked_at=clock_timestamp()
 WHERE id=$1::uuid AND user_id=$2::uuid AND revoked_at IS NULL`, id, userID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

// Validate 校验明文、吊销和到期状态，再由身份能力取得当前启用用户。
// 成功认证的使用时间每分钟最多更新一次；查询与更新错误不冒充无效凭据。
func (s *Store) Validate(ctx context.Context, plaintext string) (Principal, error) {
	var principal Principal
	var userID string
	err := s.db.QueryRowContext(ctx, `SELECT id::text,user_id::text,scope FROM steward_access_tokens
 WHERE token_hash=$1 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > now())`, Hash(plaintext)).Scan(&principal.TokenID, &userID, &principal.Scope)
	if errors.Is(err, sql.ErrNoRows) {
		return Principal{}, ErrUnauthenticated
	}
	if err != nil {
		return Principal{}, err
	}
	principal.User, err = s.users.EnabledUser(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return Principal{}, ErrUnauthenticated
	}
	if err != nil {
		return Principal{}, err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE steward_access_tokens SET last_used_at=clock_timestamp()
 WHERE id=$1::uuid AND (last_used_at IS NULL OR last_used_at <= clock_timestamp()-interval '1 minute')`, principal.TokenID)
	if err != nil {
		return Principal{}, err
	}
	return principal, nil
}
