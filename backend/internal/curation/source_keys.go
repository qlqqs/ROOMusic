package curation

import (
	"context"
	"database/sql"
	"slices"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

type KeyStore interface {
	Seal(userID, purpose string, plaintext []byte) ([]byte, error)
	Open(userID, purpose string, ciphertext []byte) ([]byte, error)
}

type SourceKeySettings struct {
	Name       string `json:"name"`
	Configured bool   `json:"configured"`
	Optional   bool   `json:"optional,omitempty"`
	LastFour   string `json:"last_four"`
	Status     string `json:"status"`
}

func (s *Sources) WithKeyStore(keys KeyStore) *Sources {
	s.keys = keys
	return s
}

func (s *Sources) keyedSource(id string) (SourceRegistration, error) {
	r, ok := s.lookup(id)
	if !ok {
		return r, unknownSource()
	}
	if len(r.Declaration.Keys) == 0 {
		return r, operations.Invalid("key_not_supported", "这个外部源不使用密钥")
	}
	return r, nil
}

func (s *Sources) namedKey(id, name string) (SourceRegistration, error) {
	r, err := s.keyedSource(id)
	if err == nil && !slices.Contains(r.Declaration.Keys, name) {
		err = operations.NotFound("not_found", "外部源密钥名称不存在")
	}
	return r, err
}

func sourceKeyPurpose(id, name string) string {
	return "curation.source_key." + id + "." + name
}

func (s *Sources) SetKey(ctx context.Context, id, name, actorID, key string) (SourceSettings, error) {
	r, err := s.namedKey(id, name)
	if err != nil {
		return SourceSettings{}, err
	}
	key = strings.TrimSpace(key)
	if len(key) < 1 || len(key) > 128 || strings.IndexFunc(key, func(c rune) bool { return c < ' ' || c > '~' }) >= 0 {
		return SourceSettings{}, operations.Invalid("invalid_input", "密钥须为 1 到 128 个可打印的 ASCII 字符")
	}
	ciphertext, err := s.keys.Seal("instance", sourceKeyPurpose(id, name), []byte(key))
	if err != nil {
		return SourceSettings{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SourceSettings{}, err
	}
	defer tx.Rollback()
	if err := lockPluginSettings(ctx, tx, r, false); err != nil {
		return SourceSettings{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO curation_source_keys(source,name,updated_by,ciphertext,last_four)
 VALUES($1,$2,$3,$4,$5)
 ON CONFLICT(source,name) DO UPDATE SET updated_by=excluded.updated_by,updated_at=now(),ciphertext=excluded.ciphertext,
 last_four=excluded.last_four,status='unchecked'`, id, name, actorID, ciphertext, key[max(0, len(key)-4):])
	if err != nil {
		return SourceSettings{}, err
	}
	return s.committedSourceState(ctx, tx, r)
}

func (s *Sources) ClearKey(ctx context.Context, id, name, actorID string) (SourceSettings, error) {
	r, err := s.namedKey(id, name)
	if err != nil {
		return SourceSettings{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SourceSettings{}, err
	}
	defer tx.Rollback()
	if err := lockPluginSettings(ctx, tx, r, false); err != nil {
		return SourceSettings{}, err
	}
	// 删除的行无法保存变更时间，通过源设置通知调度器。
	_, err = tx.ExecContext(ctx, `WITH removed AS (
 DELETE FROM curation_source_keys WHERE source=$1 AND name=$2 RETURNING source)
 INSERT INTO curation_source_settings(source,enabled,updated_by) SELECT source,$3,$4 FROM removed
 ON CONFLICT(source) DO UPDATE SET updated_by=excluded.updated_by,updated_at=now()`, id, name, r.Declaration.DefaultEnabled, actorID)
	if err != nil {
		return SourceSettings{}, err
	}
	return s.committedSourceState(ctx, tx, r)
}

func loadKeySettings(ctx context.Context, tx *sql.Tx, d sources.Declaration) ([]SourceKeySettings, error) {
	if len(d.Keys) == 0 {
		return nil, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT names.name,k.name IS NOT NULL,COALESCE(k.last_four,''),COALESCE(k.status,'unchecked')
 FROM unnest($2::text[]) WITH ORDINALITY AS names(name,position)
 LEFT JOIN curation_source_keys k ON k.source=$1 AND k.name=names.name ORDER BY names.position`, d.ID, d.Keys)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := make([]SourceKeySettings, 0, len(d.Keys))
	for rows.Next() {
		var key SourceKeySettings
		if err := rows.Scan(&key.Name, &key.Configured, &key.LastFour, &key.Status); err != nil {
			return nil, err
		}
		key.Optional = slices.Contains(d.OptionalKeys, key.Name)
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

type keyUseContext struct{}

type keyUse struct {
	ciphertext []byte
	status     string
}

func (s *Sources) Key(ctx context.Context, id, name string) (string, error) {
	if _, err := s.namedKey(id, name); err != nil {
		return "", err
	}
	keys, err := s.readKeys(ctx, id, []string{name}, nil)
	return keys[name], err
}

func (s *Sources) Keys(ctx context.Context, id string) (map[string]string, error) {
	r, err := s.keyedSource(id)
	if err != nil {
		return nil, err
	}
	return s.readKeys(ctx, id, r.Declaration.Keys, r.Declaration.OptionalKeys)
}

// 每次出站前在同一数据库快照中取出所需密钥，按本次密文回写结果。
func (s *Sources) readKeys(ctx context.Context, id string, names, optional []string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name,ciphertext,status FROM curation_source_keys
 WHERE source=$1 AND name=ANY($2::text[])`, id, names)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	stored := make(map[string]keyUse, len(names))
	for rows.Next() {
		var name string
		var key keyUse
		if err := rows.Scan(&name, &key.ciphertext, &key.status); err != nil {
			return nil, err
		}
		stored[name] = key
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(names))
	for _, name := range names {
		key, exists := stored[name]
		if !exists {
			if slices.Contains(optional, name) {
				continue
			}
			return nil, disabledSource("key_missing")
		}
		if key.status == "invalid" {
			return nil, disabledSource("key_invalid")
		}
		plain, err := s.keys.Open("instance", sourceKeyPurpose(id, name), key.ciphertext)
		if err != nil {
			if err := s.markKeys(ctx, id, []string{name}, [][]byte{key.ciphertext}, "invalid"); err != nil {
				return nil, err
			}
			return nil, disabledSource("key_invalid")
		}
		out[name] = string(plain)
	}
	if used, ok := ctx.Value(keyUseContext{}).(map[string]keyUse); ok {
		for _, name := range names {
			if key, ok := stored[name]; ok {
				used[name] = key
			} else {
				delete(used, name)
			}
		}
	}
	return out, nil
}

func (s *Sources) markKeys(ctx context.Context, id string, names []string, ciphertexts [][]byte, status string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE curation_source_keys AS k SET status=$4
 FROM unnest($2::text[],$3::bytea[]) AS used(name,ciphertext)
 WHERE k.source=$1 AND k.name=used.name AND k.ciphertext=used.ciphertext
 AND k.status<>$4 AND (k.status='unchecked' OR $4='invalid')`, id, names, ciphertexts, status)
	return err
}
