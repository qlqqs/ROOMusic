package config

import "testing"

func TestLoadScanWorkers(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		want        int
		invalid     bool
	}{
		{"default", "", 4, false}, {"serial", "1", 1, false}, {"maximum", "16", 16, false},
		{"zero", "0", 0, true}, {"too_many", "17", 0, true}, {"not_integer", "abc", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setRequired(t)
			t.Setenv("ROOMUSIC_SCAN_WORKERS", tc.value)
			settings, err := Load()
			if tc.invalid {
				if err == nil || err.Error() != "ROOMUSIC_SCAN_WORKERS must be an integer between 1 and 16" {
					t.Fatalf("Load=%v", err)
				}
				return
			}
			if err != nil || settings.ScanWorkers != tc.want {
				t.Fatalf("ScanWorkers=%d err=%v，期望 %d", settings.ScanWorkers, err, tc.want)
			}
		})
	}
}
