package zones

import (
	"context"
	"database/sql"
	"sort"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
)

// checkIRReference 与纯文档校验分开；关闭卷积仍须核对保留的资源归属。
func (s *Service) checkIRReference(ctx context.Context, tx *sql.Tx, userID string, document SettingsDocument) error {
	settings, err := document.DecodeConvolution()
	if err != nil {
		return err
	}
	if settings.IRID == nil {
		return nil
	}
	if s.options.IRReferences == nil {
		return ErrDatabaseUnavailable
	}
	return databaseError(s.options.IRReferences.Check(ctx, tx, userID, *settings.IRID))
}

type convolutionRemoval struct {
	zone      *zoneRuntime
	persisted PersistedZone
	tracks    map[string]catalog.TrackSummary
}

// DeleteIR 在同一事务删除本人资源并清理所有播放区引用；提交前不更改运行态。
func (s *Service) DeleteIR(ctx context.Context, userID, irID string) error {
	if !validID(irID) {
		return ErrInvalidInput
	}
	userID, irID = strings.ToLower(userID), strings.ToLower(irID)
	u, err := s.user(ctx, userID)
	if err != nil {
		return err
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if s.options.IRReferences == nil {
		return ErrDatabaseUnavailable
	}

	// 用户写锁排除设置命令和注册；读取时短暂取区锁，不与 dispatch 嵌套。
	// 渲染回调可以更新位置，但不会改变卷积引用，因此候选集保持稳定。
	ids := make([]string, 0, len(u.zones))
	for id, z := range u.zones {
		z.mu.Lock()
		settings, decodeErr := z.persisted.Settings.Convolution.DecodeConvolution()
		z.mu.Unlock()
		if decodeErr != nil {
			return decodeErr
		}
		if settings.IRID != nil && *settings.IRID == irID {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		u.zones[id].dispatch.Lock()
	}
	defer func() {
		for i := len(ids) - 1; i >= 0; i-- {
			u.zones[ids[i]].dispatch.Unlock()
		}
	}()
	for _, id := range ids {
		u.zones[id].mu.Lock()
	}
	locked := true
	unlockZones := func() {
		if locked {
			for i := len(ids) - 1; i >= 0; i-- {
				u.zones[ids[i]].mu.Unlock()
			}
			locked = false
		}
	}
	defer unlockZones()

	now := s.Now()
	removals := make([]convolutionRemoval, 0, len(ids))
	for _, id := range ids {
		z := u.zones[id]
		settings, err := z.persisted.Settings.Convolution.DecodeConvolution()
		if err != nil {
			return err
		}
		settings.Enabled, settings.IRID = false, nil
		document, err := NewConvolutionDocument(settings)
		if err != nil {
			return err
		}
		tracks, err := s.loadSummaries(ctx, z.persisted.Queue.Entries)
		if err != nil {
			return err
		}
		p := z.persisted
		p.Settings.Convolution = document
		p.Revision++
		p.PositionSeconds = positionAt(z, now, tracks)
		p.PositionSavedAt = now
		removals = append(removals, convolutionRemoval{zone: z, persisted: p, tracks: tracks})
	}
	if err := s.transact(ctx, func(store *Store) error {
		if err := s.options.IRReferences.Check(ctx, store.tx, userID, irID); err != nil {
			return err
		}
		if err := s.options.IRReferences.Delete(ctx, store.tx, userID, irID); err != nil {
			return err
		}
		for _, removal := range removals {
			if err := store.UpdateZone(ctx, userID, removal.persisted); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}

	// 仍持有用户及区锁：新命令或渲染回调不能在缓存失效前创建新会话。
	for _, removal := range removals {
		removal.zone.persisted = removal.persisted
		removal.zone.positionWrittenAt = now
		removal.zone.cursorTracks = removal.tracks
	}
	s.options.IRReferences.Invalidate(userID, irID)
	updates := make([]Zone, 0, len(removals))
	for _, removal := range removals {
		z := removal.zone
		s.cancelTimers(z)
		s.closeSession(z)
		z.signal = nil
		if z.intent == "play" && z.persisted.Queue.CurrentEntryID != "" {
			s.scheduleIRDeletionRebuild(userID, z)
		}
		updates = append(updates, s.zoneOf(z, removal.tracks))
	}
	unlockZones()
	for _, update := range updates {
		s.publishZone(userID, update)
	}
	return nil
}

// scheduleIRDeletionRebuild 仅用于删除 IR 后已主动关闭旧会话的播放区。
// 暂停、离线、清空或会话代次改变后不再自动开流，也不影响普通重建资格。
func (s *Service) scheduleIRDeletionRebuild(userID string, z *zoneRuntime) {
	generation := z.generation
	s.scheduleRebuildWhile(userID, z, 300*time.Millisecond, func() bool {
		return z.generation == generation && z.session == nil && z.online &&
			z.intent == "play" && z.persisted.Queue.CurrentEntryID != ""
	})
}
