// Package plugins 管理经管理员确认的数据源插件。
package plugins

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/qlqq/roomusic/backend/internal/plugins/runtime"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

type Metadata struct {
	Manifest     Manifest             `json:"manifest"`
	Capabilities []sources.Capability `json:"capabilities"`
	SHA256       string               `json:"sha256"`
}

type Preview struct {
	Metadata
	Token string `json:"preview_token"`
}

type Item struct {
	Metadata
	Enabled        bool      `json:"enabled"`
	DisabledReason string    `json:"disabled_reason,omitempty"`
	InstalledAt    time.Time `json:"installed_at"`
}

type pending struct {
	Preview
	expires time.Time
}

var ErrInvalid = errors.New("插件包或预检信息无效")
var ErrNotFound = errors.New("插件不存在")

type Store struct {
	db        *sql.DB
	dir, mock string
	logger    *slog.Logger
	engine    *runtime.Engine
	keys      func(context.Context, string) (map[string]string, error)
	// 安装和删除在调用结束后替换目录；注册通知只发布快照。
	mu        sync.RWMutex
	installed []*Source
	previews  map[string]pending
	changed   func([]*Source)
}

type Source struct {
	Metadata
	store                  *Store
	wasmHash, manifestHash string
	installedAt            time.Time
	blocked                atomic.Bool
	upstreams              map[string]*endpoint
}

func Open(ctx context.Context, db *sql.DB, data, mock string, logger *slog.Logger, keys func(context.Context, string) (map[string]string, error), changed func([]*Source)) (*Store, error) {
	s := &Store{db: db, dir: filepath.Join(data, "plugins"), mock: mock, logger: logger, keys: keys, previews: map[string]pending{}, changed: changed}
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return nil, err
	}
	// 预检令牌只在当前进程有效。
	if err := os.RemoveAll(filepath.Join(s.dir, ".preview")); err != nil {
		return nil, err
	}
	if err := os.Mkdir(filepath.Join(s.dir, ".preview"), 0700); err != nil {
		return nil, err
	}
	engine, err := runtime.New(filepath.Join(s.dir, ".cache"), logger)
	if err != nil {
		return nil, err
	}
	s.engine = engine
	rows, err := db.QueryContext(ctx, `SELECT manifest,capabilities,sha256,wasm_hash,manifest_hash,installed_at,disabled_reason FROM source_plugins ORDER BY installed_at,id`)
	if err != nil {
		s.Close()
		return nil, err
	}
	for rows.Next() {
		p := &Source{store: s}
		var raw, caps []byte
		var reason string
		err = rows.Scan(&raw, &caps, &p.SHA256, &p.wasmHash, &p.manifestHash, &p.installedAt, &reason)
		if err == nil {
			err = json.Unmarshal(raw, &p.Manifest)
		}
		if err == nil {
			err = json.Unmarshal(caps, &p.Capabilities)
		}
		if err != nil {
			break
		}
		p.blocked.Store(reason != "")
		p.initEndpoints()
		s.installed = append(s.installed, p)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		s.Close()
		return nil, err
	}
	for _, p := range s.installed {
		if !p.blocked.Load() {
			if _, err = p.verified(ctx); err != nil && !errors.Is(err, ErrInvalid) {
				s.Close()
				return nil, err
			}
		}
	}
	changed(slices.Clone(s.installed))
	return s, nil
}

func (s *Store) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.engine.Close(context.Background())
}
func (p *Source) Reason() string {
	if p.blocked.Load() {
		return "hash_changed"
	}
	return ""
}

func (p *Source) verified(ctx context.Context) ([]byte, error) {
	dir := filepath.Join(p.store.dir, p.Manifest.ID)
	raw, e := readFile(filepath.Join(dir, "manifest.json"), 64<<10)
	wasm, e2 := readFile(filepath.Join(dir, "plugin.wasm"), MaxPackage)
	if e == nil && e2 == nil && checksum(raw) == p.manifestHash && checksum(wasm) == p.wasmHash {
		return wasm, nil
	}
	_, err := p.store.db.ExecContext(ctx, `WITH stopped AS (UPDATE source_plugins SET disabled_reason='hash_changed' WHERE id=$1 RETURNING id)
 UPDATE curation_source_settings SET enabled=false,updated_at=now() WHERE source IN (SELECT 'plugin:'||id FROM stopped)`, p.Manifest.ID)
	if err != nil {
		return nil, err
	}
	p.blocked.Store(true)
	return nil, ErrInvalid
}

