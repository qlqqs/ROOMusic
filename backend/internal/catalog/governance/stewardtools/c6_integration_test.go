package stewardtools

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/artistgraph"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/writegate"
	"github.com/qlqq/roomusic/backend/internal/catalog/maintenance"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/operations"
	operationtools "github.com/qlqq/roomusic/backend/internal/operations/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

type c6Resolution struct{}

func (c6Resolution) RefreshArtistResolution(context.Context, *sql.Tx, map[string]string) error {
	return nil
}

type c6Fixture struct {
	t        *testing.T
	db       *sql.DB
	registry *tool.Registry
	caller   tool.Caller
	root     string
	counter  int
}

func newC6Fixture(t *testing.T) *c6Fixture {
	t.Helper()
	db, err := postgres.Open(context.Background(), postgrestest.NewSchema(t, "c6_governance_"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := maintenance.Initialize(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	f := &c6Fixture{t: t, db: db, registry: tool.NewRegistry(), root: uuid.New(), caller: tool.Caller{User: identity.User{ID: uuid.New(), Role: "admin"}, Source: tool.SourceChat, ConversationID: uuid.New(), Mode: "operator", SharedExecution: true, IdempotencyKey: uuid.New(), RequestID: "c6-test"}}
	f.exec(`INSERT INTO users(id,username,password_hash,role) VALUES($1,'c6-admin','synthetic','admin')`, f.caller.User.ID)
	f.exec(`INSERT INTO library_roots(id,path) VALUES($1,'/synthetic-c6')`, f.root)
	defs := StewardTools(db, c6Resolution{})
	defs = append(defs, operationtools.StewardTools(db, governance.CorrectionOperations())...)
	for _, d := range defs {
		if err := f.registry.Register(d); err != nil {
			t.Fatal(err)
		}
	}
	return f
}
func (f *c6Fixture) exec(q string, args ...any) {
	f.t.Helper()
	if _, err := f.db.Exec(q, args...); err != nil {
		f.t.Fatal(err)
	}
}
func (f *c6Fixture) release(artist string) (r, g, m string) {
	f.t.Helper()
	f.counter++
	r, g, m = uuid.New(), uuid.New(), uuid.New()
	f.exec(`INSERT INTO release_groups(id) VALUES($1)`, g)
	f.exec(`INSERT INTO releases(id,group_id,title,artist,album_artist,source_root_id,relative_directory,candidate_anchor,candidate_kind) VALUES($1,$2,'原始标题',$3,$3,$4,$5,$6,'ordinary_directory')`, r, g, artist, f.root, fmt.Sprint(f.counter), r)
	f.exec(`INSERT INTO media(id,release_id,position) VALUES($1,$2,1)`, m, r)
	f.exec(`INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,source_kind) VALUES($1,$2,1,'合成曲目',$3,1,$4,$5,'present',now(),'physical')`, uuid.New(), m, artist, f.root, fmt.Sprintf("%d.wav", f.counter))
	return
}
func (f *c6Fixture) raw(v any) json.RawMessage {
	f.t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		f.t.Fatal(err)
	}
	return raw
}
func (f *c6Fixture) call(name string, v any) json.RawMessage {
	f.t.Helper()
	f.caller.IdempotencyKey = uuid.New()
	result, err := f.registry.Invoke(context.Background(), f.caller, name, f.raw(v))
	if err != nil {
		f.t.Fatalf("%s：%v", name, err)
	}
	return result.Output
}
func (f *c6Fixture) count() int {
	f.t.Helper()
	var n int
	if err := f.db.QueryRow(`SELECT count(*) FROM operations`).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}
func (f *c6Fixture) preview(name string, v any) {
	f.t.Helper()
	before, err := postgrestest.Snapshot(context.Background(), f.db)
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.registry.PreviewCall(context.Background(), f.caller, name, f.raw(v)); err != nil {
		f.t.Fatal(name, err)
	}
	after, err := postgrestest.Snapshot(context.Background(), f.db)
	if err != nil {
		f.t.Fatal(err)
	}
	if diff := postgrestest.Diff(before, after); len(diff) != 0 {
		f.t.Fatalf("Preview 写库：%v", diff)
	}
}
func (f *c6Fixture) artists() (alpha, beta, keyAlpha, keyBeta string) {
	f.t.Helper()
	a, _, _ := f.release("Alpha")
	b, _, _ := f.release("Beta")
	f.exec(`UPDATE catalog_projection_state SET search_projection_version=$1 WHERE capability='artists'`, artistgraph.Version)
	tx, err := f.db.Begin()
	if err != nil {
		f.t.Fatal(err)
	}
	defer tx.Rollback()
	if err := writegate.Lock(context.Background(), tx); err != nil {
		f.t.Fatal(err)
	}
	if err := artistgraph.Refresh(context.Background(), tx, []string{a, b}); err != nil {
		f.t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		f.t.Fatal(err)
	}
	f.exec(`UPDATE catalog_projection_state SET status='ready',manifest_sealed=true,published_generation=target_generation,published_textnorm_version=textnorm_version,published_search_projection_version=search_projection_version WHERE capability='artists'`)
	if err := f.db.QueryRow(`SELECT initial_artist_id::text,id::text FROM artist_name_keys WHERE normalized_key='alpha'`).Scan(&alpha, &keyAlpha); err != nil {
		f.t.Fatal(err)
	}
	if err := f.db.QueryRow(`SELECT initial_artist_id::text,id::text FROM artist_name_keys WHERE normalized_key='beta'`).Scan(&beta, &keyBeta); err != nil {
		f.t.Fatal(err)
	}
	return
}
func correction(id string, rev int, title string) map[string]any {
	return map[string]any{"release_id": id, "expected_revision": rev, "changes": map[string]any{"title": map[string]any{"mode": "set", "value": title}}}
}
func TestPostgreSQLStewardC6ReleaseTools(t *testing.T) {
	for _, name := range []string{"correct_release", "change_release_group", "set_default_release", "review_release_issue", "preview_release_claim", "claim_release", "revert_operation"} {
		t.Run(name, func(t *testing.T) {
			f := newC6Fixture(t)
			r, g, m := f.release("合成艺人")
			var input map[string]any
			var check func(json.RawMessage)
			switch name {
			case "correct_release":
				input = correction(r, 1, "修正后")
				check = func(_ json.RawMessage) {
					var title string
					f.db.QueryRow(`SELECT title FROM release_effective WHERE id=$1`, r).Scan(&title)
					if title != "修正后" {
						t.Fatal(title)
					}
				}
			case "change_release_group":
				_, target, _ := f.release("其他艺人")
				input = map[string]any{"release_id": r, "expected_revision": 1, "action": "join", "group_id": target}
				check = func(_ json.RawMessage) {
					var actual string
					f.db.QueryRow(`SELECT group_id::text FROM releases WHERE id=$1`, r).Scan(&actual)
					if actual != target {
						t.Fatal(actual)
					}
				}
			case "set_default_release":
				input = map[string]any{"group_id": g, "expected_revision": 1, "release_id": r}
				check = func(_ json.RawMessage) {
					var actual string
					f.db.QueryRow(`SELECT default_release_id::text FROM release_groups WHERE id=$1`, g).Scan(&actual)
					if actual != r {
						t.Fatal(actual)
					}
				}
			case "review_release_issue":
				f.exec(`INSERT INTO tracks(id,medium_id,position,title,artist,source_root_id,relative_path,source_status,observed_at) VALUES($1,$2,2,'缺失','合成艺人',$3,'missing.wav','missing',now())`, uuid.New(), m, f.root)
				issues, err := governance.NewStore(f.db).LoadReleaseIssues(context.Background(), r)
				if err != nil || issues.MissingTracks == nil {
					t.Fatal(issues, err)
				}
				input = map[string]any{"release_id": r, "expected_revision": 1, "issue": "missing_tracks", "action": "review", "basis_token": issues.MissingTracks.Basis}
				check = func(_ json.RawMessage) {
					var n int
					f.db.QueryRow(`SELECT count(*) FROM release_issue_reviews WHERE release_id=$1`, r).Scan(&n)
					if n != 1 {
						t.Fatal(n)
					}
				}
			case "preview_release_claim", "claim_release":
				f.call("correct_release", correction(r, 1, "待认领标题"))
				f.exec(`UPDATE tracks SET source_status='missing' WHERE medium_id=$1`, m)
				target, _, _ := f.release("合成艺人")
				input = map[string]any{"release_id": target, "source_release_id": r}
				if name == "claim_release" {
					raw := f.call("preview_release_claim", input)
					var preview governance.ClaimPreview
					if err := json.Unmarshal(raw, &preview); err != nil {
						t.Fatal(err)
					}
					input["expected_revision"] = preview.Revision
					input["source_revision"] = preview.SourceRevision
					input["preview"] = preview.Token
					check = func(_ json.RawMessage) {
						var title string
						f.db.QueryRow(`SELECT title FROM release_effective WHERE id=$1`, target).Scan(&title)
						if title != "待认领标题" {
							t.Fatal(title)
						}
					}
				} else {
					check = func(raw json.RawMessage) {
						var p governance.ClaimPreview
						if err := json.Unmarshal(raw, &p); err != nil || p.Token == "" || len(p.Items) == 0 {
							t.Fatal(string(raw), err)
						}
					}
				}
			case "revert_operation":
				raw := f.call("correct_release", correction(r, 1, "撤销目标"))
				var result struct {
					OperationID string `json:"operation_id"`
				}
				json.Unmarshal(raw, &result)
				input = map[string]any{"operation_id": result.OperationID}
				check = func(_ json.RawMessage) {
					var title string
					f.db.QueryRow(`SELECT title FROM release_effective WHERE id=$1`, r).Scan(&title)
					if title != "原始标题" {
						t.Fatal(title)
					}
					var n int
					f.db.QueryRow(`SELECT count(*) FROM operations WHERE reverts_operation_id=$1 AND origin->>'source'='chat'`, result.OperationID).Scan(&n)
					if n != 1 {
						t.Fatal("没有撤销管家操作")
					}
				}
			}
			if name == "revert_operation" {
				// 注册表可用性由 TestC6RevertOperationRegisters 单独守住，此处验证处理器与撤销执行器。
				d := operationtools.RevertTools(f.db, governance.CorrectionOperations())[0]
				snapshot, err := postgrestest.Snapshot(context.Background(), f.db)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := d.Preview(context.Background(), f.caller, f.raw(input)); err != nil {
					t.Fatal(err)
				}
				after, err := postgrestest.Snapshot(context.Background(), f.db)
				if err != nil {
					t.Fatal(err)
				}
				if diff := postgrestest.Diff(snapshot, after); len(diff) != 0 {
					t.Fatal(diff)
				}
				f.caller.IdempotencyKey = uuid.New()
				out, err := d.Handler(context.Background(), f.caller, f.raw(input))
				if err != nil {
					t.Fatal(err)
				}
				check(out)
				return
			}
			before := f.count()
			if name != "preview_release_claim" {
				f.preview(name, input)
			}
			snapshot, err := postgrestest.Snapshot(context.Background(), f.db)
			if err != nil {
				t.Fatal(err)
			}
			out := f.call(name, input)
			if check != nil {
				check(out)
			}
			if name == "preview_release_claim" {
				after, err := postgrestest.Snapshot(context.Background(), f.db)
				if err != nil {
					t.Fatal(err)
				}
				if diff := postgrestest.Diff(snapshot, after); len(diff) != 0 {
					t.Fatal("认领预览写库", diff)
				}
			} else {
				if f.count() != before+1 {
					t.Fatal("未经 operations.Run 记录")
				}
			}
		})
	}
}
func TestPostgreSQLStewardC6ArtistTools(t *testing.T) {
	for _, name := range []string{"merge_artists", "rename_artist", "ignore_artist_candidate", "preview_artist_split", "split_artist"} {
		t.Run(name, func(t *testing.T) {
			f := newC6Fixture(t)
			a, b, ka, kb := f.artists()
			input := map[string]any{"artist_id": a, "expected_revision": 1}
			switch name {
			case "merge_artists":
				input["source_artist_id"] = b
				input["source_expected_revision"] = 1
			case "rename_artist":
				input["mode"] = "set"
				input["name"] = "人工主名"
			case "ignore_artist_candidate":
				input["other_artist_id"] = b
				input["other_expected_revision"] = 1
				input["ignored"] = true
				var release string
				if err := f.db.QueryRow(`SELECT id::text FROM releases WHERE album_artist='Alpha'`).Scan(&release); err != nil {
					t.Fatal(err)
				}
				f.exec(`INSERT INTO artist_candidate_signals(left_key_id,right_key_id,kind,release_id,left_name,right_name) VALUES(least($1::uuid,$2::uuid),greatest($1::uuid,$2::uuid),'album_track_one_to_one',$3,'合成甲','合成乙')`, ka, kb, release)
				left, right := ka, kb
				if left > right {
					left, right = right, left
				}
				input["key_pairs"] = []any{map[string]string{"left_key_id": left, "right_key_id": right}}
			case "preview_artist_split", "split_artist":
				f.call("merge_artists", map[string]any{"artist_id": a, "expected_revision": 1, "source_artist_id": b, "source_expected_revision": 1})
				input = map[string]any{"artist_id": a, "mode": "aliases", "name_key_ids": []string{kb}}
				if name == "split_artist" {
					raw := f.call("preview_artist_split", input)
					var p struct {
						Basis string `json:"basis"`
					}
					if err := json.Unmarshal(raw, &p); err != nil || p.Basis == "" {
						t.Fatal(string(raw), err)
					}
					input["expected_revision"] = 2
					input["basis_token"] = p.Basis
					input["target"] = map[string]any{"kind": "restore", "artist_id": b, "expected_revision": 2}
				}
			}
			before := f.count()
			if name != "preview_artist_split" {
				f.preview(name, input)
			}
			snapshot, err := postgrestest.Snapshot(context.Background(), f.db)
			if err != nil {
				t.Fatal(err)
			}
			out := f.call(name, input)
			if name == "preview_artist_split" {
				var p struct {
					Basis string `json:"basis"`
				}
				if err := json.Unmarshal(out, &p); err != nil || p.Basis == "" {
					t.Fatal(string(out), err)
				}
				after, err := postgrestest.Snapshot(context.Background(), f.db)
				if err != nil {
					t.Fatal(err)
				}
				if diff := postgrestest.Diff(snapshot, after); len(diff) != 0 {
					t.Fatal("拆分预览写库", diff)
				}
			} else {
				if f.count() != before+1 {
					t.Fatal("缺少操作记录")
				}
				var rev int
				f.db.QueryRow(`SELECT edit_revision FROM artists WHERE id=$1`, a).Scan(&rev)
				want := 2
				if name == "split_artist" {
					want = 3
				}
				if rev != want {
					t.Fatalf("revision=%d want=%d", rev, want)
				}
				if name == "rename_artist" {
					var actual string
					f.db.QueryRow(`SELECT effective_display_name FROM artist_profiles WHERE artist_id=$1`, a).Scan(&actual)
					if actual != "人工主名" {
						t.Fatal(actual)
					}
				}
			}
		})
	}
}
func TestPostgreSQLStewardC6ToolsRejectInputAndMember(t *testing.T) {
	f := newC6Fixture(t)
	for _, d := range StewardTools(f.db, nil) {
		t.Run(d.Name, func(t *testing.T) {
			before := f.count()
			_, err := f.registry.Invoke(context.Background(), f.caller, d.Name, json.RawMessage(`{}`))
			var e *tool.Error
			if !errors.As(err, &e) || e.Code != tool.CodeInvalidInput {
				t.Fatalf("空输入=%v", err)
			}
			member := f.caller
			member.User.Role = "user"
			_, err = f.registry.Invoke(context.Background(), member, d.Name, json.RawMessage(`{}`))
			if !errors.As(err, &e) || e.Code != tool.CodePermissionDenied {
				t.Fatalf("成员=%v", err)
			}
			if f.count() != before {
				t.Fatal("拒绝后写库")
			}
		})
	}
}
func TestPostgreSQLStewardC6DirectIdempotencyAndOrigin(t *testing.T) {
	f := newC6Fixture(t)
	r, _, _ := f.release("合成")
	input := f.raw(correction(r, 1, "直接执行"))
	first, err := f.registry.Invoke(context.Background(), f.caller, "correct_release", input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.registry.Invoke(context.Background(), f.caller, "correct_release", input)
	var firstJSON, secondJSON any
	json.Unmarshal(first.Output, &firstJSON)
	json.Unmarshal(second.Output, &secondJSON)
	if err != nil || !reflect.DeepEqual(firstJSON, secondJSON) || f.count() != 1 {
		t.Fatal("同键重复执行", err)
	}
	list := f.call("list_operations", map[string]any{})
	var result struct {
		Items []struct {
			Origin operations.Origin `json:"origin"`
		} `json:"items"`
	}
	if err := json.Unmarshal(list, &result); err != nil || len(result.Items) != 1 || result.Items[0].Origin.ConversationID != f.caller.ConversationID || result.Items[0].Origin.Mode != "operator" {
		t.Fatal(string(list), err)
	}
}

// 保留执行器原始错误链，区分夹具不足与工具映射后的 operation_failed。
func TestPostgreSQLStewardC6ExecutorOriginDiagnostic(t *testing.T) {
	f := newC6Fixture(t)
	r, _, _ := f.release("合成")
	c, err := (adapter{db: f.db}).command("correct_release", f.raw(correction(r, 1, "诊断标题")))
	if err != nil {
		t.Fatal(err)
	}
	_, err = operations.Run(context.Background(), f.db, operations.Spec{ActorID: f.caller.User.ID, Type: c.kind, Key: uuid.New(), Fingerprint: c.fingerprint, Origin: &operations.Origin{Source: "chat", ConversationID: f.caller.ConversationID, Mode: "operator"}}, c.execute)
	if err != nil {
		t.Fatalf("执行器原始失败：%+v", err)
	}
}

func TestPostgreSQLStewardC6BusinessInputRejections(t *testing.T) {
	f := newC6Fixture(t)
	r, g, _ := f.release("合成")
	id := uuid.New()
	for _, tc := range []struct {
		name  string
		input any
		code  tool.ErrorCode
	}{
		{"correct_release", correction(r, 1, ""), "invalid_correction"},
		{"change_release_group", map[string]any{"release_id": r, "expected_revision": 1, "action": "detach", "group_id": g}, tool.CodeInvalidInput},
		{"set_default_release", map[string]any{"group_id": g, "expected_revision": 1, "release_id": "invalid"}, tool.CodeInvalidInput},
		{"review_release_issue", map[string]any{"release_id": r, "expected_revision": 1, "issue": "missing_tracks", "action": "review"}, tool.CodeInvalidInput},
		{"preview_release_claim", map[string]any{"release_id": r, "source_release_id": r}, tool.CodeInvalidInput},
		{"claim_release", map[string]any{"release_id": r, "expected_revision": 1, "source_release_id": r, "source_revision": 1, "preview": "forged"}, tool.CodeInvalidInput},
		{"merge_artists", map[string]any{"artist_id": id, "expected_revision": 1, "source_artist_id": id, "source_expected_revision": 0}, tool.CodeInvalidInput},
		{"preview_artist_split", map[string]any{"artist_id": id, "mode": "aliases", "name_key_ids": []string{}}, "invalid_artist_scope"},
		{"split_artist", map[string]any{"artist_id": id, "expected_revision": 1, "mode": "aliases", "name_key_ids": []string{id}, "basis_token": string(make([]byte, 64)), "target": map[string]string{"kind": "new"}}, "invalid_artist_scope"},
		{"rename_artist", map[string]any{"artist_id": id, "expected_revision": 1, "mode": "set", "name": "  "}, "invalid_artist_name"},
		{"ignore_artist_candidate", map[string]any{"artist_id": id, "expected_revision": 1, "other_artist_id": uuid.New(), "other_expected_revision": 1, "ignored": true, "key_pairs": []any{}}, "invalid_artist_scope"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, err := postgrestest.Snapshot(context.Background(), f.db)
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.registry.Invoke(context.Background(), f.caller, tc.name, f.raw(tc.input))
			var e *tool.Error
			if !errors.As(err, &e) || e.Code != tc.code {
				t.Fatalf("code want=%s err=%v", tc.code, err)
			}
			after, err := postgrestest.Snapshot(context.Background(), f.db)
			if err != nil {
				t.Fatal(err)
			}
			if diff := postgrestest.Diff(before, after); len(diff) != 0 {
				t.Fatal("非法输入写库", diff)
			}
		})
	}
}
