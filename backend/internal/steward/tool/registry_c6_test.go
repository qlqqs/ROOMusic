package tool

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/identity"
)

func TestC6RegistryNULDecodedKeysAndValues(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		reject    bool
	}{
		{"value", `{"text":"a\u0000b"}`, true},
		{"key", `{"a\u0000b":"value"}`, true},
		{"nested_array", `{"nested":[{"text":"\u0000"}]}`, true},
		{"literal_value", `{"text":"a\\u0000b"}`, false},
		{"literal_key", `{"a\\u0000b":["\\u0000"]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := NewRegistry()
			previews, executions := 0, 0
			d := testDefinition("c6_input")
			d.Scope = ScopeShared
			d.Input = json.RawMessage(`{"type":"object"}`)
			d.Preview = func(context.Context, Caller, json.RawMessage) (Preview, error) { previews++; return Preview{}, nil }
			d.Handler = func(context.Context, Caller, json.RawMessage) (json.RawMessage, error) {
				executions++
				return json.RawMessage(`{}`), nil
			}
			registerTest(t, r, d)
			caller := Caller{User: identity.User{Role: "admin"}, SharedExecution: true}
			_, previewErr := r.PreviewCall(context.Background(), caller, d.Name, json.RawMessage(tc.raw))
			_, invokeErr := r.Invoke(context.Background(), caller, d.Name, json.RawMessage(tc.raw))
			for _, err := range []error{previewErr, invokeErr} {
				var typed *Error
				if tc.reject {
					if !errors.As(err, &typed) || typed.Code != CodeInvalidInput {
						t.Fatalf("NUL 未拒绝：%v", err)
					}
				} else if err != nil {
					t.Fatalf("字面量被拒绝：%v", err)
				}
			}
			want := 1
			if tc.reject {
				want = 0
			}
			if previews != want || executions != want {
				t.Fatalf("输入边界之后调用 preview=%d handler=%d want=%d", previews, executions, want)
			}
		})
	}
}
