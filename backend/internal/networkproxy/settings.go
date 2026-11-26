// Package networkproxy 管理实例的出站代理设置与只写密码。
package networkproxy

import (
	"context"
	"database/sql"
	"errors"
	"net/netip"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/platform/egress"
	"github.com/qlqq/roomusic/backend/internal/platform/secret"
)

const passwordPurpose = "outbound_proxy"

var (
	ErrInvalid  = errors.New("代理设置无效")
	ErrConflict = errors.New("代理设置版本冲突")
)

type Endpoint struct {
	Scheme   string `json:"scheme"`
	Host     string `json:"host"`
	Port     *int   `json:"port"`
	Username string `json:"username"`
}

type Settings struct {
	Endpoint
	Enabled     bool  `json:"enabled"`
	PasswordSet bool  `json:"password_set"`
	Version     int64 `json:"version"`
}

type Draft struct {
	Endpoint
	Enabled  *bool   `json:"enabled"`
	Password *string `json:"password"`
	Version  *int64  `json:"version"`
}

type Service struct {
	db    *sql.DB
	keys  *secret.Store
	apply func(*egress.ProxyConfig)
	// 保存与发布按同一顺序完成，防止旧版本覆盖已发布的新版本。
	mu sync.Mutex
}

func New(db *sql.DB, keys *secret.Store, apply func(*egress.ProxyConfig)) *Service {
	return &Service{db: db, keys: keys, apply: apply}
}

func (s *Service) read(ctx context.Context) (Settings, []byte, error) {
	var v Settings
	var ciphertext []byte
	err := s.db.QueryRowContext(ctx, `SELECT enabled, scheme, host, port, username, password_ciphertext, version
		FROM outbound_proxy_settings WHERE id=1`).Scan(&v.Enabled, &v.Scheme, &v.Host, &v.Port, &v.Username, &ciphertext, &v.Version)
	v.PasswordSet = len(ciphertext) > 0
	return v, ciphertext, err
}

func (s *Service) Get(ctx context.Context) (Settings, error) {
	v, _, err := s.read(ctx)
	return v, err
}

func (s *Service) Load(ctx context.Context) error {
	v, ciphertext, err := s.read(ctx)
	if err != nil {
		return err
	}
	s.publish(v, ciphertext)
	return nil
}

func (s *Service) config(e Endpoint, ciphertext []byte) *egress.ProxyConfig {
	cfg := &egress.ProxyConfig{Scheme: e.Scheme, Host: e.Host, Port: *e.Port, Username: e.Username}
	if len(ciphertext) > 0 {
		password, err := s.keys.Open("instance", passwordPurpose, ciphertext)
		cfg.Password = string(password)
		cfg.CredentialsUnavailable = err != nil
	}
	return cfg
}

func (s *Service) publish(v Settings, ciphertext []byte) {
	if !v.Enabled {
		s.apply(nil)
		return
	}
	s.apply(s.config(v.Endpoint, ciphertext))
}

func (e *Endpoint) validate(required bool) error {
	e.Host = strings.TrimSpace(e.Host)
	if e.Scheme != "http" && e.Scheme != "socks5" {
		return ErrInvalid
	}
	if required && (e.Port == nil || e.Host == "") {
		return ErrInvalid
	}
	if e.Port != nil && (*e.Port < 1 || *e.Port > 65535) {
		return ErrInvalid
	}
	if len(e.Host) > 255 || utf8.RuneCountInString(e.Username) > 255 || strings.ContainsRune(e.Username, 0) {
		return ErrInvalid
	}
	if strings.HasPrefix(e.Host, "[") && strings.HasSuffix(e.Host, "]") {
		ip, err := netip.ParseAddr(e.Host[1 : len(e.Host)-1])
		if err != nil || !ip.Is6() {
			return ErrInvalid
		}
		e.Host = ip.String()
	}
	if _, err := netip.ParseAddr(e.Host); err != nil && (strings.ContainsAny(e.Host, ":/\\?#@[]%") || strings.IndexFunc(e.Host, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0) {
		return ErrInvalid
	}
	return nil
}

func (s *Service) Save(ctx context.Context, d Draft, actor string) (Settings, error) {
	if d.Enabled == nil || d.Version == nil {
		return Settings{}, ErrInvalid
	}
	if err := d.Endpoint.validate(*d.Enabled); err != nil {
		return Settings{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ciphertext, err := s.read(ctx)
	if err != nil {
		return Settings{}, err
	}
	if old.Version != *d.Version {
		return Settings{}, ErrConflict
	}
	if d.Password != nil {
		ciphertext = nil
		if *d.Password != "" {
			ciphertext, err = s.keys.Seal("instance", passwordPurpose, []byte(*d.Password))
			if err != nil {
				return Settings{}, err
			}
		}
	}
	v := Settings{Endpoint: d.Endpoint, Enabled: *d.Enabled, PasswordSet: len(ciphertext) > 0}
	err = s.db.QueryRowContext(ctx, `UPDATE outbound_proxy_settings SET enabled=$1,scheme=$2,host=$3,port=$4,
		username=$5,password_ciphertext=$6,updated_by=$7,updated_at=now(),version=version+1
		WHERE id=1 AND version=$8 RETURNING version`, v.Enabled, v.Scheme, v.Host, v.Port, v.Username, ciphertext, actor, old.Version).Scan(&v.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return Settings{}, ErrConflict
	}
	if err != nil {
		return Settings{}, err
	}
	s.publish(v, ciphertext)
	return v, nil
}

func (s *Service) Test(ctx context.Context, d Draft) (string, error) {
	if err := d.Endpoint.validate(true); err != nil {
		return "", err
	}
	var ciphertext []byte
	if d.Password == nil {
		_, stored, err := s.read(ctx)
		if err != nil {
			return "", err
		}
		ciphertext = stored
	}
	cfg := s.config(d.Endpoint, ciphertext)
	if d.Password != nil {
		cfg.Password = *d.Password
	}
	return egress.ProbeProxy(ctx, cfg), nil
}
