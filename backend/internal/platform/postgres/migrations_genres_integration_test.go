package postgres

import (
	"database/sql"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/migrations"
)

func TestPostgreSQLMigration0032GenreLists(t *testing.T) {
	db := openMigrationTestDatabase(t)
	available, err := loadMigrations(migrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	rollbackApply(t, db, migrationPrefix(available, 31))
	releaseID, groupID, actorID, operationID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	rollbackExec(t, db, `INSERT INTO release_groups(id) VALUES($1::uuid)`, groupID)
	rollbackExec(t, db, `INSERT INTO releases(id,group_id,title,artist,genre)
 VALUES($1::uuid,$2::uuid,'多流派专辑','测试艺人','国语流行乐')`, releaseID, groupID)
	rollbackExec(t, db, `INSERT INTO release_field_decisions(release_id,field_key,selected_value,selected_source,confidence,action,rule_id,candidates,reason)
 VALUES($1::uuid,'genre','"国语流行乐"','tag','low','uncertain_apply','majority_v1','["国语流行乐","台语流行乐"]','inconsistent_candidates')`, releaseID)
	rollbackExec(t, db, `INSERT INTO users(id,username,password_hash) VALUES($1::uuid,'genre-migration','test-hash')`, actorID)
	rollbackExec(t, db, `INSERT INTO operations(id,actor_id,operation_type,target_type,target_id,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id)
 VALUES($1::uuid,$2::uuid,'release_correction','release',$3::uuid,$1::uuid,'genre-migration',1,2,'{}','{}','{}','genre-migration')`, operationID, actorID, releaseID)
	rollbackExec(t, db, `INSERT INTO release_corrections(release_id,field,mode,text_value,operation_id)
 VALUES($1::uuid,'genre','set','古典',$2::uuid)`, releaseID, operationID)

	rollbackApply(t, db, migrationPrefix(available, 32))
	assertList := func(query string, want []string) {
		t.Helper()
		var raw string
		if err := db.QueryRow(query, releaseID).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var got []string
		if err := json.Unmarshal([]byte(raw), &got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s = %v，期望 %v", query, got, want)
		}
	}
	assertList(`SELECT to_jsonb(genres)::text FROM releases WHERE id=$1::uuid`, []string{"国语流行乐", "台语流行乐"})
	assertList(`SELECT to_jsonb(list_value)::text FROM release_corrections WHERE release_id=$1::uuid AND field='genre'`, []string{"古典"})
	assertList(`SELECT to_jsonb(genres)::text FROM release_effective WHERE id=$1::uuid`, []string{"古典"})
	var confidence, action, rule string
	var reason, oldText sql.NullString
	if err := db.QueryRow(`SELECT d.confidence,d.action,d.rule_id,d.reason,c.text_value
 FROM release_field_decisions d JOIN release_corrections c ON c.release_id=d.release_id AND c.field=d.field_key
 WHERE d.release_id=$1::uuid AND d.field_key='genre'`, releaseID).Scan(&confidence, &action, &rule, &reason, &oldText); err != nil {
		t.Fatal(err)
	}
	if confidence != "high" || action != "auto_apply" || rule != "genre_union_v1" || reason.Valid || oldText.Valid {
		t.Fatalf("升级后的决定或旧修正列不正确：confidence=%s action=%s rule=%s reason=%v text_value=%v", confidence, action, rule, reason, oldText)
	}

	rollbackExec(t, db, `UPDATE release_corrections SET list_value=ARRAY['爵士','流行'] WHERE release_id=$1::uuid AND field='genre'`, releaseID)
	rollbackApply(t, db, migrationPrefix(available, 31))
	var scanned, corrected, effective string
	if err := db.QueryRow(`SELECT r.genre,c.text_value,e.genre FROM releases r
 JOIN release_corrections c ON c.release_id=r.id AND c.field='genre'
 JOIN release_effective e ON e.id=r.id WHERE r.id=$1::uuid`, releaseID).Scan(&scanned, &corrected, &effective); err != nil {
		t.Fatal(err)
	}
	if scanned != "国语流行乐" || corrected != "爵士" || effective != "爵士" {
		t.Fatalf("回退应保留扫描列表和修正列表的首项：genre=%q text_value=%q effective=%q", scanned, corrected, effective)
	}
}
