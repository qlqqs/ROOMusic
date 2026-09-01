package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const sessionCookieName = "roomusic_session"

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`)

func createIdentifier() string {
	identifierBytes := make([]byte, 16)
	if _, err := rand.Read(identifierBytes); err != nil {
		panic(err)
	}
	identifierBytes[6] = (identifierBytes[6] & 0x0f) | 0x40
	identifierBytes[8] = (identifierBytes[8] & 0x3f) | 0x80
	return hex.EncodeToString(identifierBytes[:4]) + "-" + hex.EncodeToString(identifierBytes[4:6]) + "-" + hex.EncodeToString(identifierBytes[6:8]) + "-" + hex.EncodeToString(identifierBytes[8:10]) + "-" + hex.EncodeToString(identifierBytes[10:])
}

func hashSessionToken(token string) []byte { digest := sha256.Sum256([]byte(token)); return digest[:] }

func generateSessionToken() string {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		panic(err)
	}
	return hex.EncodeToString(tokenBytes)
}

type authUser struct{ ID, Username, Role string }

func (application *roomusicApplication) authenticatedUser(request *http.Request) (string, error) {
	u, err := application.currentUser(request)
	return u.ID, err
}
func (application *roomusicApplication) currentUser(request *http.Request) (authUser, error) {
	cookie, err := request.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return authUser{}, errors.New("authentication required")
	}
	var user authUser
	err = application.database.connection.QueryRowContext(request.Context(), `SELECT u.id::text,u.username,u.role FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=$1 AND s.revoked_at IS NULL AND s.expires_at > NOW() AND u.disabled_at IS NULL`, hashSessionToken(cookie.Value)).Scan(&user.ID, &user.Username, &user.Role)
	if err != nil {
		return authUser{}, errors.New("authentication required")
	}
	return user, nil
}
func (application *roomusicApplication) requireAdmin(w http.ResponseWriter, r *http.Request) (authUser, bool) {
	u, err := application.currentUser(r)
	if err != nil {
		application.writeAuthenticationError(w, r)
		return authUser{}, false
	}
	if u.Role != "admin" {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "需要管理员权限")
		return authUser{}, false
	}
	return u, true
}

func (application *roomusicApplication) writeAuthenticationError(responseWriter http.ResponseWriter, request *http.Request) {
	writeAPIError(responseWriter, request, http.StatusUnauthorized, "unauthorized", "需要管理员登录")
}

func (application *roomusicApplication) requireSameOrigin(responseWriter http.ResponseWriter, request *http.Request) bool {
	origin := request.Header.Get("Origin")
	if origin == "" {
		writeAPIError(responseWriter, request, http.StatusForbidden, "origin_required", "请求缺少来源校验")
		return false
	}
	originURL, err := url.Parse(origin)
	if err != nil || originURL.User != nil || originURL.Path != "" || originURL.RawQuery != "" || originURL.Fragment != "" {
		writeAPIError(responseWriter, request, http.StatusForbidden, "origin_forbidden", "请求来源不受信任")
		return false
	}
	allowedScheme := "http"
	if application.config.SecureCookies {
		allowedScheme = "https"
	}
	if originURL.Scheme != allowedScheme {
		writeAPIError(responseWriter, request, http.StatusForbidden, "origin_forbidden", "请求来源不受信任")
		return false
	}
	if application.config.PublicURL != "" {
		publicURL, _ := url.Parse(application.config.PublicURL)
		if originURL.Host != publicURL.Host {
			writeAPIError(responseWriter, request, http.StatusForbidden, "origin_forbidden", "请求来源不受信任")
			return false
		}
		return true
	}
	if originURL.Host != request.Host {
		writeAPIError(responseWriter, request, http.StatusForbidden, "origin_forbidden", "请求来源不受信任")
		return false
	}
	return true
}

func (application *roomusicApplication) setSessionCookie(responseWriter http.ResponseWriter, token string) {
	http.SetCookie(responseWriter, &http.Cookie{Name: sessionCookieName, Value: token, Path: "/", HttpOnly: true, Secure: application.config.SecureCookies, SameSite: http.SameSiteLaxMode, Expires: time.Now().Add(24 * time.Hour)})
}

func hashPassword(password string) (string, error) {
	digest, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("generate password hash: %w", err)
	}
	return string(digest), nil
}

func verifyPassword(encodedHash, password string) bool {
	if strings.HasPrefix(encodedHash, "$2a$") || strings.HasPrefix(encodedHash, "$2b$") || strings.HasPrefix(encodedHash, "$2y$") {
		return bcrypt.CompareHashAndPassword([]byte(encodedHash), []byte(password)) == nil
	}
	// Keep validating hashes created before the bcrypt migration. New hashes
	// are always bcrypt; this branch only provides a safe transition path.
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 3 || parts[0] != "sha256" {
		return false
	}
	digest := sha256.Sum256([]byte(parts[1] + password))
	return subtle.ConstantTimeCompare([]byte(hex.EncodeToString(digest[:])), []byte(parts[2])) == 1
}
