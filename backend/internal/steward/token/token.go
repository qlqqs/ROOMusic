// Package token 拥有个人访问令牌的生成、范围与存取合同，不提供 HTTP 认证入口。
package token

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"

	"github.com/qlqq/roomusic/backend/internal/identity"
)

// Scope 是签发时选择的访问范围，与工具的影响范围分开。
type Scope string

const (
	ScopeRead      Scope = "read"
	ScopeReadWrite Scope = "read_write"
	// MaxActive 是每个用户未吊销的令牌上限，包含已经过期的令牌。
	MaxActive = 20
)

var (
	ErrInvalidInput    = errors.New("令牌参数无效")
	ErrLimitReached    = errors.New("未吊销令牌已达到上限")
	ErrNotFound        = errors.New("令牌不存在")
	ErrUnauthenticated = errors.New("访问令牌无效")
)

// Entry 是可供用户查看的令牌信息，不包含哈希或明文。
type Entry struct {
	ID         string
	Name       string
	Scope      Scope
	Tail       string
	CreatedAt  time.Time
	ExpiresAt  *time.Time
	LastUsedAt *time.Time
}

// IssueInput 指定名称、范围和可选的到期时间。
type IssueInput struct {
	Name      string
	Scope     Scope
	ExpiresAt *time.Time
}

// Principal 是本次明文校验取得的当前用户、令牌标识与范围。
type Principal struct {
	User    identity.User
	TokenID string
	Scope   Scope
}

// UserReader 由身份能力提供当前启用用户，不跨包查询用户表。
type UserReader interface {
	EnabledUser(context.Context, string) (identity.User, error)
}

// Generate 返回 rmt_ 前缀和 32 字节随机数的无填充 base64url 文本。
func Generate() string {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		panic(err)
	}
	return "rmt_" + base64.RawURLEncoding.EncodeToString(value)
}

// Hash 返回令牌明文的 SHA-256，数据库只保存此摘要。
func Hash(plaintext string) []byte {
	digest := sha256.Sum256([]byte(plaintext))
	return digest[:]
}
