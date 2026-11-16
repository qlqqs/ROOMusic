package stewardtools

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/library"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

func TestPostgreSQLStewardLibraryToolsRolesPrivacyAndPagination(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	db, err := postgres.Open(ctx, postgrestest.NewSchema(t, "steward_library_"))
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
	scan, root := uuid.New(), uuid.New()
	exec(`INSERT INTO scan_runs(id,status,started_at,feedback_version,phase,progress_bp,phase_completed,phase_total,roots_total,current_root_index,current_root_id,current_root_name,report_state) VALUES($1,'running',NOW(),1,'reading_tags',100,1,55,1,1,$2,'synthetic-private-mount','ready')`, scan, root)
	for i := 0; i < 54; i++ {
		exec(`INSERT INTO scan_runs(id,status,started_at,finished_at) VALUES($1,'succeeded',NOW()-interval '1 hour',NOW())`, uuid.New())
	}
	exec(`INSERT INTO scan_reports(scan_run_id,schema_version,finalized_at,files_counted,files_total,files_processed,count_complete,files_changed,albums_added,albums_updated,albums_missing,tracks_added,tracks_updated,tracks_missing,diagnostics_recorded,diagnostics_may_be_truncated,missing_reconciled) VALUES($1,1,NOW(),55,55,55,true,false,55,0,0,55,0,0,0,false,true)`, scan)
	for i := 0; i < 55; i++ {
		exec(`INSERT INTO scan_added_releases(scan_run_id,release_id,group_id_at_creation,added_at,title_at_creation,artist_at_creation) VALUES($1,$2,$3,NOW(),'合成专辑','合成艺人')`, scan, uuid.New(), uuid.New())
	}
	service := library.NewService(ctx, db, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	registry := tool.NewRegistry()
	definitions := StewardTools(service)
	if len(definitions) != 3 {
		t.Fatal("uncovered scan tools")
	}
	for _, d := range definitions {
		if err := registry.Register(d); err != nil {
			t.Fatal(err)
		}
	}
	member := tool.Caller{User: identity.User{Role: "user"}}
	admin := tool.Caller{User: identity.User{Role: "admin"}}
	for _, d := range definitions {
		t.Run(d.Name, func(t *testing.T) {
			want := tool.RoleAdmin
			input := json.RawMessage(`{"page_size":50}`)
			if d.Name == "get_scan_status" {
				want = tool.RoleMember
				input = json.RawMessage(`{}`)
			} else if d.Name == "get_scan_report" {
				input = json.RawMessage(`{"id":"` + scan + `","page_size":50}`)
			}
			if d.MinRole != want || d.Scope != tool.ScopeRead {
				t.Fatalf("contract=%+v", d)
			}
			if want == tool.RoleAdmin {
				_, err := registry.Invoke(ctx, member, d.Name, input)
				var denied *tool.Error
				if !errors.As(err, &denied) || denied.Code != tool.CodePermissionDenied {
					t.Fatalf("member error=%v", err)
				}
			}
			result, err := registry.Invoke(ctx, admin, d.Name, input)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(result.Output), "synthetic-private-mount") || strings.Contains(string(result.Output), `"lyrics"`) {
				t.Fatalf("unsafe scan output=%s", result.Output)
			}
			var value map[string]any
			if err := json.Unmarshal(result.Output, &value); err != nil {
				t.Fatal(err)
			}
			if d.Name == "list_scans" {
				if len(value["items"].([]any)) != 50 {
					t.Fatal("list pagination not applied")
				}
			}
			if d.Name == "get_scan_report" {
				albums := value["added_albums"].(map[string]any)
				if len(albums["items"].([]any)) != 50 {
					t.Fatal("report pagination not applied")
				}
			}
			if want == tool.RoleAdmin {
				var args map[string]any
				_ = json.Unmarshal(input, &args)
				args["page_size"] = 51
				raw, _ := json.Marshal(args)
				_, err := registry.Invoke(ctx, admin, d.Name, raw)
				var invalid *tool.Error
				if !errors.As(err, &invalid) || invalid.Code != tool.CodeInvalidInput {
					t.Fatalf("page 51 error=%v", err)
				}
			}
		})
	}
	_, missingErr := registry.Invoke(ctx, admin, "get_scan_report", json.RawMessage(`{"id":"`+uuid.New()+`"}`))
	var missing *tool.Error
	if !errors.As(missingErr, &missing) || missing.Code != "not_found" {
		t.Fatalf("目标不存在应返回 not_found，实际 %v", missingErr)
	}
	result, err := registry.Invoke(ctx, member, "get_scan_status", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(result.Output), root) || strings.Contains(string(result.Output), "synthetic-private-mount") {
		t.Fatalf("member root leak=%s", result.Output)
	}
}
