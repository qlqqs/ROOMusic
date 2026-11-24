package insights

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/qlqq/roomusic/backend/internal/insights/schema"
)

// nextAttempt 用随机实例 ID 和尝试时刻确定偏移，重启或设置变更时仍是同一个截止时刻。
func nextAttempt(state Status) time.Time {
	if state.LastAttemptAt == nil {
		return state.FirstSendAfter
	}
	digest := sha256.Sum256([]byte(state.InstanceID + state.LastAttemptAt.UTC().Format(time.RFC3339Nano)))
	jitter := time.Duration(binary.BigEndian.Uint64(digest[:8]) % uint64(time.Hour))
	return state.LastAttemptAt.Add(24*time.Hour + jitter)
}

func (s *Service) Run(ctx context.Context) {
	if s.options.ForcedOff || (s.options.Version == "" && !s.options.ExplicitURL) {
		return
	}
	defer s.client.CloseIdleConnections()
	for ctx.Err() == nil {
		s.mu.Lock()
		attempt, cancel := context.WithCancel(ctx)
		s.cancel = cancel
		s.mu.Unlock()
		delay := s.cycle(attempt, ctx)
		cancel()
		s.mu.Lock()
		s.cancel = nil
		s.mu.Unlock()
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-s.wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}

func (s *Service) cycle(ctx, lifecycle context.Context) time.Duration {
	const poll = 5 * time.Minute
	state, err := s.state(ctx)
	if err != nil || !state.Enabled {
		return poll
	}
	if remaining := time.Until(nextAttempt(state)); remaining > 0 {
		return remaining
	}
	members, admins, err := s.inputs.Accounts.EnabledAccountCounts(ctx)
	if err != nil || admins == 0 {
		return poll
	}
	payload, err := s.payload(ctx, state.InstanceID, members)
	if err != nil {
		return poll
	}

	// 先登记本轮尝试，崩溃、同时启动或关闭开关都不会触发补发。
	var attempt time.Time
	err = s.db.QueryRowContext(ctx, `UPDATE instance_insights SET last_attempt_at=clock_timestamp(),last_error=NULL
        WHERE id AND enabled AND last_attempt_at IS NOT DISTINCT FROM $1 RETURNING last_attempt_at`, state.LastAttemptAt).Scan(&attempt)
	if err != nil {
		return poll
	}
	code := s.send(ctx, payload)
	_, err = s.db.ExecContext(lifecycle, `UPDATE instance_insights SET last_error=NULLIF($1,''),
        last_success_at=CASE WHEN $1='' THEN clock_timestamp() ELSE last_success_at END
        WHERE id AND last_attempt_at=$2`, code, attempt)
	if err != nil {
		return poll
	}
	state.LastAttemptAt = &attempt
	return time.Until(nextAttempt(state))
}

func (s *Service) send(ctx context.Context, payload json.RawMessage) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint.String(), bytes.NewReader(payload))
	if err != nil {
		return "network"
	}
	version := s.options.Version
	if version == "" {
		version = schema.DevVersion
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "ROOMusic/"+version)
	resp, err := s.client.Do(req)
	if resp != nil {
		defer resp.Body.Close()
	}
	if resp != nil && (resp.StatusCode < 200 || resp.StatusCode >= 300) {
		return "http_" + strconv.Itoa(resp.StatusCode)
	}
	if err != nil {
		return "network"
	}
	if resp.StatusCode != http.StatusNoContent {
		return "invalid_response"
	}
	return ""
}
