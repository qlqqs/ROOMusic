package stewardtools_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/operations/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

func TestPostgreSQLStewardOperationToolRolePrivacyAndPagination(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	db, err := postgres.Open(ctx, postgrestest.NewSchema(t, "steward_operations_"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	actor, release, group := uuid.New(), uuid.New(), uuid.New()
	exec(`INSERT INTO users(id,username,password_hash,role) VALUES($1,'synthetic-private-actor','synthetic-private-password','admin')`, actor)
	exec(`INSERT INTO release_groups(id) VALUES($1)`, group)
	exec(`INSERT INTO releases(id,group_id,title,artist) VALUES($1,$2,'合成专辑','合成艺人')`, release, group)
	for i := 0; i < 55; i++ {
		exec(`INSERT INTO operations(id,actor_id,operation_type,target_type,target_id,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id) VALUES($1,$2,'release_correction','release',$3,$4,'synthetic-fingerprint',1,2,'{"private_path":"/synthetic-only/private","lyrics":"synthetic-private-lyrics"}','{}','{"private":"synthetic-private-response"}','synthetic-private-request')`, uuid.New(), actor, release, uuid.New())
	}
	definitions := stewardtools.StewardTools(db, governance.CorrectionOperations())
	if len(definitions) != 1 || definitions[0].MinRole != tool.RoleAdmin || definitions[0].Scope != tool.ScopeRead {
		t.Fatalf("contract=%+v", definitions)
	}
	registry := tool.NewRegistry()
	if err := registry.Register(definitions[0]); err != nil {
		t.Fatal(err)
	}
	_, err = registry.Invoke(ctx, tool.Caller{User: identity.User{Role: "user"}}, "list_operations", json.RawMessage(`{}`))
	var denied *tool.Error
	if !errors.As(err, &denied) || denied.Code != tool.CodePermissionDenied {
		t.Fatalf("member error=%v", err)
	}
	admin := tool.Caller{User: identity.User{Role: "admin", ID: actor}}
	for _, tc := range []struct{ page, size, count int }{{1, 50, 50}, {2, 50, 5}} {
		raw, _ := json.Marshal(map[string]any{"page": tc.page, "page_size": tc.size})
		result, err := registry.Invoke(ctx, admin, "list_operations", raw)
		if err != nil {
			t.Fatal(err)
		}
		for _, private := range []string{"synthetic-private", "/synthetic-only", `"actor"`, `"actor_id"`, `"lyrics"`} {
			if strings.Contains(string(result.Output), private) {
				t.Fatalf("private output field %s", private)
			}
		}
		var value struct {
			Items      []any `json:"items"`
			Pagination struct {
				Total int `json:"total"`
			} `json:"pagination"`
		}
		if err := json.Unmarshal(result.Output, &value); err != nil {
			t.Fatal(err)
		}
		if len(value.Items) != tc.count || value.Pagination.Total != 55 {
			t.Fatalf("page count=%d total=%d", len(value.Items), value.Pagination.Total)
		}
	}
	_, err = registry.Invoke(ctx, admin, "list_operations", json.RawMessage(`{"page_size":51}`))
	var invalid *tool.Error
	if !errors.As(err, &invalid) || invalid.Code != tool.CodeInvalidInput {
		t.Fatalf("page size 51=%v", err)
	}
}
