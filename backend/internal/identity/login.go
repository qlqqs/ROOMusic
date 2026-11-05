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

// Login 在查库前检查来源，查库成功后预计数，再核对密码并创建会话。
// 凭据错误时仍返回 Attempt，供 HTTP 边界记录失败与等待开始事件。
func Login(ctx context.Context, db *sql.DB, throttle *LoginThrottle, username, password, source string) (LoginResult, *Attempt, error) {
	if err := throttle.checkSource(source); err != nil {
		return LoginResult{}, nil, err
	}
	user := User{Username: username}
	var hash string
	err := db.QueryRowContext(ctx, "SELECT id::text,password_hash,role FROM users WHERE username=$1 AND disabled_at IS NULL", username).Scan(&user.ID, &hash, &user.Role)
	found := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return LoginResult{}, nil, fmt.Errorf("read login user: %w", err)
	}
	var fingerprint [8]byte
	if found {
		fingerprint = passwordFingerprint(hash)
	}
	attempt, err := throttle.Begin(username, source, fingerprint)
	if err != nil {
		return LoginResult{}, nil, err
	}
	if !found {
		if err := compareDummyPassword(password); err != nil {
			attempt.Succeeded()
			return LoginResult{}, nil, err
		}
		return LoginResult{}, attempt, ErrInvalidCredentials
	}
	if !VerifyPassword(hash, password) {
		return LoginResult{}, attempt, ErrInvalidCredentials
	}
	attempt.Succeeded()
	token := GenerateSessionToken()
	_, err = db.ExecContext(ctx, "INSERT INTO sessions (token_hash,user_id,expires_at) VALUES ($1,$2::uuid,NOW()+INTERVAL '24 hours')", HashSessionToken(token), user.ID)
	if err != nil {
		return LoginResult{}, nil, fmt.Errorf("create login session: %w", err)
	}
	return LoginResult{User: user, Token: token}, attempt, nil
}
