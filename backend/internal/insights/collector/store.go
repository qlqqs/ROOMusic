// Package collector 接收匿名心跳，在单个数据文件中保留各实例的最近一次上报。
package collector

import (
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/qlqq/roomusic/backend/internal/insights/schema"
)

type record struct {
	FirstSeen time.Time      `json:"first_seen"`
	LastSeen  time.Time      `json:"last_seen"`
	Payload   schema.Payload `json:"payload"`
}

type Store struct {
	mu        sync.RWMutex
	dir       string
	instances map[string]record
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	s := &Store{dir: dir, instances: make(map[string]record)}
	data, err := os.ReadFile(filepath.Join(dir, "instances.json"))
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(data, &s.instances); err != nil || s.instances == nil {
		return nil, errors.New("统计数据文件无效")
	}
	for id, rec := range s.instances {
		if id != strings.ToLower(rec.Payload.InstanceID) || rec.Payload.Validate() != nil || rec.FirstSeen.IsZero() || rec.LastSeen.Before(rec.FirstSeen) {
			return nil, errors.New("实例记录无效")
		}
	}
	if err := s.Prune(time.Now()); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) put(payload schema.Payload, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now = now.UTC()
	id := strings.ToLower(payload.InstanceID)
	payload.InstanceID = id
	// 已有记录可能来自并发请求，保留最晚的心跳与载荷。
	entry, exists := s.instances[id]
	if exists && now.Before(entry.LastSeen) {
		return nil
	}
	if !exists {
		entry.FirstSeen = now
	}
	entry.LastSeen, entry.Payload = now, payload
	next := maps.Clone(s.instances)
	next[id] = entry
	return s.save(next)
}

func (s *Store) Prune(now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := now.Add(-90 * 24 * time.Hour)
	var next map[string]record
	for id, entry := range s.instances {
		if entry.LastSeen.Before(cutoff) {
			if next == nil {
				next = maps.Clone(s.instances)
			}
			delete(next, id)
		}
	}
	if next == nil {
		return nil
	}
	return s.save(next)
}

// save 在调用方持锁时替换整份文件，落盘成功后才对读取者发布新状态。
func (s *Store) save(next map[string]record) error {
	f, err := os.CreateTemp(s.dir, ".instances-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = json.NewEncoder(f).Encode(next)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), filepath.Join(s.dir, "instances.json")); err != nil {
		return err
	}
	s.instances = next
	return nil
}
