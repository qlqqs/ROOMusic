package schema

import (
	"strings"
	"testing"
)

func TestInsightsBucketBoundaries(t *testing.T) {
	type boundary struct {
		count int
		want  string
	}
	for _, group := range []struct {
		name   string
		bucket func(int) string
		cases  []boundary
	}{
		{"library", LibraryBucket, []boundary{{0, "0"}, {1, "1-999"}, {999, "1-999"}, {1000, "1k-10k"}, {9999, "1k-10k"}, {10000, "10k-50k"}, {49999, "10k-50k"}, {50000, "50k+"}, {200000, "50k+"}}},
		{"members", MembersBucket, []boundary{{0, ""}, {1, "1"}, {2, "2-5"}, {5, "2-5"}, {6, "6-20"}, {20, "6-20"}, {21, "21+"}, {200000, "21+"}}},
		{"devices_and_plugins", DeviceBucket, []boundary{{0, "0"}, {1, "1"}, {2, "2-5"}, {5, "2-5"}, {6, "6+"}, {200000, "6+"}}},
	} {
		t.Run(group.name, func(t *testing.T) {
			for _, tc := range group.cases {
				if got := group.bucket(tc.count); got != tc.want {
					t.Errorf("计数 %d 分到 %q，期望 %q", tc.count, got, tc.want)
				}
			}
		})
	}
}

func TestInsightsStrictDecode(t *testing.T) {
	const valid = `{"schema":1,"instance_id":"a1000000-0000-4000-8000-000000000001","version":"1.2.3","os":"linux","arch":"arm64","container":false,"library":{"tracks":"0","releases":"1-999"},"members":"2-5","features":{"auto_curation":true,"steward_model":false,"mcp_token":false,"lan_devices":false,"dsp":true,"scheduled_scan":false,"plugins":"2-5"},"devices_30d":{"browser":"1","upnp":"0"}}`
	want := Payload{
		Schema: 1, InstanceID: "a1000000-0000-4000-8000-000000000001", Version: "1.2.3", OS: "linux", Arch: "arm64",
		Library: Library{Tracks: "0", Releases: "1-999"}, Members: "2-5",
		Features: Features{AutoCuration: true, DSP: true, Plugins: "2-5"}, Devices30d: Devices{Browser: "1", UPnP: "0"},
	}
	if got, err := Decode(strings.NewReader(valid + strings.Repeat(" ", 4096-len(valid)))); err != nil || got != want {
		t.Fatalf("4096 字节心跳解码 = %+v，错误 %v", got, err)
	}
	maxVersion := "1.2.3-" + strings.Repeat("a", 58)
	if got, err := Decode(strings.NewReader(strings.Replace(valid, want.Version, maxVersion, 1))); err != nil || got.Version != maxVersion {
		t.Fatalf("64 字节版本解码 = %q，错误 %v", got.Version, err)
	}
	for _, tc := range []struct {
		name, old, replacement string
	}{
		{"unknown_root", `"schema":1`, `"schema":1,"username":"synthetic"`},
		{"unknown_nested", `"upnp":"0"`, `"upnp":"0","cast":"0"`},
		{"duplicate_key", `"tracks":"0"`, `"tracks":"0","tracks":"1-999"`},
		{"wrong_key_case", `"os":`, `"OS":`},
		{"missing", `"container":false,`, ""},
		{"null_bool", `"container":false`, `"container":null`},
		{"null_object", `{"tracks":"0","releases":"1-999"}`, "null"},
		{"wrong_type", `"browser":"1"`, `"browser":1`},
		{"schema_version", `"schema":1`, `"schema":2`},
		{"not_uuid", want.InstanceID, "a1000000-0000-4000-8000-00000000000z"},
		{"version_format", want.Version, "arbitrary-text"},
		{"version_length", want.Version, "1.2.3-" + strings.Repeat("a", 59)},
		{"os_enum", `"linux"`, `"windows"`},
		{"arch_enum", `"arm64"`, `"386"`},
		{"tracks_enum", `"tracks":"0"`, `"tracks":"1"`},
		{"releases_enum", `"releases":"1-999"`, `"releases":"1-1000"`},
		{"members_enum", `"members":"2-5"`, `"members":"0"`},
		{"plugins_enum", `"plugins":"2-5"`, `"plugins":"6-20"`},
		{"browser_enum", `"browser":"1"`, `"browser":"21+"`},
		{"upnp_enum", `"upnp":"0"`, `"upnp":"50k+"`},
		{"trailing_object", valid, valid + `{}`},
		{"over_4KiB", valid, valid + strings.Repeat(" ", 4097-len(valid))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.Replace(valid, tc.old, tc.replacement, 1)
			if body == valid {
				t.Fatal("测试没有改变输入")
			}
			if _, err := Decode(strings.NewReader(body)); err != ErrInvalid {
				t.Fatalf("解码错误 = %v，期望拒绝心跳", err)
			}
		})
	}
}
