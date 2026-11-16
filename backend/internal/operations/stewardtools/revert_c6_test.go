package stewardtools_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/operations/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

func TestC6RevertOperationRegisters(t *testing.T) {
	for _, d := range stewardtools.RevertTools(nil) {
		if err := tool.NewRegistry().Register(d); err != nil {
			t.Fatal(err)
		}
	}
}
func TestC6RevertOperationHandlerRejectsMalformedID(t *testing.T) {
	d := stewardtools.RevertTools(nil)[0]
	for _, raw := range []string{`{}`, `{"operation_id":"not-a-uuid"}`, `{"operation_id":123}`} {
		t.Run(raw, func(t *testing.T) {
			_, err := d.Handler(context.Background(), tool.Caller{}, json.RawMessage(raw))
			var e *tool.Error
			if !errors.As(err, &e) || e.Code != tool.CodeInvalidInput {
				t.Fatalf("输入=%s err=%v", raw, err)
			}
		})
	}
}
