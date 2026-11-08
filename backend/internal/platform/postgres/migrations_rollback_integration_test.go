package postgres

import (
	"fmt"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/migrations"
)

// 新迁移若改变旧约束或枚举，必须提供新版数据，证明回退后不留新表数据。
var rollbackFixtures = map[int64]string{
	40: "", // 仅新增可空搜索派生列与索引，往返不需要新版取值。
	39: `UPDATE tracks SET work_title='Cello Suite, BWV 1007',work_catalog='BWV 1007',work_index=1,
 movement_number=1,movement_title='Prelude',work_source='tag' WHERE id='c3000000-0000-4000-8000-000000000035';
 INSERT INTO catalog_search_values(id,track_id,field,position,original_value,normalized_value,normalization_version,projection_generation,short_grams) VALUES
 ('c5000000-0000-4000-8000-000000000035','c3000000-0000-4000-8000-000000000035','work_title',1,'Cello Suite, BWV 1007','cello suite, bwv 1007',1,1,ARRAY['ce']);`,
	38: `INSERT INTO track_credits(track_id,role,name,detail,source,position) VALUES
 ('c3000000-0000-4000-8000-000000000034','lyricist','测试署名','libretto','lyrics',1),
 ('c3000000-0000-4000-8000-000000000034','arranger','测试署名',NULL,'tag',2),
 ('c3000000-0000-4000-8000-000000000034','performer','测试演奏者','violin','tag',4),
 ('c3000000-0000-4000-8000-000000000034','performer','测试演奏者','cello','lyrics',5);
 INSERT INTO artist_credits(id,track_id,role,name_id,position,layer) VALUES
 ('c4000000-0000-4000-8000-000000000034','c3000000-0000-4000-8000-000000000034','lyricist','a2000000-0000-4000-8000-000000000001',1,'scan'),
 ('c4000000-0000-4000-8000-000000000035','c3000000-0000-4000-8000-000000000034','arranger','a2000000-0000-4000-8000-000000000001',2,'scan');
 INSERT INTO artist_effective_credits(credit_id,track_id,role,name_id,artist_id,position)
 SELECT id,track_id,role,name_id,'a0000000-0000-4000-8000-000000000001',position
 FROM artist_credits WHERE track_id='c3000000-0000-4000-8000-000000000034';
 INSERT INTO artist_tag_evidence(track_id,role,tag_key,occurrence_index,raw_value,source_kind) VALUES
 ('c3000000-0000-4000-8000-000000000034','lyricist','LYRICIST',0,'测试署名','vorbis'),
 ('c3000000-0000-4000-8000-000000000034','arranger','ARRANGER',0,'测试署名','vorbis');
 INSERT INTO catalog_search_values(id,track_id,field,role,position,original_value,normalized_value,normalization_version,projection_generation,short_grams) VALUES
 ('c5000000-0000-4000-8000-000000000034','c3000000-0000-4000-8000-000000000034','credit','lyricist',1,'测试署名','测试署名',1,1,ARRAY['测试']),
 ('c5000000-0000-4000-8000-000000000035','c3000000-0000-4000-8000-000000000034','credit','arranger',2,'测试署名','测试署名',1,1,ARRAY['测试']);`,
	37: "",
	36: "",
	35: `UPDATE library_roots SET path='/rollback-relocated',revision=2
 WHERE id='f1000000-0000-4000-8000-000000000001';
 INSERT INTO library_root_operations(id,root_id,actor_id,operation_type,status,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id)
 VALUES('f3000000-0000-4000-8000-000000000004','f1000000-0000-4000-8000-000000000001',
 'f0000000-0000-4000-8000-000000000001','relocate','succeeded','回滚改路径','relocate-fixture',1,2,
 '{"path":"/rollback-filter","revision":1}','{"path":"/rollback-relocated","revision":2}','{}','rollback-relocate');`,
	34: `INSERT INTO library_root_exclusions(root_id,relative_path)
 VALUES('f1000000-0000-4000-8000-000000000001','测试子目录');
 INSERT INTO scan_held_sources(scan_run_id,root_id,kind,relative_path)
 VALUES('f2000000-0000-4000-8000-000000000001','f1000000-0000-4000-8000-000000000001','file','损坏.flac');
 UPDATE scan_runs SET skipped_files=1 WHERE id='f2000000-0000-4000-8000-000000000001';
 INSERT INTO library_root_operations(id,root_id,actor_id,operation_type,status,idempotency_key,fingerprint,result_revision,after_state,response,request_id)
 VALUES('f3000000-0000-4000-8000-000000000001','f1000000-0000-4000-8000-000000000001',
 'f0000000-0000-4000-8000-000000000001','exclude','succeeded','回滚排除','exclude-fixture',1,
 '{"relative_path":"测试子目录"}','{}','rollback-exclude'),
 ('f3000000-0000-4000-8000-000000000002','f1000000-0000-4000-8000-000000000001',
 'f0000000-0000-4000-8000-000000000001','unexclude','succeeded','回滚恢复','unexclude-fixture',1,
 '{"relative_path":"已移除子目录"}','{}','rollback-unexclude');`,
	33: "",
	// 0032 的非空列表转换由专门的数据迁移用例覆盖；结构往返不另加会截断的数据。
	32: "",
	31: `INSERT INTO artist_alias_signals(left_key_id,right_key_id,bracket_key_id,alias_part,release_id,alias_release_id,left_name,right_name)
 VALUES('a1000000-0000-4000-8000-000000000001','a1000000-0000-4000-8000-000000000002',
 'a1000000-0000-4000-8000-000000000001','inner','c0000000-0000-4000-8000-000000000001',
 'c0000000-0000-4000-8000-000000000001','测试（别名）','别名');`,
}