var capabilities = []sources.Capability{sources.CoverArt, sources.LyricsLookup, sources.ArtistImageLookup, sources.ArtistBioLookup}

func (s *Store) Preview(ctx context.Context, data []byte) (Preview, error) {
	b, err := unpack(data)
	if err != nil {
		return Preview{}, errors.Join(ErrInvalid, err)
	}
	s.mu.RLock()
	res, err := s.engine.Call(ctx, b.manifest.ID, func(context.Context) ([]byte, error) { return b.wasm, nil }, "describe", []byte("{}"), nil)
	s.mu.RUnlock()
	var desc struct {
		Capabilities []sources.Capability `json:"capabilities"`
	}
	if err != nil || res.Code != 0 || !strict(res.Output, &desc) {
		return Preview{}, errors.Join(ErrInvalid, errors.New("describe 调用或能力列表无效"))
	}
	declared := make(map[sources.Capability]bool, len(desc.Capabilities))
	for _, c := range desc.Capabilities {
		if !slices.Contains(capabilities, c) {
			return Preview{}, fmt.Errorf("%w：describe 声明了 ABI 1 不支持的能力 %q", ErrInvalid, c)
		}
		if declared[c] {
			return Preview{}, fmt.Errorf("%w：describe 重复声明能力 %q", ErrInvalid, c)
		}
		if !res.Exports[string(c)] {
			return Preview{}, fmt.Errorf("%w：describe 声明了能力 %q，但模块未导出该函数", ErrInvalid, c)
		}
		declared[c] = true
	}
	var exports []sources.Capability
	for _, c := range capabilities {
		if res.Exports[string(c)] {
			if !declared[c] {
				return Preview{}, fmt.Errorf("%w：模块导出了能力 %q，但 describe 未声明该能力", ErrInvalid, c)
			}
			exports = append(exports, c)
		}
	}
	if len(exports) == 0 {
		return Preview{}, fmt.Errorf("%w：插件至少需要提供一项能力", ErrInvalid)
	}
	prev := Preview{Metadata: Metadata{b.manifest, exports, b.hash}, Token: rand.Text()}
	s.mu.Lock()
	defer s.mu.Unlock()
	for token, v := range s.previews {
		if !time.Now().Before(v.expires) {
			s.dropPreview(token)
		}
	}
	if err = os.WriteFile(s.previewPath(prev.Token), data, 0600); err != nil {
		return Preview{}, err
	}
	s.previews[prev.Token] = pending{prev, time.Now().Add(15 * time.Minute)}
	return prev, nil
}

func (s *Store) previewPath(token string) string { return filepath.Join(s.dir, ".preview", token) }
func (s *Store) dropPreview(token string) {
	delete(s.previews, token)
	_ = os.Remove(s.previewPath(token))
}

func (s *Store) index(id string) int {
	return slices.IndexFunc(s.installed, func(p *Source) bool { return p.Manifest.ID == id })
}

