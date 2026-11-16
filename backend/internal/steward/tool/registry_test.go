package tool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/identity"
)

func testDefinition(name string) Definition {
	return Definition{Name: name, MinRole: RoleMember, Scope: ScopeRead, Input: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`), Output: json.RawMessage(`{}`), Handler: func(context.Context, Caller, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"ok":true}`), nil
	}}
}
func registerTest(t *testing.T, r *Registry, d Definition) {
	t.Helper()
	if err := r.Register(d); err != nil {
		t.Fatal(err)
	}
}
func TestRegistryFiltersAndReauthorizesEveryInvocation(t *testing.T) {
	r := NewRegistry()
	member := Caller{User: identity.User{ID: "synthetic", Role: "user"}}
	admin := member
	admin.User.Role = "admin"
	d := testDefinition("admin")
	d.MinRole = RoleAdmin
	calls := 0
	d.Handler = func(context.Context, Caller, json.RawMessage) (json.RawMessage, error) {
		calls++
		return json.RawMessage(`{}`), nil
	}
	registerTest(t, r, d)
	registerTest(t, r, testDefinition("member"))
	if got := r.List(member); len(got) != 1 || got[0].Name != "member" {
		t.Fatalf("member list: %+v", got)
	}
	if got := r.List(admin); len(got) != 2 || got[0].Handler != nil {
		t.Fatalf("admin list: %+v", got)
	}
	if len(r.List(Caller{})) != 0 {
		t.Fatal("anonymous caller sees tools")
	}
	if _, err := r.Invoke(context.Background(), admin, "admin", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	admin.User.Role = "user"
	_, err := r.Invoke(context.Background(), admin, "admin", json.RawMessage(`{}`))
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != CodePermissionDenied || calls != 1 {
		t.Fatalf("second invocation = %v, calls=%d", err, calls)
	}
}
func TestRegistryInvocationErrorsAreDistinct(t *testing.T) {
	r := NewRegistry()
	registerTest(t, r, testDefinition("read"))
	d := testDefinition("admin")
	d.MinRole = RoleAdmin
	registerTest(t, r, d)
	for _, scope := range []Scope{ScopePersonal, ScopeShared} {
		d := testDefinition(string(scope))
		d.Scope = scope
		registerTest(t, r, d)
	}
	for _, tc := range []struct {
		name, input string
		code        ErrorCode
	}{{"missing", "{}", CodeNotFound}, {"admin", "{}", CodePermissionDenied}, {"personal", "{}", CodeUnsupportedScope}, {"shared", "{}", CodeUnsupportedScope}, {"read", "{", CodeInvalidInput}, {"read", `{"extra":1}`, CodeInvalidInput}} {
		t.Run(tc.name+tc.input, func(t *testing.T) {
			_, err := r.Invoke(context.Background(), Caller{User: identity.User{Role: "user"}}, tc.name, json.RawMessage(tc.input))
			var typed *Error
			if !errors.As(err, &typed) || typed.Code != tc.code {
				t.Fatalf("got %v, want %s", err, tc.code)
			}
		})
	}
}
func TestRegistrySchemaSubset(t *testing.T) {
	for _, tc := range []struct{ name, schema, valid, invalid string }{
		{"object_properties_required", `{"type":"object","properties":{"x":{"type":"string"}},"required":["x"],"additionalProperties":false}`, `{"x":"好"}`, `{"x":1}`},
		{"required", `{"required":["x"]}`, `{"x":null}`, `{}`},
		{"additionalProperties", `{"additionalProperties":false}`, `{}`, `{"x":1}`},
		{"array_items", `{"type":"array","items":{"type":"boolean"}}`, `[true,false]`, `[true,1]`},
		{"string_length_unicode", `{"type":"string","minLength":2,"maxLength":3}`, `"中文"`, `"文"`},
		{"maxLength", `{"maxLength":2}`, `"中文"`, `"三个字"`},
		{"number_minimum", `{"type":"number","minimum":1.5}`, `1.5`, `1.49`},
		{"maximum_exact_integer", `{"type":"integer","maximum":9007199254740992}`, `9007199254740992`, `9007199254740993`},
		{"integer", `{"type":"integer"}`, `1e2`, `1.5`},
		{"enum_structural", `{"enum":[{"x":[1,true]}]}`, `{"x":[1.0,true]}`, `{"x":[2,true]}`},
		{"null", `{"type":"null"}`, `null`, `false`},
		{"boolean", `{"type":"boolean"}`, `true`, `"true"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := NewRegistry()
			d := testDefinition("schema")
			d.Input = json.RawMessage(tc.schema)
			registerTest(t, r, d)
			caller := Caller{User: identity.User{Role: "user"}}
			if _, err := r.Invoke(context.Background(), caller, d.Name, json.RawMessage(tc.valid)); err != nil {
				t.Fatalf("valid input: %v", err)
			}
			_, err := r.Invoke(context.Background(), caller, d.Name, json.RawMessage(tc.invalid))
			var typed *Error
			if !errors.As(err, &typed) || typed.Code != CodeInvalidInput {
				t.Fatalf("invalid input accepted: %v", err)
			}
		})
	}
}
func TestRegistryRejectsInvalidRegistrations(t *testing.T) {
	for _, schema := range []string{`{"pattern":"x"}`, `{"$ref":"#"}`, `{"oneOf":[]}`, `{"properties":{"x":{"format":"uri"}}}`, `{"additionalProperties":true}`, `{"type":["string","null"]}`, `{"required":["x","x"]}`, `{"enum":[1,1.0]}`, `{"minLength":-1}`} {
		t.Run(schema, func(t *testing.T) {
			for _, output := range []bool{false, true} {
				r := NewRegistry()
				d := testDefinition("bad")
				if output {
					d.Output = json.RawMessage(schema)
				} else {
					d.Input = json.RawMessage(schema)
				}
				if err := r.Register(d); err == nil {
					t.Fatal("unsupported schema registered")
				}
				if len(r.List(Caller{User: identity.User{Role: "admin"}})) != 0 {
					t.Fatal("failed registration changed registry")
				}
			}
		})
	}
	r := NewRegistry()
	for _, name := range []string{"", "  "} {
		if err := r.Register(testDefinition(name)); err == nil {
			t.Fatal("empty name accepted")
		}
	}
	registerTest(t, r, testDefinition("same"))
	if err := r.Register(testDefinition("same")); err == nil {
		t.Fatal("duplicate accepted")
	}
}
func TestRegistryOutputTruncationPreservesJSON(t *testing.T) {
	for _, value := range []string{"small", strings.Repeat("中文\"<&\\", 20000)} {
		r := NewRegistry()
		d := testDefinition("large")
		raw, _ := json.Marshal(map[string]string{"data": value})
		d.Untrusted = true
		d.Handler = func(context.Context, Caller, json.RawMessage) (json.RawMessage, error) { return raw, nil }
		registerTest(t, r, d)
		result, err := r.Invoke(context.Background(), Caller{User: identity.User{Role: "user"}}, d.Name, json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		if !json.Valid(result.Output) || !utf8.Valid(result.Output) || len(result.Output) > 64*1024 || result.Truncated != (len(raw) > 64*1024) || !result.Untrusted {
			t.Fatalf("invalid bounded output: bytes=%d truncated=%v", len(result.Output), result.Truncated)
		}
		if !result.Truncated && string(result.Output) != string(raw) {
			t.Fatal("small output changed")
		}
		if result.Truncated {
			var prefix string
			if err := json.Unmarshal(result.Output, &prefix); err != nil || !strings.HasPrefix(string(raw), prefix) {
				t.Fatal("truncated JSON is not an original prefix")
			}
		}
	}
}
