package library

import (
	"strings"
	"testing"
)

func TestScanDiagnosticProjectionDoesNotEchoStoredHostPaths(t *testing.T) {
	message := safeDiagnosticMessage("unsupported_cue", "open /srv/private/album.cue: permission denied")
	if strings.Contains(message, "/srv/private") {
		t.Fatalf("safe diagnostic message leaked a host path: %q", message)
	}
	if safeDiagnosticKind("../../private") != "unknown" {
		t.Fatal("unsafe diagnostic kind was exposed")
	}
}
