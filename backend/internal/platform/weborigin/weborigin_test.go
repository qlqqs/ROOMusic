package weborigin

import "testing"

func TestParseOrigins(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
		configuredOnly    bool
	}{
		{"http", "http://192.168.50.206:8080", "http://192.168.50.206:8080", false},
		{"https", "https://music.example.com", "https://music.example.com", false},
		{"case", "HTTPS://Music.Example.COM", "https://music.example.com", false},
		{"http default port", "HTTP://Music.Example.com:80", "http://music.example.com", false},
		{"https default port", "https://music.example.com:443", "https://music.example.com", false},
		{"nondefault port", "https://music.example.com:8443", "https://music.example.com:8443", false},
		{"port lower bound", "http://localhost:1", "http://localhost:1", false},
		{"port upper bound", "http://localhost:65535", "http://localhost:65535", false},
		{"IPv6", "http://[fd00::1]:8090", "http://[fd00::1]:8090", false},
		{"IPv6 default port", "HTTPS://[FD00::AB]:443", "https://[fd00::ab]", false},
		{"punycode", "https://xn--fiqs8s.example", "https://xn--fiqs8s.example", false},
		{"configured slash", "https://Music.Example.com:443/", "https://music.example.com", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for name, parse := range map[string]func(string) (string, error){"configured": ParseConfigured, "request": ParseRequest} {
				t.Run(name, func(t *testing.T) {
					got, err := parse(tc.input)
					if name == "request" && tc.configuredOnly {
						if err == nil {
							t.Fatalf("ParseRequest(%q) = %q，必须拒绝末尾斜杠", tc.input, got)
						}
						return
					}
					if err != nil || got != tc.want {
						t.Fatalf("parse(%q) = %q, %v，期望 %q", tc.input, got, err, tc.want)
					}
				})
			}
		})
	}
}

func TestParseOriginsRejectsInvalidInput(t *testing.T) {
	for _, input := range []string{
		"", "null", "file:///music", "ftp://music.example.com", "music.example.com", "//music.example.com", "http://", "https:///app",
		"https://music.example.com/app", "https://music.example.com//", "https://music.example.com/%2f",
		"https://music.example.com?query=1", "https://music.example.com?", "https://music.example.com#part", "https://music.example.com#",
		"https://user@music.example.com", "https://user:password@music.example.com",
		"http://localhost:0", "http://localhost:65536", "http://localhost:-1", "http://localhost:abc", "http://localhost:",
		"http://fd00::1", "http://fd00::1:8090", "http://[fd00::1", "http://[not-ipv6]", "http://[fd00::1%25eth0]",
		"https://音乐.example", "https://*.example.com", "https://music.*", " https://music.example.com", "https://music.example.com ",
	} {
		t.Run(input, func(t *testing.T) {
			for name, parse := range map[string]func(string) (string, error){"configured": ParseConfigured, "request": ParseRequest} {
				if got, err := parse(input); err == nil {
					t.Errorf("%s(%q) = %q，必须拒绝非法来源", name, input, got)
				}
			}
		})
	}
}

func TestFromHost(t *testing.T) {
	for _, tc := range []struct {
		name, scheme, host, want string
	}{
		{"lowercase host", "https", "Music.Example.COM", "https://music.example.com"},
		{"https default port", "https", "Music.Example.com:443", "https://music.example.com"},
		{"http default port", "http", "Music.Example.com:80", "http://music.example.com"},
		{"http keeps 443", "http", "music.example.com:443", "http://music.example.com:443"},
		{"https keeps 80", "https", "music.example.com:80", "https://music.example.com:80"},
		{"nondefault port", "https", "music.qlqqs.com:8888", "https://music.qlqqs.com:8888"},
		{"LAN address", "http", "192.168.50.206:18090", "http://192.168.50.206:18090"},
		{"minimum port", "http", "h:1", "http://h:1"},
		{"maximum port", "https", "h:65535", "https://h:65535"},
		{"IPv6", "http", "[fd00::1]:8090", "http://[fd00::1]:8090"},
		{"IPv6 case and default port", "https", "[FD00::AB]:443", "https://[fd00::ab]"},
		{"IPv6 text preserved", "http", "[fd00:0:0:0:0:0:0:1]", "http://[fd00:0:0:0:0:0:0:1]"},
		{"trailing dot preserved", "https", "Music.Example.com.", "https://music.example.com."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := FromHost(tc.scheme, tc.host)
			if err != nil || got != tc.want {
				t.Fatalf("FromHost(%q, %q) = %q, %v，期望 %q", tc.scheme, tc.host, got, err, tc.want)
			}
		})
	}
}

func TestFromHostRejectsInvalidInput(t *testing.T) {
	for _, host := range []string{
		"", "a/b", "a?b", "a#b", "u@h", `a\b`, "a b", " a", "a ", "a\tb", "a\nb", "a\rb", "a\x00b", "a\x7fb", "a\u00a0b",
		"a%41", "音乐.example", "*", "*.example.com", "h:", "h:0", "h:65536", "h:x", "h:-1",
		"fd00::1", "fd00::1:8090", "[fd00::1%eth0]", "[fd00::1%25eth0]", "[fd00::1", "[not-ipv6]",
	} {
		for _, scheme := range []string{"http", "https"} {
			t.Run(scheme+"/"+host, func(t *testing.T) {
				if got, err := FromHost(scheme, host); err == nil {
					t.Fatalf("FromHost(%q, %q) = %q，必须拒绝非法 Host", scheme, host, got)
				}
			})
		}
	}
	for _, scheme := range []string{"", "ftp", "file", "ws", "HTTP", "HTTPS", " https"} {
		t.Run("scheme/"+scheme, func(t *testing.T) {
			if got, err := FromHost(scheme, "music.example.com"); err == nil {
				t.Fatalf("FromHost(%q, host) = %q，必须拒绝非法协议", scheme, got)
			}
		})
	}
}
