package backup

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
)

const stateName = ".roomusic-backup-state.json"

type diskState struct {
	Version     int      `json:"version"`
	LastFailure *Failure `json:"last_failure"`
}

func (s *Service) readState() {
	file, err := os.Open(filepath.Join(s.settings.Directory, stateName))
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		s.logger.Warn("无法读取备份状态", "event", "backup.state_unreadable")
		return
	}
	defer file.Close()
	var state diskState
	decoder := json.NewDecoder(io.LimitReader(file, 64*1024))
	err = decoder.Decode(&state)
	var extra any
	if err != nil || state.Version != 1 || decoder.Decode(&extra) != io.EOF || !validFailure(state.LastFailure) {
		s.logger.Warn("无法读取备份状态", "event", "backup.state_unreadable")
		return
	}
	s.lastFailure = state.LastFailure
}
func validFailure(f *Failure) bool {
	if f == nil {
		return true
	}
	if f.At.IsZero() || !validSource(f.Source) {
		return false
	}
	switch f.Stage {
	case "directory", "prepare", "pg_dump", "verify", "publish":
		return true
	}
	return false
}
func validSource(source Source) bool {
	switch source {
	case SourceScheduled, SourceManual, SourcePreMigration, SourcePreRestore:
		return true
	}
	return false
}
func (s *Service) writeState() {
	s.mu.Lock()
	state := diskState{Version: 1, LastFailure: s.lastFailure}
	s.mu.Unlock()
	file, err := os.CreateTemp(s.settings.Directory, ".roomusic-state.partial.*")
	if err != nil {
		return
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if json.NewEncoder(file).Encode(state) != nil || file.Sync() != nil {
		return
	}
	if file.Close() != nil {
		return
	}
	if os.Rename(file.Name(), filepath.Join(s.settings.Directory, stateName)) != nil {
		return
	}
	_ = syncDirectory(s.settings.Directory)
}
