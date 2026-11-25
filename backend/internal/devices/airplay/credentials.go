package airplay

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/qlqq/roomusic/backend/internal/devices/airplay/hap"
	"github.com/qlqq/roomusic/backend/internal/devices/airplay/rtsp"
	"github.com/qlqq/roomusic/backend/internal/platform/secret"
)

type CredentialStore struct {
	db   *sql.DB
	keys *secret.Store
}

func NewCredentialStore(db *sql.DB, keys *secret.Store) *CredentialStore {
	return &CredentialStore{db: db, keys: keys}
}

// 将双方身份、密钥作为一个整体认证加密，绑定到接收端键，配对更新以一条写入提交。
func (s *CredentialStore) Save(ctx context.Context, key string, credentials hap.Credentials) error {
	if credentials.ControllerID == "" || credentials.AccessoryID == "" ||
		len(credentials.ControllerSeed) != ed25519.SeedSize || len(credentials.AccessoryPublicKey) != ed25519.PublicKeySize {
		return errors.New("AirPlay 配对凭据不完整")
	}
	data, err := json.Marshal(credentials)
	if err != nil {
		return err
	}
	sealed, err := s.keys.Seal("instance", "airplay.pairing."+key, data)
	clear(data)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO airplay_pairings(udn, credentials_ciphertext) VALUES($1,$2)
		ON CONFLICT(udn) DO UPDATE SET credentials_ciphertext=excluded.credentials_ciphertext`, key, sealed)
	return err
}

func (s *CredentialStore) Load(ctx context.Context, key string) (hap.Credentials, error) {
	var ciphertext []byte
	err := s.db.QueryRowContext(ctx, `SELECT credentials_ciphertext FROM airplay_pairings WHERE udn=$1`, key).Scan(&ciphertext)
	if err != nil {
		return hap.Credentials{}, err
	}
	data, err := s.keys.Open("instance", "airplay.pairing."+key, ciphertext)
	if err != nil {
		return hap.Credentials{}, err
	}
	defer clear(data)
	var c hap.Credentials
	if err := json.Unmarshal(data, &c); err != nil {
		return hap.Credentials{}, err
	}
	return c, nil
}

// FinishPIN 在接收端确认配对身份后保存长期凭据。
func (s *CredentialStore) FinishPIN(ctx context.Context, key string, setup *hap.PINSetup, pin string) error {
	c, err := setup.Finish(ctx, pin)
	if err != nil {
		return err
	}
	defer clear(c.ControllerSeed)
	return s.Save(ctx, key, c)
}

// Verify 从设备键恢复配对身份，并在当前连接上验证接收端与启用加密。
func (s *CredentialStore) Verify(ctx context.Context, key string, client *rtsp.Client) (*hap.Session, error) {
	c, err := s.Load(ctx, key)
	if err != nil {
		return nil, err
	}
	defer clear(c.ControllerSeed)
	return hap.PairVerify(ctx, client, c)
}
