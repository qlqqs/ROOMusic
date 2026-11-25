package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"golang.org/x/crypto/bcrypt"
)

// ErrInvalidCredentials 不区分不存在、停用或密码错误的账号。
var ErrInvalidCredentials = errors.New("invalid credentials")

// LoginResult 包含查看者与仅供设置会话 Cookie 的不透明令牌。
type LoginResult struct {
	User  User
	Token string
}

var dummyPassword struct {
	once sync.Once
	hash []byte
	err  error
}

func compareDummyPassword(password string) error {
	dummyPassword.once.Do(func() {
		var random [32]byte
		if _, dummyPassword.err = rand.Read(random[:]); dummyPassword.err == nil {
			dummyPassword.hash, dummyPassword.err = bcrypt.GenerateFromPassword(random[:], bcrypt.DefaultCost)
		}
	})
	if dummyPassword.err != nil {
		return fmt.Errorf("prepare dummy password: %w", dummyPassword.err)
	}
	// 不存在的账号始终失败，但执行与真实 bcrypt 哈希相同成本的比较。
	_ = bcrypt.CompareHashAndPassword(dummyPassword.hash, []byte(password))
	return nil
}

func passwordFingerprint(hash string) [8]byte {
	digest := sha256.Sum256([]byte(hash))
	return [8]byte(digest[:8])
}

// CheckLogin 在核对凭据前共用网页登录的来源与账号计数。成功时返回核对过的哈希，
// 供调用方在写入会话或密码副本前复核；底层故障不计作凭据错误。
func CheckLogin(ctx context.Context, db *sql.DB, throttle *LoginThrottle, username, source, dummyInput string, verify func(User, string) (bool, error)) (User, string, *Attempt, error) {
	if err := throttle.checkSource(source); err != nil {
		return User{}, "", nil, err
	}
	user := User{Username: username}
	var hash string
	err := db.QueryRowContext(ctx, "SELECT id::text,password_hash,role FROM users WHERE username=$1 AND disabled_at IS NULL", username).Scan(&user.ID, &hash, &user.Role)
	found := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return User{}, "", nil, fmt.Errorf("read login user: %w", err)
	}
	var fingerprint [8]byte
	if found {
		fingerprint = passwordFingerprint(hash)
	}
	attempt, err := throttle.Begin(username, source, fingerprint)
	if err != nil {
		return User{}, "", nil, err
	}
	if !found {
		if err := compareDummyPassword(dummyInput); err != nil {
			attempt.Succeeded()
			return User{}, "", nil, err
		}
		return User{}, "", attempt, ErrInvalidCredentials
	}
	valid, err := verify(user, hash)
	if err != nil {
		attempt.Succeeded()
		return User{}, "", nil, err
	}
	if !valid {
		return User{}, "", attempt, ErrInvalidCredentials
	}
	attempt.Succeeded()
	return user, hash, attempt, nil
}

// Login 核对密码后，在同一事务中记录会话和密码观察结果。
func Login(ctx context.Context, db *sql.DB, throttle *LoginThrottle, username, password, source string, observers ...PasswordObserver) (LoginResult, *Attempt, error) {
	user, hash, attempt, err := CheckLogin(ctx, db, throttle, username, source, password, func(_ User, hash string) (bool, error) {
		return VerifyPassword(hash, password), nil
	})
	if err != nil {
		return LoginResult{}, attempt, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return LoginResult{}, nil, err
	}
	defer tx.Rollback()
	if err := lockVerifiedPassword(ctx, tx, user.ID, hash); err != nil {
		return LoginResult{}, attempt, err
	}
	token := GenerateSessionToken()
	if _, err = tx.ExecContext(ctx, "INSERT INTO sessions (token_hash,user_id,expires_at) VALUES ($1,$2::uuid,NOW()+INTERVAL '24 hours')", HashSessionToken(token), user.ID); err != nil {
		return LoginResult{}, nil, fmt.Errorf("create login session: %w", err)
	}
	if err := observePassword(ctx, tx, user.ID, password, observers); err != nil {
		return LoginResult{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return LoginResult{}, nil, err
	}
	return LoginResult{User: user, Token: token}, attempt, nil
}

func lockVerifiedPassword(ctx context.Context, tx *sql.Tx, id, hash string) error {
	var current string
	err := tx.QueryRowContext(ctx, "SELECT password_hash FROM users WHERE id=$1 AND disabled_at IS NULL FOR SHARE", id).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && hash != current) {
		return ErrInvalidCredentials
	}
	return err
}
