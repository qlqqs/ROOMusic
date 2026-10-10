package playback

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/library"
	"github.com/qlqq/roomusic/backend/internal/playback/derivation"
)

func TestClassifyDerivation(t *testing.T) {
	alive := context.Background()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	failed := &derivation.FailedError{Category: derivation.FailureTimeout, Err: context.DeadlineExceeded}
	unexpected := errors.New("unexpected")

	for _, testCase := range []struct {
		name    string
		request context.Context
		err     error
		want    error
	}{
		{"the caller went away", canceled, context.Canceled, context.Canceled},
		{"the caller went away while the job also failed", canceled, failed, context.Canceled},
		// 任务自己的超时把 DeadlineExceeded 作为原因：它是转换失败，不是“等待时间用完”。
		{"a conversion that timed out", alive, failed, ErrDerivationFailed},
		{"a verification mismatch", alive, &derivation.FailedError{Category: derivation.FailureVerifyMismatch}, ErrDerivationFailed},
		{"the limits are full", alive, derivation.ErrBusy, ErrDerivationBusy},
		{"the manager is shutting down", alive, fmt.Errorf("wrapped: %w", derivation.ErrClosed), ErrDerivationBusy},
		{"the wait ran out", alive, context.DeadlineExceeded, ErrPreparing},
		{"the source changed during the conversion", alive, fmt.Errorf("%w: content differs", ErrSourceChanged), ErrSourceChanged},
		{"an unexpected error is passed through", alive, unexpected, unexpected},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := classifyDerivation(testCase.request, testCase.err); !errors.Is(got, testCase.want) {
				t.Fatalf("classifyDerivation = %v, want %v", got, testCase.want)
			}
		})
	}
	if got := classifyDerivation(alive, failed); !errors.Is(got, ErrDerivationFailed) || errors.Is(got, ErrPreparing) {
		t.Fatalf("a conversion timeout was classified as %v", got)
	}
}

func TestDerivedEntityTagValue(t *testing.T) {
	value := derivedEntityTagValue("track-1", "key-1")
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(value) {
		t.Fatalf("value = %q, want 32 lowercase hexadecimal characters", value)
	}
	if again := derivedEntityTagValue("track-1", "key-1"); again != value {
		t.Fatalf("the value is not deterministic: %q, %q", value, again)
	}
	for name, other := range map[string]string{
		"another track": derivedEntityTagValue("track-2", "key-1"),
		"another key":   derivedEntityTagValue("track-1", "key-2"),
		// 字段之间有分隔符：拼接后相同的两组输入不应得到相同的值。
		"shifted fields": derivedEntityTagValue("track-1|key", "-1"),
	} {
		if other == value {
			t.Errorf("%s produced the same value as the original", name)
		}
	}
}

func TestVerifyHandle(t *testing.T) {
	ctx := context.Background()
	fixture := filepath.Join("..", "..", "testdata", "playback", "alac-16-44100.m4a")
	content, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	modified := time.Date(2026, 10, 1, 8, 30, 15, 123456000, time.UTC)
	path := filepath.Join(t.TempDir(), "track.m4a")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write track: %v", err)
	}
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatalf("set modification time: %v", err)
	}
	fingerprint, err := library.ContentFingerprintOf(ctx, ".m4a", mustOpen(t, path), int64(len(content)))
	if err != nil || fingerprint == "" {
		t.Fatalf("fingerprint = %q, %v", fingerprint, err)
	}
	size := int64(len(content))

	record := func(update func(*catalog.TrackSource)) resolved {
		source := catalog.TrackSource{RelativePath: "dir/track.m4a", Size: &size, ModifiedAt: &modified, ContentFingerprint: fingerprint}
		update(&source)
		return resolved{source: source}
	}
	otherSize, otherTime := size+1, modified.Add(time.Second)

	for _, testCase := range []struct {
		name    string
		target  resolved
		content bool
		want    error
	}{
		{"matches (before)", record(func(*catalog.TrackSource) {}), true, nil},
		{"matches (after)", record(func(*catalog.TrackSource) {}), false, nil},
		{"size differs", record(func(source *catalog.TrackSource) { source.Size = &otherSize }), false, ErrSourceChanged},
		{"modification time differs", record(func(source *catalog.TrackSource) { source.ModifiedAt = &otherTime }), false, ErrSourceChanged},
		{"the scan has no size", record(func(source *catalog.TrackSource) { source.Size = nil }), false, ErrSourceChanged},
		{"the scan has no modification time", record(func(source *catalog.TrackSource) { source.ModifiedAt = nil }), true, ErrSourceChanged},
		{"content differs (before)", record(func(source *catalog.TrackSource) { source.ContentFingerprint = "m4a-mdat-v1:other" }), true, ErrSourceChanged},
		// 转换之后只核对大小与修改时间：指纹不参与，所以这里不会因为指纹不同而失败。
		{"content is not compared after the conversion", record(func(source *catalog.TrackSource) { source.ContentFingerprint = "m4a-mdat-v1:other" }), false, nil},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			file := mustOpen(t, path)
			defer file.Close()
			err := testCase.target.verifyHandle(ctx, file, testCase.content)
			if testCase.want == nil && err != nil || testCase.want != nil && !errors.Is(err, testCase.want) {
				t.Fatalf("verifyHandle = %v, want %v", err, testCase.want)
			}
		})
	}

	t.Run("a rewrite that keeps size and modification time", func(t *testing.T) {
		rewritten := append([]byte(nil), content...)
		mdat := indexOf(rewritten, "mdat")
		rewritten[mdat+8] ^= 0xff
		copyPath := filepath.Join(t.TempDir(), "track.m4a")
		if err := os.WriteFile(copyPath, rewritten, 0o644); err != nil {
			t.Fatalf("write rewritten track: %v", err)
		}
		if err := os.Chtimes(copyPath, modified, modified); err != nil {
			t.Fatalf("set modification time: %v", err)
		}
		file := mustOpen(t, copyPath)
		defer file.Close()
		target := record(func(*catalog.TrackSource) {})
		if err := target.verifyHandle(ctx, file, false); err != nil {
			t.Fatalf("the cheap check must not see the rewrite: %v", err)
		}
		if err := target.verifyHandle(ctx, file, true); !errors.Is(err, ErrSourceChanged) {
			t.Fatalf("the content check must reject the rewrite: %v", err)
		}
	})
}

func mustOpen(t *testing.T, path string) *os.File {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = file.Close() })
	return file
}

func indexOf(content []byte, needle string) int {
	for index := 0; index+len(needle) <= len(content); index++ {
		if string(content[index:index+len(needle)]) == needle {
			return index
		}
	}
	return -1
}
