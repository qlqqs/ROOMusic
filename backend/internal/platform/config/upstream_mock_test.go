package config

import (
	"strings"
	"testing"
)

func TestLoadUpstreamMockURL(t *testing.T) {
	for _, tc := range []struct {
		name, value, want string
		invalid           bool
	}{
		{name: "unset"},
		{name: "blank", value: " \t "},
		{name: "loopback", value: " http://127.0.0.1:18090/ ", want: "http://127.0.0.1:18090"},
		{name: "compose", value: "http://upstream-mock:18090", want: "http://upstream-mock:18090"},
		{name: "https_prefix", value: "https://Mock.Example.invalid:443/api%20mock/", want: "https://mock.example.invalid/api%20mock"},
		{name: "ipv6", value: "http://[::1]:18090", want: "http://[::1]:18090"},
		{name: "other_scheme", value: "ftp://mock.example.invalid", invalid: true},
		{name: "relative", value: "mock.example.invalid/path", invalid: true},
		{name: "scheme_relative", value: "//mock.example.invalid/path", invalid: true},
		{name: "host_missing", value: "http:///path", invalid: true},
		{name: "opaque", value: "https:mock.example.invalid", invalid: true},
		{name: "credentials", value: "http://test-user:test-token@mock.example.invalid", invalid: true},
		{name: "query", value: "http://mock.example.invalid?token=test-token", invalid: true},
		{name: "fragment", value: "http://mock.example.invalid/#test-fragment", invalid: true},
		{name: "invalid_escape", value: "http://mock.example.invalid/%ZZ", invalid: true},
		{name: "invalid_port", value: "http://mock.example.invalid:65536", invalid: true},
		{name: "zero_port", value: "http://mock.example.invalid:0", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setRequired(t)
			t.Setenv("ROOMUSIC_UPSTREAM_MOCK_URL", tc.value)
			settings, err := Load()
			if tc.invalid {
				if err == nil || !strings.Contains(err.Error(), "ROOMUSIC_UPSTREAM_MOCK_URL") {
					t.Fatalf("Load 错误 = %v，期望拒绝 mock 地址", err)
				}
				if strings.Contains(err.Error(), "test-token") {
					t.Fatal("配置错误泄露了合成密钥")
				}
				return
			}
			if err != nil || settings.UpstreamMockURL != tc.want {
				t.Fatalf("UpstreamMockURL = %q, %v，期望 %q", settings.UpstreamMockURL, err, tc.want)
			}
		})
	}
}
