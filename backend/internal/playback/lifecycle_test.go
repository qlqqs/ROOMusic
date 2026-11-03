package playback

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

func TestLifecycleReaderReadsAndSeeks(t *testing.T) {
	reader := NewLifecycleReader(bytes.NewReader([]byte("0123456789")), context.Background())
	if end, err := reader.Seek(0, io.SeekEnd); err != nil || end != 10 {
		t.Fatalf("Seek to end = %d, %v", end, err)
	}
	if position, err := reader.Seek(4, io.SeekStart); err != nil || position != 4 {
		t.Fatalf("Seek to 4 = %d, %v", position, err)
	}
	got, err := io.ReadAll(reader)
	if err != nil || string(got) != "456789" || reader.Aborted() {
		t.Fatalf("ReadAll = %q, %v; aborted=%v", got, err, reader.Aborted())
	}
}

func TestLifecycleReaderAbortsAndStaysAbortedOnShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	file := bytes.NewReader([]byte("0123456789"))
	reader := NewLifecycleReader(file, ctx)
	buffer := make([]byte, 2)
	if n, err := reader.Read(buffer); n != 2 || err != nil || string(buffer) != "01" {
		t.Fatalf("read before shutdown = %d, %v, %q", n, err, buffer)
	}
	cancel()
	for range 2 {
		if n, err := reader.Read(buffer); n != 0 || !errors.Is(err, context.Canceled) || !reader.Aborted() {
			t.Fatalf("read after shutdown = %d, %v; aborted=%v", n, err, reader.Aborted())
		}
	}
	if position, err := file.Seek(0, io.SeekCurrent); position != 2 || err != nil {
		t.Fatalf("underlying position = %d, %v; canceled reads must not touch the file", position, err)
	}
}

type blockedAudioReader struct {
	*bytes.Reader
	entered chan struct{}
	release chan struct{}
	err     error
}

func (file *blockedAudioReader) Read(buffer []byte) (int, error) {
	close(file.entered)
	<-file.release
	n, _ := file.Reader.Read(buffer)
	return n, file.err
}

func TestLifecycleReaderDiscardsBytesWhenShutdownOccursDuringRead(t *testing.T) {
	for _, testCase := range []struct {
		name string
		err  error
	}{
		{"success", nil}, {"EOF", io.EOF}, {"read failure", io.ErrUnexpectedEOF},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			file := &blockedAudioReader{Reader: bytes.NewReader([]byte("audio")), entered: make(chan struct{}), release: make(chan struct{}), err: testCase.err}
			reader := NewLifecycleReader(file, ctx)
			type result struct {
				n   int
				err error
			}
			done := make(chan result, 1)
			go func() {
				n, err := reader.Read(make([]byte, 16))
				done <- result{n, err}
			}()
			select {
			case <-file.entered:
			case <-time.After(5 * time.Second):
				close(file.release)
				t.Fatal("underlying read never started")
			}
			cancel()
			close(file.release)
			select {
			case got := <-done:
				if got.n != 0 || !errors.Is(got.err, context.Canceled) || !reader.Aborted() {
					t.Fatalf("read canceled while blocked = %d, %v; aborted=%v", got.n, got.err, reader.Aborted())
				}
			case <-time.After(5 * time.Second):
				t.Fatal("read did not return after the underlying reader was released")
			}
		})
	}
}

func TestLifecycleReaderPreservesReadErrorsWithoutReportingShutdown(t *testing.T) {
	release := make(chan struct{})
	close(release)
	file := &blockedAudioReader{Reader: bytes.NewReader([]byte("audio")), entered: make(chan struct{}), release: release, err: io.ErrUnexpectedEOF}
	reader := NewLifecycleReader(file, context.Background())
	if n, err := reader.Read(make([]byte, 16)); n != 5 || !errors.Is(err, io.ErrUnexpectedEOF) || reader.Aborted() {
		t.Fatalf("read error = %d, %v; aborted=%v", n, err, reader.Aborted())
	}
}
