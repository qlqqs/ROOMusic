package auth

import (
	"context"
	"crypto/md5"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/subsonic/protocol"
)

var ErrMissingCredentials = errors.New("missing required credentials")

// Authenticate 只校验客户端凭据；登录会话与令牌认证用的副本由网页入口创建。
func (s *Service) Authenticate(ctx context.Context, request protocol.Request, source string) (identity.User, error) {
	if err := s.RequireEnabled(ctx); err != nil {
		return identity.User{}, err
	}
	if !request.HasCredentials() {
		return identity.User{}, ErrMissingCredentials
	}
	params := request.Values
	password := params.Get("p")
	verify := func(user identity.User, hash string) (bool, error) {
		if password == "" {
			return s.verifyToken(ctx, user.ID, params.Get("t"), params.Get("s"))
		}
		if encoded, ok := strings.CutPrefix(password, "enc:"); ok {
			decoded, err := hex.DecodeString(encoded)
			if err != nil {
				return false, nil
			}
			password = string(decoded)
		}
		return s.cache.verify(user.ID, hash, password), nil
	}
	user, _, _, err := identity.CheckLogin(ctx, s.db, s.throttle, params.Get("u"), source, password, verify)
	return user, err
}

func (s *Service) verifyToken(ctx context.Context, id, token, salt string) (bool, error) {
	var sealed []byte
	err := s.db.QueryRowContext(ctx, "SELECT sealed FROM subsonic_credentials WHERE user_id=$1", id).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	plaintext, err := s.secrets.Open(id, passwordPurpose, sealed)
	if err != nil {
		return false, nil
	}
	digest := md5.Sum(append(plaintext, salt...))
	supplied, err := hex.DecodeString(token)
	return err == nil && subtle.ConstantTimeCompare(digest[:], supplied) == 1, nil
}
