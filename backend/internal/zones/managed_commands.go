package zones

import "context"

func startsOutput(command string) bool {
	switch command {
	case "play", "play_tracks", "play_entry", "next", "previous", "retry", "transfer":
		return true
	}
	return false
}

func (s *Service) prepareManagedCommand(ctx context.Context, userID string, u *userRuntime, zoneID string, c Command) error {
	if s.managedOutputs() == nil {
		return nil
	}
	if err := s.syncManaged(ctx, userID, u); err != nil {
		return err
	}
	u.mu.RLock()
	source := u.zones[zoneID]
	target := source
	if c.Command == "transfer" {
		target = u.zones[c.TargetZoneID]
	}
	u.mu.RUnlock()
	if source == nil || target == nil {
		return ErrNotFound
	}
	source.mu.Lock()
	visible := source.visible()
	source.mu.Unlock()
	if !visible {
		return ErrNotFound
	}
	if startsOutput(c.Command) {
		return s.acquireManaged(ctx, userID, target, c.Takeover)
	}
	return nil
}

// CommandFromOutput 只接受当前托管会话的设备端操作，在同一区锁内验证并提交。
func (s *Service) CommandFromOutput(ctx context.Context, userID, sessionID, command string) (CommandResult, error) {
	if command != "play" && command != "pause" {
		return CommandResult{}, ErrInvalidInput
	}
	out, err := s.findOutput(userID, sessionID)
	if err != nil {
		return CommandResult{}, err
	}
	z := out.zone
	z.dispatch.Lock()
	defer z.dispatch.Unlock()
	z.mu.Lock()
	defer z.mu.Unlock()
	if err := outputError(out); err != nil {
		return CommandResult{}, err
	}
	if z.device.Kind != "upnp" {
		return CommandResult{}, ErrInvalidInput
	}
	before := z.persisted.Revision
	result, err := s.commandLocked(ctx, userID, z, Command{Command: command})
	if err == nil && result.Zone.Revision != before {
		z.event = &ZoneEvent{Kind: command, Seq: result.Zone.Revision, Source: "device"}
		result.Zone.Event = cloneZoneEvent(z.event)
		s.publishZone(userID, result.Zone)
	}
	return result, err
}

// ManagedOutputSession 区分浏览器与进程内的会话权限。会话类别和归属在创建后不可变。
func (s *Service) ManagedOutputSession(userID, sessionID string) bool {
	if sessionID == "" {
		return false
	}
	out, err := s.findOutput(userID, sessionID)
	return err == nil && out.managed
}

// ManagedOutputIdle 在转移后核对占用者与队列，避免释放已重新开始播放的输出端。
func (s *Service) ManagedOutputIdle(userID, sessionID string) bool {
	out, err := s.findOutput(userID, sessionID)
	if err != nil || !out.managed {
		return false
	}
	z := out.zone
	z.mu.Lock()
	defer z.mu.Unlock()
	return outputError(out) == nil && len(z.persisted.Queue.Entries) == 0 && z.intent == "pause"
}
