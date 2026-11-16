package runtime

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/migrations"
)

func runtimeDatabase(t *testing.T) (*sql.DB, *Store, string, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	db, err := postgres.Open(ctx, postgrestest.NewSchema(t, "steward_store_"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	a, b := uuid.New(), uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO users(id,username,password_hash,role) VALUES($1,'synthetic-a','test-only','user'),($2,'synthetic-admin','test-only','admin')`, a, b); err != nil {
		t.Fatal(err)
	}
	return db, NewStore(db), a, b
}
func mustStore(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func TestPostgreSQLStewardSettingsIsolationAndOptionalKey(t *testing.T) {
	_, s, a, b := runtimeDatabase(t)
	ctx := context.Background()
	zero, err := s.GetUserSettings(ctx, a)
	mustStore(t, err)
	if zero.IsConfigured() || zero.Key != nil {
		t.Fatal("new user already configured")
	}
	saved, err := s.SaveUserSettings(ctx, a, UserSettingsInput{DisplayName: "合成管家"})
	mustStore(t, err)
	if saved.DisplayName != "合成管家" || saved.IsConfigured() || saved.Key != nil {
		t.Fatalf("name-only=%+v", saved)
	}
	key := &EncryptedKey{Ciphertext: []byte{1, 2, 3, 4, 5, 6}, LastFour: "1234"}
	probed := time.Now().UTC().Truncate(time.Microsecond)
	input := UserSettingsInput{Endpoint: "http://synthetic.invalid", Protocol: "openai", Model: "synthetic", Key: key, Probe: &ProbeResult{Result: json.RawMessage(`{"tool_support":"supported"}`), ProbedAt: probed}, Overrides: json.RawMessage(`{"model":"manual"}`)}
	saved, err = s.SaveUserSettings(ctx, a, input)
	mustStore(t, err)
	if !saved.IsConfigured() {
		t.Fatal("full settings not configured")
	}
	input.Key = nil
	input.Probe = nil
	input.Overrides = nil
	input.DisplayName = "改名"
	saved, err = s.SaveUserSettings(ctx, a, input)
	mustStore(t, err)
	if !bytes.Equal(saved.Key.Ciphertext, key.Ciphertext) || saved.Key.LastFour != "1234" || saved.Probe == nil || !saved.Probe.ProbedAt.Equal(probed) || !bytes.Contains(saved.Overrides, []byte("manual")) {
		t.Fatalf("partial update lost fields: %+v", saved)
	}
	other, err := s.GetUserSettings(ctx, b)
	mustStore(t, err)
	if other.Key != nil || other.Endpoint != "" || other.DisplayName != "" {
		t.Fatal("administrator sees another user's settings")
	}
	_, err = s.SaveUserSettings(ctx, b, UserSettingsInput{DisplayName: "管理员自己的管家"})
	mustStore(t, err)
	saved, err = s.GetUserSettings(ctx, a)
	mustStore(t, err)
	if saved.DisplayName != "改名" {
		t.Fatal("another user's write changed settings")
	}
	enabled, err := s.GetMembersEnabled(ctx)
	mustStore(t, err)
	if !enabled {
		t.Fatal("members should initially be enabled")
	}
	mustStore(t, s.SetMembersEnabled(ctx, false))
	enabled, err = s.GetMembersEnabled(ctx)
	mustStore(t, err)
	if enabled {
		t.Fatal("members switch not persisted")
	}
}
func TestPostgreSQLStewardConversationIsolationAndCascade(t *testing.T) {
	db, s, a, b := runtimeDatabase(t)
	ctx := context.Background()
	c, err := s.CreateConversation(ctx, a, "原名", "synthetic-model")
	mustStore(t, err)
	if c.Status != "idle" || c.Mode != "assistant" {
		t.Fatalf("conversation=%+v", c)
	}
	mustStore(t, s.RenameConversation(ctx, a, c.ID, "新名"))
	list, err := s.ListConversations(ctx, a, ConversationPage{1, 20})
	mustStore(t, err)
	if len(list) != 1 || list[0].Title != "新名" {
		t.Fatalf("list=%+v", list)
	}
	other, err := s.ListConversations(ctx, b, ConversationPage{1, 20})
	mustStore(t, err)
	if len(other) != 0 {
		t.Fatal("other owner sees conversation")
	}
	for _, owner := range []string{b, uuid.New()} {
		for _, operation := range []func() error{func() error { _, err := s.GetConversation(ctx, owner, c.ID); return err }, func() error { return s.RenameConversation(ctx, owner, c.ID, "侵入") }, func() error { return s.DeleteConversation(ctx, owner, c.ID) }, func() error { return s.AcquireConversation(ctx, owner, c.ID) }, func() error { return s.ReleaseConversation(ctx, owner, c.ID) }, func() error {
			_, err := s.AppendMessage(ctx, owner, c.ID, MessageInput{Role: "user", Content: json.RawMessage(`{}`)})
			return err
		}} {
			if err := operation(); !errors.Is(err, ErrNotFound) {
				t.Fatalf("foreign access=%v", err)
			}
		}
	}
	opaque := json.RawMessage(`{"role":"assistant", "content":[{"type":"opaque","opaque":{"protocol":"messages","data":{"type":"thinking","signature":"synthetic-sig"}}}]}`)
	m, err := s.AppendMessage(ctx, a, c.ID, MessageInput{Role: "assistant", Content: opaque, ToolCalls: []ToolCallInput{{Name: "lookup", Input: json.RawMessage(`{}`), Untrusted: true}}})
	mustStore(t, err)
	if len(m.ToolCalls) != 1 || m.Sequence != 1 {
		t.Fatalf("message=%+v", m)
	}
	call := m.ToolCalls[0]
	if err := s.FinishToolCall(ctx, b, c.ID, call.ID, ToolCallResult{Status: "succeeded", Output: json.RawMessage(`{}`)}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign tool write=%v", err)
	}
	_, err = s.FinishToolCallMessage(ctx, a, c.ID, call.ID, ToolCallResult{Status: "succeeded", Output: json.RawMessage(`{"synthetic":true}`)}, MessageInput{Role: "tool", Content: json.RawMessage(`{"role":"tool","content":[]}`)})
	mustStore(t, err)
	detail, err := s.GetConversation(ctx, a, c.ID)
	mustStore(t, err)
	if len(detail.Messages) != 2 || detail.Messages[1].Sequence != 2 || !bytes.Equal(detail.Messages[0].Content, opaque) || detail.Messages[0].ToolCalls[0].Status != "succeeded" {
		t.Fatalf("detail=%+v", detail)
	}
	mustStore(t, s.AcquireConversation(ctx, a, c.ID))
	if err := s.DeleteConversation(ctx, a, c.ID); !errors.Is(err, ErrConversationBusy) {
		t.Fatalf("delete running=%v", err)
	}
	mustStore(t, s.ReleaseConversation(ctx, a, c.ID))
	mustStore(t, s.DeleteConversation(ctx, a, c.ID))
	_, err = s.GetConversation(ctx, a, c.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted get=%v", err)
	}
	for _, query := range []string{`SELECT count(*) FROM steward_messages WHERE conversation_id=$1`, `SELECT count(*) FROM steward_tool_calls WHERE message_id=$1`} {
		id := c.ID
		if query == `SELECT count(*) FROM steward_tool_calls WHERE message_id=$1` {
			id = m.ID
		}
		var count int
		mustStore(t, db.QueryRow(query, id).Scan(&count))
		if count != 0 {
			t.Fatalf("cascade left %d rows", count)
		}
	}
}
func TestPostgreSQLStewardAcquireAtomicAndStartupRecovery(t *testing.T) {
	_, s, a, _ := runtimeDatabase(t)
	ctx := context.Background()
	c, err := s.CreateConversation(ctx, a, "", "synthetic")
	mustStore(t, err)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; results <- s.AcquireConversation(ctx, a, c.ID) }()
	}
	close(start)
	wg.Wait()
	close(results)
	success, busy := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrConversationBusy) {
			busy++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || busy != 1 {
		t.Fatalf("success=%d busy=%d", success, busy)
	}
	count, err := s.ResetRunningConversations(ctx)
	mustStore(t, err)
	if count != 1 {
		t.Fatalf("recovered=%d", count)
	}
	d, err := s.GetConversation(ctx, a, c.ID)
	mustStore(t, err)
	if d.Conversation.Status != "idle" {
		t.Fatal("recovery did not release")
	}
	count, err = s.ResetRunningConversations(ctx)
	mustStore(t, err)
	if count != 0 {
		t.Fatal("recovery not idempotent")
	}
	mustStore(t, s.AcquireConversation(ctx, a, c.ID))
	mustStore(t, s.ReleaseConversation(ctx, a, c.ID))
}
func TestPostgreSQLStewardAllowlistCRUD(t *testing.T) {
	_, s, a, b := runtimeDatabase(t)
	ctx := context.Background()
	entries, err := s.ListAllowlist(ctx)
	mustStore(t, err)
	if len(entries) != 0 {
		t.Fatal("allowlist not initially empty")
	}
	first, err := s.AddAllowlistEntry(ctx, a, "Synthetic.INVALID.", 1234)
	mustStore(t, err)
	second, err := s.AddAllowlistEntry(ctx, b, "synthetic.invalid", 1234)
	mustStore(t, err)
	if !first.CreatedAt.Equal(second.CreatedAt) || *second.AddedBy != a {
		t.Fatal("duplicate changed creator/time")
	}
	for _, tc := range []struct {
		host string
		port int
		want bool
	}{{"SYNTHETIC.INVALID.", 1234, true}, {"synthetic.invalid", 1235, false}, {"else.invalid", 1234, false}} {
		got, err := s.IsEgressAllowed(ctx, tc.host, tc.port)
		mustStore(t, err)
		if got != tc.want {
			t.Fatalf("allowed %s:%d=%v", tc.host, tc.port, got)
		}
	}
	entries, err = s.ListAllowlist(ctx)
	mustStore(t, err)
	if len(entries) != 1 || entries[0].Host != "synthetic.invalid" {
		t.Fatalf("entries=%+v", entries)
	}
	mustStore(t, s.DeleteAllowlistEntry(ctx, "SYNTHETIC.INVALID.", 1234))
	entries, err = s.ListAllowlist(ctx)
	mustStore(t, err)
	if len(entries) != 0 {
		t.Fatal("delete left entry")
	}
}
func TestPostgreSQLStewardMigration51UpgradeRollbackRoundTrip(t *testing.T) {
	scoped := postgrestest.NewSchema(t, "steward_migration_")
	files := fstest.MapFS{}
	names, err := fs.ReadDir(migrations.Files, ".")
	mustStore(t, err)
	for _, entry := range names {
		v, err := strconv.Atoi(entry.Name()[:4])
		mustStore(t, err)
		if v <= 50 {
			raw, err := migrations.Files.ReadFile(entry.Name())
			mustStore(t, err)
			files[entry.Name()] = &fstest.MapFile{Data: raw}
		}
	}
	ctx := context.Background()
	db, err := postgres.Open(ctx, scoped, postgres.WithMigrationFS(files))
	mustStore(t, err)
	defer db.Close()
	before, err := postgrestest.Snapshot(ctx, db)
	mustStore(t, err)
	// 沿用迁移往返测试的隔离 schema，直接在事务内执行真实 SQL，比较前后逻辑结构。
	up, err := migrations.Files.ReadFile("0051_steward_runtime.sql")
	mustStore(t, err)
	down, err := migrations.Files.ReadFile("0051_steward_runtime.down.sql")
	mustStore(t, err)
	execute := func(raw []byte) {
		t.Helper()
		tx, err := db.BeginTx(ctx, nil)
		mustStore(t, err)
		defer tx.Rollback()
		_, err = tx.ExecContext(ctx, string(raw))
		mustStore(t, err)
		mustStore(t, tx.Commit())
	}
	execute(up)
	after, err := postgrestest.Snapshot(ctx, db)
	mustStore(t, err)
	for _, table := range []string{"steward_user_settings", "steward_instance_settings", "steward_egress_allowlist", "steward_conversations", "steward_messages", "steward_tool_calls"} {
		if _, ok := after.Structure["relation/"+table]; !ok {
			t.Errorf("missing %s", table)
		}
	}
	user := uuid.New()
	_, err = db.Exec(`INSERT INTO users(id,username,password_hash) VALUES($1,'migration-synthetic','test-only')`, user)
	mustStore(t, err)
	_, err = db.Exec(`INSERT INTO steward_user_settings(user_id,display_name) VALUES($1,'合成')`, user)
	mustStore(t, err)
	// 此处只升级到 0051，按当时结构造数据，避免当前 Store 依赖后续方案列。
	conversationID, messageID := uuid.New(), uuid.New()
	_, err = db.Exec(`INSERT INTO steward_conversations(id,user_id,title,model) VALUES($1,$2,'合成','synthetic')`, conversationID, user)
	mustStore(t, err)
	_, err = db.Exec(`INSERT INTO steward_messages(id,conversation_id,sequence,role,content) VALUES($1,$2,1,'assistant','{}')`, messageID, conversationID)
	mustStore(t, err)
	_, err = db.Exec(`INSERT INTO steward_tool_calls(id,message_id,position,tool_name,input) VALUES($1,$2,0,'lookup','{}')`, uuid.New(), messageID)
	mustStore(t, err)
	execute(down)
	_, err = db.Exec(`DELETE FROM users WHERE id=$1`, user)
	mustStore(t, err)
	rolled, err := postgrestest.Snapshot(ctx, db)
	mustStore(t, err)
	if diff := postgrestest.Diff(before, rolled); len(diff) != 0 {
		t.Fatalf("rollback drift: %v", diff)
	}
	execute(up)
	again, err := postgrestest.Snapshot(ctx, db)
	mustStore(t, err)
	if !reflect.DeepEqual(after.Structure, again.Structure) || !reflect.DeepEqual(after.Data, again.Data) {
		t.Fatalf("re-upgrade differs: %v", postgrestest.Diff(after, again))
	}
}
