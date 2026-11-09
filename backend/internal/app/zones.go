package app

import (
	"context"

	"github.com/qlqq/roomusic/backend/internal/listening"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

// zonePlayRecorder 在装配层转换返回值，计次策略与访问幂等键仍由播放区拥有。
type zonePlayRecorder struct {
	service *listening.Service
}

func (r zonePlayRecorder) Record(ctx context.Context, userID, idempotencyKey, trackID string, playedSeconds float64) error {
	_, err := r.service.Record(ctx, userID, idempotencyKey, trackID, playedSeconds)
	return err
}

var _ zones.PlayRecorder = zonePlayRecorder{}
