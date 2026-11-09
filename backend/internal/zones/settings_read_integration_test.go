package zones

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestPostgreSQLZonesInvalidSettingsFallback(t *testing.T) {
	for _, tc := range []struct {
		key string
		raw string
	}{
		{"headroom", `{"enabled":true,"db":"private-json-sentinel","clip_indicator":true}`},
		{"sample_rate", `{"strategy":"custom","custom_rate":"private-json-sentinel","filter":"linear_precise"}`},
	} {
		t.Run(tc.key, func(t *testing.T) {
			f := newZonesFixture(t)
			user := f.user(t)
			zoneID, _ := f.zone(t, user, time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC))
			// jsonb 已保证语法合法；用非法类型携带哨兵，验证日志不泄露存储内容。
			raw := tc.raw
			f.exec(t, `UPDATE playback_zones SET `+tc.key+`=$2::jsonb,leveling='track',gapless=false,crossfade=true,crossfade_seconds=8,quality='lossy' WHERE id=$1::uuid`, zoneID, raw)
			var logs bytes.Buffer
			state, err := loadUser(context.Background(), f.database, user, slog.New(slog.NewJSONHandler(&logs, nil)))
			if err != nil {
				t.Fatal(err)
			}
			if len(state.Zones) != 1 {
				t.Fatalf("播放区数量=%d", len(state.Zones))
			}
			want := PlaybackSettings{Leveling: "track", Gapless: false, Crossfade: true, CrossfadeSeconds: 8, Quality: "lossy", Headroom: defaultHeadroom(), SampleRate: DefaultSampleRateDocument(), Downmix: DefaultDownmixDocument(), PEQ: DefaultPEQDocument(), Convolution: DefaultConvolutionDocument()}
			if !EqualSettings(state.Zones[0].Settings, want) {
				t.Fatalf("坏键影响其他设置：%+v", state.Zones[0].Settings)
			}
			lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
			if len(lines) != 1 {
				t.Fatalf("WARN 数量=%d", len(lines))
			}
			var event map[string]any
			if err := json.Unmarshal([]byte(lines[0]), &event); err != nil {
				t.Fatal(err)
			}
			if event["level"] != "WARN" || event["event"] != "zones.settings.invalid" || event["zone_id"] != zoneID || event["key"] != tc.key || event["reason"] != "invalid_document" {
				t.Fatalf("降级日志=%+v", event)
			}
			if strings.Contains(logs.String(), "private-json-sentinel") || strings.Contains(logs.String(), raw) {
				t.Fatal("日志泄露原始 JSON")
			}
			var unchanged bool
			if err := f.database.QueryRow(`SELECT `+tc.key+`=$2::jsonb FROM playback_zones WHERE id=$1::uuid`, zoneID, raw).Scan(&unchanged); err != nil {
				t.Fatal(err)
			}
			if !unchanged {
				t.Fatal("读取默认回退不应写库")
			}
		})
	}
}
