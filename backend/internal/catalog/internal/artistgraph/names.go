package artistgraph

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"strings"
	"unicode/utf8"
)

type nameResolver struct {
	tx               *sql.Tx
	names            map[string]string
	createdArtistIDs []string
}

// prefetch 只在本次事实原文集合内按 256 项分批预取；未找到的名字仍走 ensure 创建路径。
// 哈希用于定位，不代替完整规范化键和原文校验，不能用全库缓存掩盖碰撞或遗漏新身份。
func (r *nameResolver) prefetch(ctx context.Context, names []string) error {
	type input struct {
		Raw          string `json:"raw"`
		KeyHash      string `json:"key_hash"`
		SpellingHash string `json:"spelling_hash"`
	}
	unique := uniqueIDs(names)
	for start := 0; start < len(unique); start += 256 {
		batch := unique[start:min(start+256, len(unique))]
		values := make([]input, 0, len(batch))
		keys := make(map[string]string, len(batch))
		for _, name := range batch {
			if name == "" || len(name) > 4096 || !utf8.ValidString(name) {
				return errors.New("invalid artist spelling budget")
			}
			key := textnorm.Text(name)
			if key == "" || len(key) > 65536 {
				return errors.New("invalid artist name key")
			}
			keyHash, spellingHash := sha256.Sum256([]byte(key)), sha256.Sum256([]byte(name))
			keys[name] = key
			values = append(values, input{name, hex.EncodeToString(keyHash[:]), hex.EncodeToString(spellingHash[:])})
		}
		data, err := json.Marshal(values)
		if err != nil {
			return err
		}
		rows, err := r.tx.QueryContext(ctx, `SELECT v.raw,k.normalized_key,n.id::text,n.spelling
 FROM jsonb_to_recordset($1::jsonb) AS v(raw text,key_hash text,spelling_hash text)
 LEFT JOIN artist_name_keys k ON k.normalization_version=$2 AND k.key_hash=decode(v.key_hash,'hex')
 LEFT JOIN artist_names n ON n.key_id=k.id AND n.spelling_hash=decode(v.spelling_hash,'hex')`, string(data), textnorm.Version)
		if err != nil {
			return err
		}
		for rows.Next() {
			var raw string
			var key, id, spelling sql.NullString
			if err = rows.Scan(&raw, &key, &id, &spelling); err != nil {
				rows.Close()
				return err
			}
			if key.Valid && key.String != keys[raw] {
				rows.Close()
				return errors.New("artist name hash collision")
			}
			if id.Valid {
				if !spelling.Valid || spelling.String != raw {
					rows.Close()
					return errors.New("artist spelling hash collision")
				}
				r.names[raw] = id.String
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *nameResolver) ensure(ctx context.Context, name, origin string) (string, error) {
	if id, ok := r.names[name]; ok {
		return id, nil
	}
	if name == "" || len(name) > 4096 || !utf8.ValidString(name) {
		return "", errors.New("invalid artist spelling budget")
	}
	key := textnorm.Text(name)
	if key == "" || len(key) > 65536 {
		return "", errors.New("invalid artist name key")
	}
	hash := sha256.Sum256([]byte(key))
	var keyID, stored string
	err := r.tx.QueryRowContext(ctx, `SELECT id::text,normalized_key FROM artist_name_keys WHERE normalization_version=$1 AND key_hash=$2`, textnorm.Version, hash[:]).Scan(&keyID, &stored)
	if errors.Is(err, sql.ErrNoRows) {
		keyID = uuid.New()
		id := uuid.New()
		if _, err = r.tx.ExecContext(ctx, `INSERT INTO artists(id,initial_name) VALUES($1::uuid,$2)`, id, name); err != nil {
			return "", err
		}
		// 即使 scan 署名被 M3 修正遮盖，新身份也必须在同事务获得完整派生。
		r.createdArtistIDs = append(r.createdArtistIDs, id)
		_, err = r.tx.ExecContext(ctx, `INSERT INTO artist_name_keys(id,normalization_version,normalized_key,key_hash,initial_artist_id) VALUES($1::uuid,$2,$3,$4,$5::uuid)`, keyID, textnorm.Version, key, hash[:], id)
	} else if err == nil && stored != key {
		return "", errors.New("artist name hash collision")
	}
	if err != nil {
		return "", err
	}
	spellingHash := sha256.Sum256([]byte(name))
	var id string
	err = r.tx.QueryRowContext(ctx, `SELECT id::text,spelling FROM artist_names WHERE key_id=$1::uuid AND spelling_hash=$2`, keyID, spellingHash[:]).Scan(&id, &stored)
	if errors.Is(err, sql.ErrNoRows) {
		id = uuid.New()
		_, err = r.tx.ExecContext(ctx, `INSERT INTO artist_names(id,key_id,spelling,spelling_hash,origin) VALUES($1::uuid,$2::uuid,$3,$4,$5)`, id, keyID, name, spellingHash[:], origin)
	} else if err == nil && stored != name {
		return "", errors.New("artist spelling hash collision")
	}
	if err == nil {
		r.names[name] = id
	}
	return id, err
}
func nonempty(s string) bool { return strings.TrimSpace(s) != "" }