func (s *Store) Install(ctx context.Context, token string) (Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.previews[token]
	if !ok || !time.Now().Before(v.expires) {
		return Item{}, ErrInvalid
	}
	raw, err := readFile(s.previewPath(token), MaxPackage)
	if err != nil {
		return Item{}, err
	}
	if checksum(raw) != v.SHA256 {
		return Item{}, ErrInvalid
	}
	b, err := unpack(raw)
	if err != nil {
		return Item{}, ErrInvalid
	}
	id := b.manifest.ID
	p := &Source{Metadata: v.Metadata, wasmHash: checksum(b.wasm), manifestHash: checksum(b.raw), store: s}
	p.initEndpoints()
	stage, err := os.MkdirTemp(s.dir, ".stage-")
	if err != nil {
		return Item{}, err
	}
	defer os.RemoveAll(stage)
	for name, data := range map[string][]byte{"manifest.json": b.raw, "plugin.wasm": b.wasm} {
		if err = os.WriteFile(filepath.Join(stage, name), data, 0600); err != nil {
			return Item{}, err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Item{}, err
	}
	defer tx.Rollback()
	caps, _ := json.Marshal(p.Capabilities)
	err = tx.QueryRowContext(ctx, `INSERT INTO source_plugins(id,manifest,capabilities,sha256,wasm_hash,manifest_hash) VALUES($1,$2,$3,$4,$5,$6)
 ON CONFLICT(id) DO UPDATE SET manifest=excluded.manifest,capabilities=excluded.capabilities,sha256=excluded.sha256,wasm_hash=excluded.wasm_hash,manifest_hash=excluded.manifest_hash,disabled_reason=''
 RETURNING installed_at`, id, b.raw, caps, p.SHA256, p.wasmHash, p.manifestHash).Scan(&p.installedAt)
	if err != nil {
		return Item{}, err
	}
	if err = resetSource(ctx, tx, id); err != nil {
		return Item{}, err
	}
	dir := filepath.Join(s.dir, id)
	backup := stage + ".old"
	moved := false
	if err = os.Rename(dir, backup); err == nil {
		moved = true
	} else if !os.IsNotExist(err) {
		return Item{}, err
	}
	if err = os.Rename(stage, dir); err == nil {
		err = tx.Commit()
		if err != nil {
			os.RemoveAll(dir)
		}
	}
	if err != nil {
		if moved {
			if restore := os.Rename(backup, dir); restore != nil {
				return Item{}, errors.Join(err, restore)
			}
		}
		return Item{}, err
	}
	if moved {
		os.RemoveAll(backup)
	}
	if i := s.index(id); i >= 0 {
		s.installed[i] = p
	} else {
		s.installed = append(s.installed, p)
	}
	s.changed(slices.Clone(s.installed))
	s.dropPreview(token)
	return Item{Metadata: p.Metadata, InstalledAt: p.installedAt}, nil
}

func resetSource(ctx context.Context, tx *sql.Tx, id string) error {
	_, err := tx.ExecContext(ctx, `WITH cleared AS (DELETE FROM curation_source_keys WHERE source=$1) DELETE FROM curation_source_settings WHERE source=$1`, "plugin:"+id)
	return err
}

func (s *Store) List(ctx context.Context) ([]Item, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.QueryContext(ctx, `SELECT p.id,COALESCE(c.enabled,false),p.disabled_reason FROM source_plugins p LEFT JOIN curation_source_settings c ON c.source='plugin:'||p.id ORDER BY installed_at,p.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Item, 0, len(s.installed))
	for rows.Next() {
		var item Item
		var id string
		if err = rows.Scan(&id, &item.Enabled, &item.DisabledReason); err != nil {
			return nil, err
		}
		if i := s.index(id); i >= 0 {
			p := s.installed[i]
			item.Metadata = p.Metadata
			item.InstalledAt = p.installedAt
			out = append(out, item)
		}
	}
	return out, rows.Err()
}

func (s *Store) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.index(id)
	if i < 0 {
		return ErrNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM source_plugins WHERE id=$1", id); err != nil {
		return err
	}
	if err = resetSource(ctx, tx, id); err != nil {
		return err
	}
	dir := filepath.Join(s.dir, id)
	backup := filepath.Join(s.dir, ".remove-"+rand.Text())
	err = os.Rename(dir, backup)
	moved := err == nil
	if !moved && !os.IsNotExist(err) {
		return err
	}
	if err = tx.Commit(); err != nil {
		if moved {
			err = errors.Join(err, os.Rename(backup, dir))
		}
		return err
	}
	s.installed = slices.Delete(s.installed, i, i+1)
	s.changed(slices.Clone(s.installed))
	return os.RemoveAll(backup)
}
