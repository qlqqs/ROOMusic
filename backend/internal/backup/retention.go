package backup

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

const retentionName = ".roomusic-backup-retention.json"

// Retention 的两个上限独立生效，nil 表示不限。
type Retention struct {
	KeepDays  *int `json:"keep_days"`
	KeepCount *int `json:"keep_count"`
}

type diskRetention struct {
	Version int `json:"version"`
	Retention
	SavedAt time.Time `json:"saved_at"`
}

func validRetention(rule Retention) bool {
	return (rule.KeepDays == nil || (*rule.KeepDays >= 1 && *rule.KeepDays <= 3650)) &&
		(rule.KeepCount == nil || (*rule.KeepCount >= 1 && *rule.KeepCount <= 1000))
}

func (s *Service) effectiveRetention() (Retention, error) {
	fallback := func() (Retention, error) {
		days := s.settings.KeepDays
		return Retention{KeepDays: &days}, nil
	}
	root, err := os.OpenRoot(s.settings.Directory)
	if errors.Is(err, os.ErrNotExist) {
		return fallback()
	}
	if err != nil {
		return Retention{}, failure("retention", "unreadable")
	}
	defer root.Close()
	file, err := root.OpenFile(retentionName, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		return fallback()
	}
	if err != nil {
		return Retention{}, failure("retention", "unreadable")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return Retention{}, failure("retention", "unreadable")
	}
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || len(data) > 4096 {
		return Retention{}, failure("retention", "unreadable")
	}
	// RawMessage 区分缺键与显式 null；不能把损坏文件误读为“两项不限”。
	var stored struct {
		Version   int             `json:"version"`
		KeepDays  json.RawMessage `json:"keep_days"`
		KeepCount json.RawMessage `json:"keep_count"`
		SavedAt   time.Time       `json:"saved_at"`
	}
	var rule Retention
	if json.Unmarshal(data, &stored) != nil || stored.Version != 1 ||
		json.Unmarshal(stored.KeepDays, &rule.KeepDays) != nil ||
		json.Unmarshal(stored.KeepCount, &rule.KeepCount) != nil || !validRetention(rule) {
		return Retention{}, failure("retention", "unreadable")
	}
	return rule, nil
}

func (s *Service) writeRetention(rule Retention) error {
	if s.settings.CreateDirectory {
		if err := os.MkdirAll(s.settings.Directory, 0700); err != nil {
			return ErrNotWritable
		}
	}
	file, err := os.CreateTemp(s.settings.Directory, retentionName+".tmp-*")
	if err != nil {
		return ErrNotWritable
	}
	defer os.Remove(file.Name())
	defer file.Close()
	stored := diskRetention{Version: 1, Retention: rule, SavedAt: s.now().UTC()}
	if json.NewEncoder(file).Encode(stored) != nil || file.Sync() != nil || file.Close() != nil {
		return ErrNotWritable
	}
	if os.Rename(file.Name(), filepath.Join(s.settings.Directory, retentionName)) != nil {
		return ErrNotWritable
	}
	if syncDirectory(s.settings.Directory) != nil {
		return ErrNotWritable
	}
	return nil
}

// retentionPlan 接受 listFiles 的完整有序结果，始终保留第一份，严格早于截止时间才算过期。
func retentionPlan(files []File, rule Retention, now time.Time) []File {
	plan := []File{}
	var cutoff time.Time
	if rule.KeepDays != nil {
		cutoff = now.Add(-time.Duration(*rule.KeepDays) * 24 * time.Hour)
	}
	for i, file := range files {
		if i == 0 {
			continue
		}
		if (rule.KeepDays != nil && file.CreatedAt.Before(cutoff)) || (rule.KeepCount != nil && i >= *rule.KeepCount) {
			plan = append(plan, file)
		}
	}
	return plan
}

// SaveRetention 在同一临界区内预览、持久化与清理；未确认且需要删除时没有任何写入。
func (s *Service) SaveRetention(ctx context.Context, rule Retention, confirm bool, actorID string) (RetentionResult, error) {
	if err := ctx.Err(); err != nil {
		return RetentionResult{}, err
	}
	if !validRetention(rule) {
		return RetentionResult{}, failure("retention", "invalid_input")
	}
	s.retentionMu.Lock()
	defer s.retentionMu.Unlock()
	if err := ctx.Err(); err != nil {
		return RetentionResult{}, err
	}
	files, err := s.listFiles()
	if err != nil {
		return RetentionResult{}, ErrNotWritable
	}
	plan := retentionPlan(files, rule, s.now())
	if len(plan) > 0 && !confirm {
		return RetentionResult{ConfirmationRequired: true, Deletions: len(plan)}, nil
	}
	if err := ctx.Err(); err != nil {
		return RetentionResult{}, err
	}
	if err := s.writeRetention(rule); err != nil {
		return RetentionResult{}, err
	}
	s.logger.Info("已保存备份清理规则", "event", "backup.retention.saved",
		"keep_days", retentionLogValue(rule.KeepDays), "keep_count", retentionLogValue(rule.KeepCount), "actor_id", actorID)
	deleted, failed, err := s.applyRetention(ctx, plan, "saved")
	return RetentionResult{Deleted: deleted, Failed: failed}, err
}

func retentionLogValue(value *int) any {
	if value == nil {
		return "unlimited"
	}
	return *value
}

func (s *Service) retain() {
	s.retentionMu.Lock()
	defer s.retentionMu.Unlock()
	rule, err := s.effectiveRetention()
	if err != nil {
		s.logger.Warn("读不到备份清理规则，这次不清理", "event", "backup.retention_unreadable")
		return
	}
	files, err := s.listFiles()
	if err != nil {
		s.logger.Warn("无法清理过期备份", "event", "backup.retention_failed")
		return
	}
	_, _, _ = s.applyRetention(context.Background(), retentionPlan(files, rule, s.now()), "backup")
}

func (s *Service) applyRetention(ctx context.Context, plan []File, trigger string) (deleted, failed int, err error) {
	if len(plan) == 0 {
		return 0, 0, nil
	}
	root, openErr := os.OpenRoot(s.settings.Directory)
	if openErr == nil {
		defer root.Close()
	}
	defer func() {
		if deleted > 0 {
			_ = syncDirectory(s.settings.Directory)
		}
	}()
	for _, file := range plan {
		if err := ctx.Err(); err != nil {
			return deleted, failed, err
		}
		removeErr := deletionError(openErr)
		if openErr == nil {
			removeErr = deleteIn(root, file.Name)
		}
		switch {
		case errors.Is(removeErr, ErrNotFound):
			// 与手动删除竞争时，已经不存在的文件不算清理失败。
			continue
		case removeErr != nil:
			failed++
			s.logger.Warn("无法清理过期备份", "event", "backup.retention_failed")
		default:
			deleted++
			s.logger.Info("已清理过期备份", "event", "backup.retention.deleted", "file", file.Name, "trigger", trigger)
		}
	}
	return deleted, failed, nil
}
