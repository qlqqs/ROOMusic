package dspsettings

import (
	"context"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/dspsettings/irload"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type irCacheKey struct {
	user, id, digest, layout string
	rate, channels           int
}
type irCacheEntry struct {
	key    irCacheKey
	loaded LoadedIR
}

func (s *Service) InvalidateIR(user, id string) {
	user, id = strings.ToLower(user), strings.ToLower(id)
	s.irMu.Lock()
	defer s.irMu.Unlock()
	s.irEpoch++
	kept := s.irCache[:0]
	for _, entry := range s.irCache {
		if entry.key.user != user || entry.key.id != id {
			kept = append(kept, entry)
		}
	}
	clear(s.irCache[len(kept):])
	s.irCache = kept
}

// LoadIR 返回只读配置；每个渲染会话自行创建独占的卷积器状态。
func (s *Service) LoadIR(ctx context.Context, user, id string, rate, channels int, layout string) (LoadedIR, error) {
	if !uuid.Valid(user) || !uuid.Valid(id) || rate <= 0 || channels < 1 || channels > 8 {
		return LoadedIR{}, ErrInvalidInput
	}
	if s.irLoader == nil {
		return LoadedIR{}, irload.ErrDecodeFailed
	}
	user, id = strings.ToLower(user), strings.ToLower(id)
	ctx, cancel := context.WithTimeout(ctx, irload.ProcessingTimeout)
	defer cancel()
	store := irStore{s.database}
	metadata, digest, err := store.get(ctx, user, id)
	if err != nil {
		return LoadedIR{}, err
	}
	key := irCacheKey{user: user, id: id, digest: string(digest), rate: rate, channels: channels, layout: layout}
	s.irMu.Lock()
	for i, entry := range s.irCache {
		if entry.key == key {
			copy(s.irCache[1:i+1], s.irCache[:i])
			s.irCache[0] = entry
			s.irMu.Unlock()
			result := entry.loaded
			result.Name = metadata.Name
			return result, nil
		}
	}
	s.irMu.Unlock()
	data, err := store.content(ctx, user, id)
	if err != nil {
		return LoadedIR{}, err
	}
	loaded, err := s.irLoader.Load(ctx, metadata.FileName, data, rate, channels)
	if err != nil {
		return LoadedIR{}, err
	}
	result := LoadedIR{Name: metadata.Name, ResampledFrom: loaded.ResampledFrom, Config: loaded.Config}
	// 解码期间可能删除或改名；短查询重核验并用失效代次避免重新填回旧缓存。
	for {
		if err := ctx.Err(); err != nil {
			return LoadedIR{}, err
		}
		s.irMu.Lock()
		epoch := s.irEpoch
		s.irMu.Unlock()
		metadata, _, err = store.get(ctx, user, id)
		if err != nil {
			return LoadedIR{}, err
		}
		result.Name = metadata.Name
		s.irMu.Lock()
		if epoch != s.irEpoch {
			s.irMu.Unlock()
			continue
		}
		for i := 0; i < len(s.irCache); i++ {
			if s.irCache[i].key == key {
				s.irCache = append(s.irCache[:i], s.irCache[i+1:]...)
				break
			}
		}
		s.irCache = append([]irCacheEntry{{key: key, loaded: result}}, s.irCache...)
		if len(s.irCache) > 4 {
			clear(s.irCache[4:])
			s.irCache = s.irCache[:4]
		}
		s.irMu.Unlock()
		return result, nil
	}
}
