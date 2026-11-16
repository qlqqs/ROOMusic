package memory

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

func TestC10MemoryRejectsInvalidInputBeforeStorage(t *testing.T) {
	store := NewStore(nil)
	for _, text := range []string{"", " \n\t", strings.Repeat("字", 501), "a\x00b", string([]byte{0xff})} {
		if _, err := store.Remember(context.Background(), "user", nil, text); !errors.Is(err, ErrInvalidInput) {
			t.Fatal(err)
		}
	}
	id := "invalid"
	if _, err := store.Remember(context.Background(), "user", &id, "偏好"); !errors.Is(err, ErrInvalidInput) {
		t.Fatal(err)
	}
	if err := store.Forget(context.Background(), "user", &id); !errors.Is(err, ErrInvalidInput) {
		t.Fatal(err)
	}
}
func TestC10MemoryToolValidationCodeAndReadOnlyMetadata(t *testing.T) {
	registry := tool.NewRegistry()
	for _, definition := range NewStore(nil).StewardTools() {
		if definition.Scope != tool.ScopePersonal || definition.ReadOnly != (definition.Name == "list_memories") || !definition.Untrusted || definition.Revertible {
			t.Fatal(definition.Name)
		}
		if err := registry.Register(definition); err != nil {
			t.Fatal(err)
		}
	}
	caller := tool.Caller{User: identity.User{ID: "synthetic", Role: "user"}, Source: tool.SourceChat}
	for _, tc := range []struct{ name, input string }{{"remember", `{}`}, {"remember", `{"content":1}`}, {"remember", `{"content":"a","user_id":"other"}`}, {"forget", `{}`}, {"list_memories", `{"user_id":"other"}`}} {
		_, err := registry.Invoke(context.Background(), caller, tc.name, json.RawMessage(tc.input))
		var typed *tool.Error
		if !errors.As(err, &typed) || string(typed.Code) != "invalid_input" {
			t.Fatal(err)
		}
	}
}
