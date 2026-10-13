package operations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestFingerprintIgnoresMapOrderAndSeparatesContent(t *testing.T) {
	a := Fingerprint(map[string]any{"type": TypeReleaseCorrection, "target": "x", "revision": 1})
	b := Fingerprint(map[string]any{"revision": 1, "target": "x", "type": TypeReleaseCorrection})
	if a != b || len(a) != 64 {
		t.Fatalf("fingerprints of the same request differ or are not SHA-256 hex: %q vs %q", a, b)
	}
	for name, other := range map[string]map[string]any{
		"another type":     {"type": TypeRevert, "target": "x", "revision": 1},
		"another target":   {"type": TypeReleaseCorrection, "target": "y", "revision": 1},
		"another revision": {"type": TypeReleaseCorrection, "target": "x", "revision": 2},
	} {
		if Fingerprint(other) == a {
			t.Errorf("%s produced the same fingerprint", name)
		}
	}
}

func TestProblemMapsFailuresToStableResponses(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		err     error
		status  int
		code    string
		message string
	}{
		{"invalid", Invalid("invalid_correction", "值无效"), 400, "invalid_correction", "值无效"},
		{"not found", NotFound("not_found", "不存在"), 404, "not_found", "不存在"},
		{"conflict", Conflict("revision_conflict", "已变化"), 409, "revision_conflict", "已变化"},
		{"wrapped failure", fmt.Errorf("context: %w", Conflict("same_group", "已在这张专辑里")), 409, "same_group", "已在这张专辑里"},
		{"unavailable", fmt.Errorf("%w: begin: boom", ErrUnavailable), 503, "database_unavailable", "服务暂不可用"},
		{"operation failed", fmt.Errorf("%w: insert: SQL internals", ErrOperationFailed), 500, "operation_failed", "操作未能完成"},
		{"unknown", errors.New("anything else"), 500, "operation_failed", "操作未能完成"},
	} {
		status, code, message := Problem(testCase.err)
		if status != testCase.status || code != testCase.code || message != testCase.message {
			t.Errorf("%s: Problem = (%d, %q, %q), want (%d, %q, %q)", testCase.name, status, code, message, testCase.status, testCase.code, testCase.message)
		}
	}
}

func TestClassifySeparatesConnectionFailuresFromServerFailures(t *testing.T) {
	failure := Conflict("revision_conflict", "已变化")
	if got := classify("execute", failure); got != error(failure) {
		t.Fatalf("a classified failure must pass through unchanged, got %v", got)
	}
	for name, err := range map[string]error{
		"canceled":          context.Canceled,
		"deadline":          context.DeadlineExceeded,
		"network":           &net.OpError{Op: "read", Err: errors.New("connection reset")},
		"end of stream":     io.ErrUnexpectedEOF,
		"connection done":   sql.ErrConnDone,
		"connection class":  &pgconn.PgError{Code: "08006"},
		"too many clients":  &pgconn.PgError{Code: "53300"},
		"admin shutdown":    &pgconn.PgError{Code: "57P01"},
		"wrapped shutdown":  fmt.Errorf("commit: %w", &pgconn.PgError{Code: "57P01"}),
		"wrapped cancelled": fmt.Errorf("insert: %w", context.Canceled),
	} {
		if got := classify("insert", err); !errors.Is(got, ErrUnavailable) {
			t.Errorf("%s: classify = %v, want ErrUnavailable", name, got)
		}
	}
	for name, err := range map[string]error{
		"trigger":         &pgconn.PgError{Code: "P0001", Message: "rejected"},
		"check violation": &pgconn.PgError{Code: "23514"},
		"foreign key":     &pgconn.PgError{Code: "23503"},
		"deadlock":        &pgconn.PgError{Code: "40P01"},
		"program error":   errors.New("encode state"),
		"transaction":     sql.ErrTxDone,
	} {
		if got := classify("insert", err); !errors.Is(got, ErrOperationFailed) || errors.Is(got, ErrUnavailable) {
			t.Errorf("%s: classify = %v, want ErrOperationFailed", name, got)
		}
	}
}
