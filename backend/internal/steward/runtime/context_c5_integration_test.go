package runtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/migrations"
)

func TestPostgreSQLStewardC5Migration53RoundTrip(t *testing.T) {
	scoped := postgrestest.NewSchema(t, "steward_migration_")
	files := fstest.MapFS{}
	names, err := fs.ReadDir(migrations.Files, ".")
	mustStore(t, err)
	for _, entry := range names {
		v, err := strconv.Atoi(entry.Name()[:4])
		mustStore(t, err)
		if v <= 52 {
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
	up, err := migrations.Files.ReadFile("0053_steward_message_context.sql")
	mustStore(t, err)
	down, err := migrations.Files.ReadFile("0053_steward_message_context.down.sql")
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
	user := uuid.New()
	_, err = db.Exec(`INSERT INTO users(id,username,password_hash) VALUES($1,'c5-migration','synthetic')`, user)
	mustStore(t, err)
	s := NewStore(db)
	c, err := s.CreateConversation(ctx, user, "合成", "model")
	mustStore(t, err)
	raw := json.RawMessage(`{"page":{"kind":"other","title":"合成"}}`)
	msg, err := s.AppendMessage(ctx, user, c.ID, MessageInput{Role: "user", Content: json.RawMessage(`{"role":"user","content":[{"type":"text","text":"旧消息"}]}`), Context: raw})
	mustStore(t, err)
	var saved string
	mustStore(t, db.QueryRow(`SELECT context::text FROM steward_messages WHERE id=$1`, msg.ID).Scan(&saved))
	if !strings.Contains(saved, "合成") {
		t.Fatal(saved)
	}
	// 回退只移除上下文列，保留原始正文；再次升级后旧消息得到 SQL NULL。
	execute(down)
	var content string
	mustStore(t, db.QueryRow(`SELECT content::text FROM steward_messages WHERE id=$1`, msg.ID).Scan(&content))
	if !strings.Contains(content, "旧消息") {
		t.Fatal(content)
	}
	execute(up)
	var empty sql.NullString
	mustStore(t, db.QueryRow(`SELECT context::text FROM steward_messages WHERE id=$1`, msg.ID).Scan(&empty))
	if empty.Valid {
		t.Fatalf("再次升级的旧消息上下文应为空：%v", empty)
	}
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

func TestPostgreSQLStewardC5ContextStoreOwnership(t *testing.T) {
	_, s, a, b := runtimeDatabase(t)
	ctx := context.Background()
	c, err := s.CreateConversation(ctx, a, "合成", "model")
	mustStore(t, err)
	other, err := s.CreateConversation(ctx, b, "另一个用户", "model")
	mustStore(t, err)
	text := json.RawMessage(`{"role":"user","content":[{"type":"text","text":"合成问题"}]}`)
	original := json.RawMessage(`{"page":{"kind":"other","title":"旧页面"}}`)
	msg, err := s.AppendMessage(ctx, a, c.ID, MessageInput{Role: "user", Content: text, Context: original})
	mustStore(t, err)
	updated := json.RawMessage(`{"page":{"kind":"other","title":"新页面"}}`)
	for _, tc := range []struct{ name, user, conversation string }{{"foreign_user", b, c.ID}, {"foreign_conversation", a, other.ID}} {
		t.Run(tc.name, func(t *testing.T) {
			if err := s.SetMessageContext(ctx, tc.user, tc.conversation, msg.ID, updated); !errors.Is(err, ErrNotFound) {
				t.Fatalf("err=%v", err)
			}
		})
	}
	mustStore(t, s.SetMessageContext(ctx, a, c.ID, msg.ID, updated))
	detail, err := s.GetConversation(ctx, a, c.ID)
	mustStore(t, err)
	if len(detail.Messages) != 1 || detail.Messages[0].ID != msg.ID || !strings.Contains(string(detail.Messages[0].Context), "新页面") {
		t.Fatalf("更新原消息失败：%+v", detail)
	}
	_, err = s.AppendMessage(ctx, a, c.ID, MessageInput{Role: "assistant", Content: json.RawMessage(`{"role":"assistant","content":[]}`), Context: updated})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("assistant 接受上下文：%v", err)
	}
}

func TestPostgreSQLStewardC5EmptyContextPersistsNullAndReplaysWithoutBlock(t *testing.T) {
	db, store, user, _ := runtimeDatabase(t)
	ctx := context.Background()
	for _, raw := range []string{`{}`, `{"page":null}`, `{"playback":null}`, `{"page":null,"playback":null}`} {
		t.Run(raw, func(t *testing.T) {
			normalized, err := NormalizeContext(json.RawMessage(raw))
			mustStore(t, err)
			if normalized != nil {
				t.Fatalf("空上下文没有归一为空：%s", normalized)
			}
			conversation, err := store.CreateConversation(ctx, user, "合成空上下文", "model")
			mustStore(t, err)
			content := json.RawMessage(`{"role":"user","content":[{"type":"text","text":"合成问题 <>&"}]}`)
			message, err := store.AppendMessage(ctx, user, conversation.ID, MessageInput{Role: "user", Content: content, Context: json.RawMessage(raw)})
			mustStore(t, err)
			assertNull := func() {
				t.Helper()
				var isNull bool
				mustStore(t, db.QueryRow(`SELECT context IS NULL FROM steward_messages WHERE id=$1`, message.ID).Scan(&isNull))
				if !isNull {
					t.Fatal("空上下文没有存为 SQL NULL")
				}
			}
			assertNull()
			mustStore(t, store.SetMessageContext(ctx, user, conversation.ID, message.ID, json.RawMessage(`{"page":{"kind":"other","title":"旧页面"}}`)))
			mustStore(t, store.SetMessageContext(ctx, user, conversation.ID, message.ID, json.RawMessage(raw)))
			assertNull()
			detail, err := store.GetConversation(ctx, user, conversation.ID)
			mustStore(t, err)
			if len(detail.Messages) != 1 || len(detail.Messages[0].Context) != 0 {
				t.Fatalf("读取空上下文错误：%+v", detail.Messages)
			}
			// 兼容历史 JSON 空对象；重放不能为它追加来源块。
			for _, contextValue := range []json.RawMessage{detail.Messages[0].Context, json.RawMessage(raw)} {
				replay := detail.Messages[0]
				replay.Context = contextValue
				messages, err := replayMessages([]Message{replay})
				mustStore(t, err)
				if len(messages) != 1 || len(messages[0].Content) != 1 || messages[0].Content[0].Text != "合成问题 <>&" {
					t.Fatalf("空上下文改变模型正文或产生上下文块：%+v", messages)
				}
			}
		})
	}
}
