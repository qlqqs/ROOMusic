package playback

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

// fakeClock 是可手动推进的时钟。
type fakeClock struct{ current time.Time }

func (clock *fakeClock) now() time.Time { return clock.current }

func (clock *fakeClock) advance(duration time.Duration) { clock.current = clock.current.Add(duration) }

type recheckFixture struct {
	reader    *RecheckingReader
	clock     *fakeClock
	checks    int
	checkErr  error
	lifecycle context.CancelFunc
}

func newRecheckFixture(t *testing.T, content string) *recheckFixture {
	t.Helper()
	fixture := &recheckFixture{clock: &fakeClock{current: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)}}
	lifecycle, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	fixture.lifecycle = cancel
	fixture.reader = NewRecheckingReader(bytes.NewReader([]byte(content)), lifecycle, 10*time.Second, fixture.clock.now, func() error {
		fixture.checks++
		return fixture.checkErr
	})
	return fixture
}

func readN(t *testing.T, reader io.Reader, count int) (string, error) {
	t.Helper()
	buffer := make([]byte, count)
	read, err := reader.Read(buffer)
	return string(buffer[:read]), err
}

func TestRecheckingReaderDoesNotRecheckBeforeTheInterval(t *testing.T) {
	fixture := newRecheckFixture(t, "0123456789")
	for _, step := range []time.Duration{0, 3 * time.Second, 6 * time.Second} {
		fixture.clock.advance(step)
		if _, err := readN(t, fixture.reader, 2); err != nil {
			t.Fatalf("read after %v: %v", step, err)
		}
	}
	if fixture.checks != 0 {
		t.Fatalf("checks = %d within 9 seconds of a 10 second interval, want 0", fixture.checks)
	}
	if _, aborted := fixture.reader.Aborted(); aborted {
		t.Fatal("reader reports an abort without a failed check")
	}
}

func TestRecheckingReaderRechecksOncePerIntervalAndKeepsReadingWhileTheCheckPasses(t *testing.T) {
	fixture := newRecheckFixture(t, "0123456789")
	fixture.clock.advance(10 * time.Second)
	if got, err := readN(t, fixture.reader, 3); err != nil || got != "012" {
		t.Fatalf("read after the interval = %q, %v", got, err)
	}
	if fixture.checks != 1 {
		t.Fatalf("checks = %d after one interval, want 1", fixture.checks)
	}
	// 紧接着的读取不再复核：间隔从上次通过的复核重新计算。
	fixture.clock.advance(9 * time.Second)
	if _, err := readN(t, fixture.reader, 3); err != nil || fixture.checks != 1 {
		t.Fatalf("second read: err=%v checks=%d, want a pass without another check", err, fixture.checks)
	}
	fixture.clock.advance(1 * time.Second)
	if got, err := readN(t, fixture.reader, 3); err != nil || got != "678" || fixture.checks != 2 {
		t.Fatalf("third read = %q, %v, checks=%d; want 678 and a second check", got, err, fixture.checks)
	}
}

func TestRecheckingReaderAbortsAndStaysAbortedWhenTheCheckFails(t *testing.T) {
	fixture := newRecheckFixture(t, "0123456789")
	fixture.checkErr = &AbortError{Reason: AbortRootDisabled, Err: ErrRootDisabled}
	fixture.clock.advance(10 * time.Second)
	got, err := readN(t, fixture.reader, 4)
	var abort *AbortError
	if !errors.As(err, &abort) || abort.Reason != AbortRootDisabled || got != "" {
		t.Fatalf("read after a failed check = %q, %v; want no bytes and a root_disabled abort", got, err)
	}
	if !errors.Is(err, ErrRootDisabled) {
		t.Fatalf("the abort does not unwrap to its cause: %v", err)
	}
	if reason, aborted := fixture.reader.Aborted(); !aborted || reason != AbortRootDisabled {
		t.Fatalf("Aborted() = %q, %v", reason, aborted)
	}
	// 失败之后一直失败，并且不再复核：恢复条件也不会让已中止的传输继续。
	fixture.checkErr = nil
	if _, err := readN(t, fixture.reader, 4); !errors.As(err, &abort) || fixture.checks != 1 {
		t.Fatalf("read after the abort: err=%v checks=%d, want the same abort without another check", err, fixture.checks)
	}
}

func TestRecheckingReaderClassifiesUnknownCheckErrorsAsCheckFailed(t *testing.T) {
	fixture := newRecheckFixture(t, "0123456789")
	fixture.checkErr = errors.New("database is down")
	fixture.clock.advance(10 * time.Second)
	if _, err := readN(t, fixture.reader, 4); err == nil {
		t.Fatal("a failing check let the read through")
	}
	if reason, aborted := fixture.reader.Aborted(); !aborted || reason != AbortCheckFailed {
		t.Fatalf("Aborted() = %q, %v; want check_failed", reason, aborted)
	}
}

func TestRecheckingReaderAbortsOnShutdownWithoutWaitingForTheInterval(t *testing.T) {
	fixture := newRecheckFixture(t, "0123456789")
	if _, err := readN(t, fixture.reader, 2); err != nil {
		t.Fatalf("read before shutdown: %v", err)
	}
	fixture.lifecycle()
	got, err := readN(t, fixture.reader, 2)
	var abort *AbortError
	if !errors.As(err, &abort) || abort.Reason != AbortShutdown || got != "" {
		t.Fatalf("read after shutdown = %q, %v; want no bytes and a shutdown abort", got, err)
	}
	if fixture.checks != 0 {
		t.Fatalf("shutdown ran %d checks, want none", fixture.checks)
	}
}

func TestRecheckingReaderSeekPassesThroughWithoutChecking(t *testing.T) {
	fixture := newRecheckFixture(t, "0123456789")
	fixture.clock.advance(time.Hour)
	if end, err := fixture.reader.Seek(0, io.SeekEnd); err != nil || end != 10 {
		t.Fatalf("Seek to end = %d, %v", end, err)
	}
	if position, err := fixture.reader.Seek(4, io.SeekStart); err != nil || position != 4 {
		t.Fatalf("Seek to 4 = %d, %v", position, err)
	}
	if fixture.checks != 0 {
		t.Fatalf("Seek ran %d checks, want none (only reads recheck)", fixture.checks)
	}
	if got, err := readN(t, fixture.reader, 2); err != nil || got != "45" {
		// 时钟已前进一小时，所以这次读取会复核一次并通过。
		t.Fatalf("read after Seek = %q, %v", got, err)
	}
	if fixture.checks != 1 {
		t.Fatalf("checks = %d, want exactly one on the first read after the long pause", fixture.checks)
	}
}

func TestReasonForMapsServiceErrors(t *testing.T) {
	for _, testCase := range []struct {
		err  error
		want AbortReason
	}{
		{ErrTrackNotFound, AbortTrackUnavailable},
		{ErrRootDisabled, AbortRootDisabled},
		{ErrDatabase, AbortCheckFailed},
		{errors.New("anything else"), AbortCheckFailed},
	} {
		if got := ReasonFor(testCase.err); got != testCase.want {
			t.Errorf("ReasonFor(%v) = %q, want %q", testCase.err, got, testCase.want)
		}
	}
}
