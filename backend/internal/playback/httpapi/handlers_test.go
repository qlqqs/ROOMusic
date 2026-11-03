package httpapi

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/playback"
)

func TestLogAbortedSuppressesShutdownWhenClientDisconnectedFirst(t *testing.T) {
	var logs bytes.Buffer
	handlers := &Handlers{logger: slog.New(slog.NewJSONHandler(&logs, nil))}
	requestContext, disconnect := context.WithCancel(context.Background())
	defer disconnect()
	lifecycle, shutdown := context.WithCancel(context.Background())
	defer shutdown()
	request := httptest.NewRequest(http.MethodGet, "/audio", nil).WithContext(requestContext)
	reader := playback.NewLifecycleReader(bytes.NewReader([]byte("audio")), lifecycle)

	disconnect()
	shutdown()
	if n, err := reader.Read(make([]byte, 1)); n != 0 || err == nil || !reader.Aborted() {
		t.Fatalf("shutdown did not abort reading: n=%d, err=%v", n, err)
	}
	handlers.logAborted(request, "track-id")
	if logs.Len() != 0 {
		t.Fatalf("client disconnect was logged as shutdown: %s", logs.String())
	}
}
