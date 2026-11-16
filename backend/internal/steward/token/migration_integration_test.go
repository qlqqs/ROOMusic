package token

import (
	"context"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/migrations"
)

func TestPostgreSQLTokenMigration52ConstraintsAndRoundTrip(t *testing.T) {
	ctx := context.Background()
	db, err := postgres.Open(ctx, postgrestest.NewSchema(t, "token_migration_"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	up, err := migrations.Files.ReadFile("0052_steward_access_tokens.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := migrations.Files.ReadFile("0052_steward_access_tokens.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	initial, err := postgrestest.Snapshot(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	user := uuid.New()
	if _, err := db.Exec(`INSERT INTO users(id,username,password_hash,role) VALUES($1,'synthetic-migration','synthetic-only','user')`, user); err != nil {
		t.Fatal(err)
	}
	plaintext := Generate()
	id := uuid.New()
	insert := `INSERT INTO steward_access_tokens(id,user_id,name,scope,token_hash,token_tail) VALUES($1,$2,$3,$4,$5,$6)`
	if _, err := db.Exec(insert, id, user, "合成", "read", Hash(plaintext), plaintext[len(plaintext)-4:]); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name                         string
		user, nameValue, scope, tail string
		hash                         []byte
	}{
		{"empty-name", user, "", "read", "1234", Hash("empty")},
		{"bad-scope", user, "合成", "write", "1234", Hash("scope")},
		{"bad-hash", user, "合成", "read", "1234", []byte{1}},
		{"bad-tail", user, "合成", "read", "123", Hash("tail")},
		{"duplicate-hash", user, "合成", "read", "1234", Hash(plaintext)},
		{"missing-user", uuid.New(), "合成", "read", "1234", Hash("missing-user")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := db.Exec(insert, uuid.New(), tc.user, tc.nameValue, tc.scope, tc.hash, tc.tail); err == nil {
				t.Fatal("迁移约束未拒绝输入")
			}
		})
	}
	if _, err := db.Exec(`DELETE FROM users WHERE id=$1`, user); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM steward_access_tokens`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("级联删除：count=%d err=%v", count, err)
	}
	if _, err := db.Exec(string(down)); err != nil {
		t.Fatal(err)
	}
	var removed bool
	if err := db.QueryRow(`SELECT to_regclass('steward_access_tokens') IS NULL`).Scan(&removed); err != nil || !removed {
		t.Fatalf("down 未删除表：%v", err)
	}
	if _, err := db.Exec(string(up)); err != nil {
		t.Fatal(err)
	}
	after, err := postgrestest.Snapshot(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if diff := postgrestest.Diff(initial, after); len(diff) != 0 {
		t.Fatalf("0052 升降级结构或旧数据变化：%v", diff)
	}
}
