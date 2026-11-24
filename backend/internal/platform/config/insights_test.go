package config

import (
	"strings"
	"testing"
)

func TestLoadInsightsConfiguration(t *testing.T) {
	setRequired(t)
	t.Setenv("ROOMUSIC_INSIGHTS_DISABLED", "")
	t.Setenv("ROOMUSIC_INSIGHTS_URL", "  ")
	t.Setenv("ROOMUSIC_SOURCE_URL", "")
	defaults, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if defaults.InsightsDisabled || defaults.InsightsURLExplicit ||
		defaults.InsightsURL != "https://insights.roomusic.org/api/v1/heartbeat" ||
		defaults.SourceURL != "https://github.com/qlqqs/ROOMusic" {
		t.Fatalf("匿名统计默认配置不符合同：启用=%t，显式 URL=%t，端点=%q，源代码=%q",
			!defaults.InsightsDisabled, defaults.InsightsURLExplicit, defaults.InsightsURL, defaults.SourceURL)
	}
	t.Setenv("ROOMUSIC_INSIGHTS_DISABLED", " true ")
	t.Setenv("ROOMUSIC_INSIGHTS_URL", " https://collector.example.invalid/heartbeat ")
	t.Setenv("ROOMUSIC_SOURCE_URL", "http://source.example.invalid/fork")
	settings, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !settings.InsightsDisabled || !settings.InsightsURLExplicit ||
		settings.InsightsURL != "https://collector.example.invalid/heartbeat" || settings.SourceURL != "http://source.example.invalid/fork" {
		t.Fatalf("匿名统计自定义配置未生效：关闭=%t，显式 URL=%t，端点=%q，源代码=%q",
			settings.InsightsDisabled, settings.InsightsURLExplicit, settings.InsightsURL, settings.SourceURL)
	}
}

func TestLoadInsightsRejectsInvalidConfiguration(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{"ROOMUSIC_INSIGHTS_DISABLED", "sometimes"},
		{"ROOMUSIC_INSIGHTS_URL", "collector.example.invalid/heartbeat"},
		{"ROOMUSIC_SOURCE_URL", "ftp://source.example.invalid/repo"},
		{"ROOMUSIC_SOURCE_URL", "https://user:test-token@source.example.invalid/repo"},
	} {
		t.Run(tc.key+"/"+tc.value, func(t *testing.T) {
			setRequired(t)
			t.Setenv("ROOMUSIC_INSIGHTS_DISABLED", "")
			t.Setenv("ROOMUSIC_INSIGHTS_URL", "")
			t.Setenv("ROOMUSIC_SOURCE_URL", "")
			t.Setenv(tc.key, tc.value)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), tc.key) || strings.Contains(err.Error(), "test-token") {
				t.Fatalf("配置错误 = %v，期望只标识无效配置项 %s", err, tc.key)
			}
		})
	}
}
