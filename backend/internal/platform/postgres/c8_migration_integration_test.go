package postgres

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/migrations"
)

func TestPostgreSQLC8Migration56ConstraintsAndRoundTrip(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, postgrestest.NewSchema(t, "c8_migration_"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	up, err := migrations.Files.ReadFile("0056_steward_web.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := migrations.Files.ReadFile("0056_steward_web.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	initial, err := postgrestest.Snapshot(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	user, conversation := uuid.New(), uuid.New()
	if _, err = db.Exec(`INSERT INTO users(id,username,password_hash,role) VALUES($1,'c8-migration','synthetic','admin')`, user); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO steward_user_settings(user_id,protocol,search_source,search_key_ciphertext,search_key_last_four,overrides,probed_at,probe_result) VALUES($1,'responses','tavily','\x01020304','1234','{"protocol":"responses","native_search":"supported"}',now(),'{"protocol":"responses","native_search":"supported","responses_available":true,"failures":[{"protocol":"responses","step":"native_search"}]}')`, user); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO steward_conversations(id,user_id,title,model) VALUES($1,$2,'迁移合成对话','synthetic')`, conversation, user); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		role, blocks string
		ok           bool
	}{{"assistant", `{}`, true}, {"user", `{}`, false}, {"tool", `{}`, false}, {"assistant", `[]`, false}, {"assistant", `null`, false}} {
		t.Run(tc.role+tc.blocks, func(t *testing.T) {
			_, err := db.Exec(`INSERT INTO steward_messages(id,conversation_id,sequence,role,content,provider_blocks) VALUES($1,$2,1,$3,'{}',$4::jsonb)`, uuid.New(), conversation, tc.role, tc.blocks)
			if (err == nil) != tc.ok {
				t.Fatalf("约束错误=%v", err)
			}
			db.Exec(`DELETE FROM steward_messages WHERE conversation_id=$1`, conversation)
		})
	}
	for _, statement := range []string{`UPDATE steward_user_settings SET protocol='invalid'`, `UPDATE steward_user_settings SET search_source='invalid'`, `UPDATE steward_user_settings SET search_key_ciphertext=NULL`, `UPDATE steward_user_settings SET search_key_last_four='12345'`} {
		if _, err = db.Exec(statement); err == nil {
			t.Fatalf("约束接受了 %s", statement)
		}
	}
	if _, err = db.Exec(string(down)); err != nil {
		t.Fatal(err)
	}
	var protocol string
	var probe, overrides []byte
	if err = db.QueryRow(`SELECT protocol,probe_result,overrides FROM steward_user_settings WHERE user_id=$1`, user).Scan(&protocol, &probe, &overrides); err != nil {
		t.Fatal(err)
	}
	var p, o map[string]any
	json.Unmarshal(probe, &p)
	json.Unmarshal(overrides, &o)
	if protocol != "openai" || p["protocol"] != "openai" || p["native_search"] != nil || p["responses_available"] != nil || o["native_search"] != nil || len(p["failures"].([]any)) != 0 {
		t.Fatalf("回退数据=%s %s %s", protocol, probe, overrides)
	}
	if _, err = db.Exec(`UPDATE steward_user_settings SET protocol='responses'`); err == nil {
		t.Fatal("回退未恢复旧协议约束")
	}
	if _, err = db.Exec(string(up)); err != nil {
		t.Fatal(err)
	}
	var source string
	if err = db.QueryRow(`SELECT search_source,probe_result FROM steward_user_settings WHERE user_id=$1`, user).Scan(&source, &probe); err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(probe, &p)
	if source != "none" || p["native_search"] != "unknown" || p["responses_available"] != false {
		t.Fatalf("升级默认值=%s %s", source, probe)
	}
	db.Exec(`DELETE FROM users WHERE id=$1`, user)
	after, err := postgrestest.Snapshot(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if diff := postgrestest.Diff(initial, after); len(diff) != 0 {
		t.Fatalf("结构往返变化=%v", diff)
	}
}