func migrationPrefix(available []migration, last int64) fstest.MapFS {
	source := fstest.MapFS{}
	for _, m := range available {
		if m.Version > last {
			continue
		}
		source[m.Name] = &fstest.MapFile{Data: m.SQL}
		if m.Down != nil {
			source[m.Down.Name] = &fstest.MapFile{Data: m.Down.SQL}
		}
	}
	return source
}
func rollbackNeedsFixture(before, after postgrestest.SchemaSnapshot) bool {
	for key, value := range before.Structure {
		next, ok := after.Structure[key]
		if (strings.HasPrefix(key, "constraint/") || strings.HasPrefix(key, "enum/")) && ok && value != next {
			return true
		}
	}
	return false
}
func TestPostgreSQLMigrationRollbackRoundTrip(t *testing.T) {
	available, err := loadMigrations(migrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	var reversible []migration
	for _, m := range available {
		if m.Version >= rollbackRequiredFromVersion && m.Down != nil {
			if m.Down.Irreversible {
				if m.Down.Reason == "" {
					t.Fatalf("不可回退迁移缺理由：%d", m.Version)
				}
			} else {
				reversible = append(reversible, m)
			}
		}
	}
	// 当前无可回退的真实迁移时注入示例；以后自动遍历发布集合，不允许空集伪通过。
	if len(reversible) == 0 {
		t.Log("嵌入集合暂无可回退迁移，注入测试 FS 验证往返框架")
		source := migrationPrefix(available, int64(len(available)))
		v := int64(len(available)) + 1
		source[fmt.Sprintf("%04d_roundtrip.sql", v)] = &fstest.MapFile{Data: []byte(`CREATE TABLE roundtrip_probe(id bigint PRIMARY KEY,note text); INSERT INTO roundtrip_probe VALUES(1,'合成种子'); ALTER TABLE users ADD COLUMN roundtrip_note text; CREATE INDEX roundtrip_index ON roundtrip_probe(note);`)}
		source[fmt.Sprintf("%04d_roundtrip.down.sql", v)] = &fstest.MapFile{Data: []byte(`DROP INDEX roundtrip_index; ALTER TABLE users DROP COLUMN roundtrip_note; DROP TABLE roundtrip_probe;`)}
		available, err = loadMigrations(source)
		if err != nil {
			t.Fatal(err)
		}
		reversible = append(reversible, available[len(available)-1])
	}
	for _, m := range reversible {
		t.Run(m.Name, func(t *testing.T) {
			db := openMigrationTestDatabase(t)
			// 每个候选独立建 schema，之前即使有不可回退迁移也不会妨碍后续测试。
			rollbackApply(t, db, migrationPrefix(available, m.Version-1))
			if m.Version == 31 {
				// 新信号引用升级前已有的发行，保证回退只需删除新版信号表。
				rollbackExec(t, db, `INSERT INTO release_groups(id) VALUES('c1000000-0000-4000-8000-000000000001');
 INSERT INTO releases(id,group_id,title,artist) VALUES('c0000000-0000-4000-8000-000000000001',
 'c1000000-0000-4000-8000-000000000001','迁移测试发行','测试艺人');`)
			}
			if m.Version == 38 {
				// 先存旧版依赖与无细分署名，回退须保留它们并删除新版重复细分。
				rollbackExec(t, db, `INSERT INTO library_roots(id,path) VALUES('c6000000-0000-4000-8000-000000000034','/synthetic/credit-rollback');
 INSERT INTO release_groups(id) VALUES('c1000000-0000-4000-8000-000000000034');
 INSERT INTO releases(id,group_id,title,artist) VALUES('c0000000-0000-4000-8000-000000000034',
 'c1000000-0000-4000-8000-000000000034','署名迁移测试发行','测试艺人');
 INSERT INTO media(id,release_id,position) VALUES('c2000000-0000-4000-8000-000000000034','c0000000-0000-4000-8000-000000000034',1);
 INSERT INTO tracks(id,medium_id,position,title,artist,source_root_id,relative_path,observed_at) VALUES
 ('c3000000-0000-4000-8000-000000000034','c2000000-0000-4000-8000-000000000034',1,'署名迁移测试曲目','测试艺人',
 'c6000000-0000-4000-8000-000000000034','synthetic-credit.flac',NOW());
 INSERT INTO track_credits(id,track_id,role,name,position) VALUES
 ('c7000000-0000-4000-8000-000000000034','c3000000-0000-4000-8000-000000000034','performer','测试演奏者',3);`)
			}
			if m.Version == 39 {
				// 作品附着于旧曲目；回退应只删作品字段与搜索值，不删除曲目本身。
				rollbackExec(t, db, `INSERT INTO library_roots(id,path) VALUES('c6000000-0000-4000-8000-000000000035','/synthetic/work-rollback');
 INSERT INTO release_groups(id) VALUES('c1000000-0000-4000-8000-000000000035');
 INSERT INTO releases(id,group_id,title,artist) VALUES('c0000000-0000-4000-8000-000000000035',
 'c1000000-0000-4000-8000-000000000035','作品迁移测试发行','测试艺人');
 INSERT INTO media(id,release_id,position) VALUES('c2000000-0000-4000-8000-000000000035','c0000000-0000-4000-8000-000000000035',1);
 INSERT INTO tracks(id,medium_id,position,title,artist,source_root_id,relative_path,observed_at) VALUES
 ('c3000000-0000-4000-8000-000000000035','c2000000-0000-4000-8000-000000000035',1,'作品迁移测试曲目','测试艺人',
 'c6000000-0000-4000-8000-000000000035','synthetic-work.flac',NOW());`)
			}
			if m.Version == 34 || m.Version == 35 {
				// 升级前保留旧类型收据；回退只能移除新类型，不能误删原目录、扫描或操作记录。
				rollbackExec(t, db, `INSERT INTO users(id,username,password_hash,role)
 VALUES('f0000000-0000-4000-8000-000000000001','rollback-filter-admin','test-only','admin');
 INSERT INTO library_roots(id,path) VALUES('f1000000-0000-4000-8000-000000000001','/rollback-filter');
 INSERT INTO scan_runs(id,status,started_at,finished_at)
 VALUES('f2000000-0000-4000-8000-000000000001','succeeded',NOW(),NOW());
 INSERT INTO library_root_operations(id,root_id,actor_id,operation_type,status,idempotency_key,fingerprint,result_revision,request_id)
 VALUES('f3000000-0000-4000-8000-000000000003','f1000000-0000-4000-8000-000000000001',
 'f0000000-0000-4000-8000-000000000001','create','succeeded','旧目录收据','create-fixture',1,'rollback-create');`)
			}
			s0 := rollbackSnapshot(t, db)
			rollbackApply(t, db, migrationPrefix(available, m.Version))
			s1 := rollbackSnapshot(t, db)
			fixture, ok := rollbackFixtures[m.Version]
			if rollbackNeedsFixture(s0, s1) && !ok {
				t.Fatalf("迁移 %d 改变旧约束或枚举，必须补 rollbackFixtures 的新取值数据", m.Version)
			}
			if fixture != "" {
				rollbackExec(t, db, fixture)
			}
			if m.Version == 35 {
				// 改路径是已提交业务状态，下行只删除新类型收据，不恢复旧路径或版本。
				// 以回退前的完整目录数据作期望，仍检查下行没有误改任何目录列。
				s0.Data["library_roots"] = rollbackSnapshot(t, db).Data["library_roots"]
			}
			rollbackApply(t, db, migrationPrefix(available, m.Version-1))
			s2 := rollbackSnapshot(t, db)
			if m.Version == 35 {
				var path string
				var revision int64
				err := db.QueryRow(`SELECT path,revision FROM library_roots WHERE id='f1000000-0000-4000-8000-000000000001'`).Scan(&path, &revision)
				if err != nil || path != "/rollback-relocated" || revision != 2 {
					t.Fatalf("0035 下行不应撤销已改路径或版本：path=%q revision=%d err=%v", path, revision, err)
				}
			}
			if diff := postgrestest.Diff(s0, s2); len(diff) > 0 {
				t.Fatalf("升级→回退不一致：\n%s", strings.Join(diff, "\n"))
			}
			rollbackApply(t, db, migrationPrefix(available, m.Version))
			s3 := rollbackSnapshot(t, db)
			// 再升级只比较结构和行数；种子 UUID、时间等合法非确定值不应误报。
			countsOnly := func(snapshot postgrestest.SchemaSnapshot) postgrestest.SchemaSnapshot {
				data := make(map[string]string, len(snapshot.Data))
				for table, value := range snapshot.Data {
					count, _, _ := strings.Cut(value, " md5=")
					data[table] = count
				}
				return postgrestest.SchemaSnapshot{Structure: snapshot.Structure, Data: data}
			}
			if diff := postgrestest.Diff(countsOnly(s1), countsOnly(s3)); len(diff) > 0 {
				t.Fatalf("再次升级结构/行数不一致：\n%s", strings.Join(diff, "\n"))
			}
		})
	}
}

func TestPostgreSQLSchemaSnapshotDetectsChanges(t *testing.T) {
	for _, tc := range []struct{ name, ddl, key string }{
		{"列", `ALTER TABLE rollback_probe ADD COLUMN leftover text`, "column/rollback_probe/leftover"},
		{"约束", `ALTER TABLE rollback_probe ADD CONSTRAINT extra_unique UNIQUE(value)`, "constraint/rollback_probe/extra_unique"},
		{"索引", `CREATE INDEX leftover_idx ON rollback_probe(value)`, "index/leftover_idx"},
		{"CHECK取值", `ALTER TABLE rollback_probe DROP CONSTRAINT value_check; ALTER TABLE rollback_probe ADD CONSTRAINT value_check CHECK(value IN ('原有数据','新版取值'))`, "constraint/rollback_probe/value_check"},
		{"数据", `UPDATE rollback_probe SET value='被更改'`, "数据 rollback_probe"},
		{"残留行", `INSERT INTO rollback_probe VALUES(2,'残留')`, "数据 rollback_probe"},
		{"视图", `CREATE VIEW leftover_view AS SELECT id FROM rollback_probe`, "view/leftover_view"},
		{"函数", `CREATE FUNCTION leftover_fn() RETURNS int LANGUAGE sql AS 'SELECT 1'`, "function/leftover_fn"},
		{"枚举", `CREATE TYPE leftover_enum AS ENUM ('a','b')`, "enum/leftover_enum"},
		{"域", `CREATE DOMAIN leftover_domain AS int CHECK(VALUE>0)`, "domain/leftover_domain"},
		{"序列", `CREATE SEQUENCE leftover_seq START 9 INCREMENT 2`, "sequence/leftover_seq"},
		{"触发器", `CREATE FUNCTION leftover_trigger_fn() RETURNS trigger LANGUAGE plpgsql AS 'BEGIN RETURN NEW; END'; CREATE TRIGGER leftover_trigger BEFORE INSERT ON rollback_probe FOR EACH ROW EXECUTE FUNCTION leftover_trigger_fn()`, "trigger/rollback_probe/leftover_trigger"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openMigrationTestDatabase(t)
			source := rollbackTestFS(30)
			rollbackApply(t, db, source)
			rollbackExec(t, db, `ALTER TABLE rollback_probe ADD CONSTRAINT value_check CHECK(value <> '禁止')`)
			before := rollbackSnapshot(t, db)
			rollbackExec(t, db, tc.ddl)
			after := rollbackSnapshot(t, db)
			diff := postgrestest.Diff(before, after)
			if !strings.Contains(strings.Join(diff, "\n"), tc.key) {
				t.Fatalf("快照漏检 %s：%v", tc.name, diff)
			}
			if tc.name == "CHECK取值" && !rollbackNeedsFixture(before, after) {
				t.Fatal("约束变化必须要求夹具")
			}
		})
	}
	t.Run("列序号和schema名称归一化", func(t *testing.T) {
		first, second := openMigrationTestDatabase(t), openMigrationTestDatabase(t)
		rollbackApply(t, first, rollbackTestFS(30))
		rollbackApply(t, second, rollbackTestFS(30))
		rollbackExec(t, second, `ALTER TABLE rollback_probe ADD COLUMN discarded int; ALTER TABLE rollback_probe DROP COLUMN discarded`)
		// 最后一列删除再添加，逻辑顺序不变但 attnum 真的改变。
		rollbackExec(t, second, `ALTER TABLE rollback_probe DROP COLUMN value; ALTER TABLE rollback_probe ADD COLUMN value text; UPDATE rollback_probe SET value='原有数据'`)
		a, b := rollbackSnapshot(t, first), rollbackSnapshot(t, second)
		if diff := postgrestest.Diff(a, b); len(diff) > 0 {
			t.Fatalf("物理列号/schema/applied_at 不应影响快照：%v", diff)
		}
		rollbackExec(t, second, `UPDATE schema_migrations SET applied_at=applied_at+interval '1 day'`)
		if !reflect.DeepEqual(b, rollbackSnapshot(t, second)) {
			t.Fatal("applied_at 未排除")
		}
	})
	t.Run("千行阈值", func(t *testing.T) {
		db := openMigrationTestDatabase(t)
		rollbackApply(t, db, rollbackTestFS(30))
		rollbackExec(t, db, `CREATE TABLE many_rows(id int); INSERT INTO many_rows SELECT generate_series(1,1001)`)
		before := rollbackSnapshot(t, db)
		if before.Data["many_rows"] != "rows=1001" {
			t.Fatalf("大表不应计算摘要：%v", before.Data)
		}
	})
}

func TestRollbackFixtureDetection(t *testing.T) {
	for _, key := range []string{"constraint/t/c", "enum/state"} {
		a := postgrestest.SchemaSnapshot{Structure: map[string]string{key: "old"}}
		b := postgrestest.SchemaSnapshot{Structure: map[string]string{key: "new"}}
		if !rollbackNeedsFixture(a, b) {
			t.Errorf("漏检：%s", key)
		}
	}
}

// 确认嵌入 FS 能被原样复制，不把 .down.sql 算进正向版本数。
func TestEmbeddedMigrationRollbackFiles(t *testing.T) {
	available, err := loadMigrations(migrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	copied := migrationPrefix(available, available[len(available)-1].Version)
	for name := range copied {
		raw, err := fs.ReadFile(migrations.Files, name)
		if err != nil || string(raw) != string(copied[name].Data) {
			t.Fatalf("复制改变迁移：%s %v", name, err)
		}
	}
}
