package zones

import (
	"context"
	"time"
)

// DeviceUsage 以最后在线时间计算近期输出端。多个成员使用同一 UPnP 设备只计一次。
func (s *Service) DeviceUsage(ctx context.Context, since time.Time) (browser, upnp int, dsp bool, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT count(*) FILTER (WHERE kind='browser'),
        count(DISTINCT external_id) FILTER (WHERE kind='upnp'),
        EXISTS (SELECT 1 FROM playback_zones WHERE peq @> '{"enabled":true}' OR convolution @> '{"enabled":true}')
        FROM playback_devices WHERE last_seen_at >= $1`, since).Scan(&browser, &upnp, &dsp)
	return
}
