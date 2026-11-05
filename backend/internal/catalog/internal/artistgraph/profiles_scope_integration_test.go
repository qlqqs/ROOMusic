package artistgraph_test

import (
	"context"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/artistgraph"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestArtistGraphEmptyRefreshIsNoOp(t *testing.T) {
	// 空批不能读取代次或数据库，更不能隐式补齐无署名身份。
	if err := artistgraph.RefreshProfiles(context.Background(), nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := artistgraph.Refresh(context.Background(), nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestPostgreSQLArtistGraphRefreshOnlyOldAndNewParticipants(t *testing.T) {
	f := newGraphReviewFixture(t)
	// 模拟回填开始时的大量预置身份；在线刷新不能把它们捎带进同一 CATP 事务。
	f.exec(`INSERT INTO artists(id,initial_name) SELECT gen_random_uuid(),'待回填身份 '||i FROM generate_series(1,20000) i`)
	track := f.track("原署名", "present")
	f.refresh()
	old := f.text(`SELECT artist_id::text FROM artist_effective_credits WHERE track_id=$1::uuid`, track)
	if n := f.count(`SELECT count(*) FROM artist_profiles`); n != 1 {
		t.Fatalf("单发行刷新补齐了无关身份：%d", n)
	}
	if n := f.count(`SELECT count(*) FROM artist_search_names WHERE artist_id<>$1::uuid`, old); n != 0 {
		t.Fatalf("单发行刷新写入无关搜索名字：%d", n)
	}
	f.exec(`UPDATE catalog_projection_state SET target_generation=7 WHERE capability='artists'`)
	f.exec(`UPDATE tracks SET artist='新署名' WHERE id=$1::uuid`, track)
	f.refresh()
	current := f.text(`SELECT artist_id::text FROM artist_effective_credits WHERE track_id=$1::uuid`, track)
	if old == current {
		t.Fatal("署名变化没有新身份")
	}
	if n := f.count(`SELECT count(*) FROM artist_effective_credits WHERE artist_id=$1::uuid`, old); n != 0 {
		t.Fatal("旧身份仍有有效署名")
	}
	if n := f.count(`SELECT count(*) FROM artist_profiles WHERE artist_id=ANY($1::uuid[]) AND applied_generation=7`, []string{old, current}); n != 2 {
		t.Fatalf("失去最后署名的旧身份或新身份未刷新：%d", n)
	}
	if got := f.text(`SELECT auto_display_name FROM artist_profiles WHERE artist_id=$1::uuid`, old); got != "原署名" {
		t.Fatalf("无票旧身份回退错误：%q", got)
	}
	if n := f.count(`SELECT count(*) FROM artist_profiles`); n != 2 {
		t.Fatalf("后续刷新越过旧新参与身份范围：%d", n)
	}
	if n := f.count(`SELECT count(*) FROM artist_search_names WHERE projection_generation<>7`); n != 0 {
		t.Fatalf("旧新身份的搜索名字未与 profile 同代刷新：%d", n)
	}
	if n := f.count(`SELECT applied_generation FROM artist_projection_releases WHERE release_id=$1::uuid`, f.release); n != 7 {
		t.Fatalf("发行完成代次错误：%d", n)
	}
}

func TestPostgreSQLArtistGraphRefreshProfilesExplicitBatchAndGeneration(t *testing.T) {
	f := newGraphReviewFixture(t)
	first, second := uuid.New(), uuid.New()
	for _, id := range []string{first, second} {
		f.exec(`INSERT INTO artists(id,initial_name) VALUES($1::uuid,'无署名保留身份')`, id)
	}
	f.exec(`UPDATE catalog_projection_state SET target_generation=3 WHERE capability='artists'`)
	if err := artistgraph.RefreshProfiles(f.ctx, f.tx, []string{first, first}); err != nil {
		t.Fatal(err)
	}
	if n := f.count(`SELECT count(*) FROM artist_profiles`); n != 1 {
		t.Fatalf("显式批次刷新了其他身份或种子：%d", n)
	}
	if n := f.count(`SELECT applied_generation FROM artist_profiles WHERE artist_id=$1::uuid`, first); n != 3 {
		t.Fatalf("零署名身份没有完成标记：%d", n)
	}
	if got := f.text(`SELECT auto_display_name FROM artist_profiles WHERE artist_id=$1::uuid`, first); got != "无署名保留身份" {
		t.Fatalf("无名字身份未回退 initial_name：%q", got)
	}
	if n := f.count(`SELECT count(*) FROM artist_search_names WHERE artist_id=$1::uuid AND projection_generation=3`, first); n != 1 {
		t.Fatalf("零署名身份没有同代搜索主名：%d", n)
	}
	f.exec(`UPDATE catalog_projection_state SET target_generation=4 WHERE capability='artists'`)
	if err := artistgraph.RefreshProfiles(f.ctx, f.tx, []string{second}); err != nil {
		t.Fatal(err)
	}
	if n := f.count(`SELECT applied_generation FROM artist_profiles WHERE artist_id=$1::uuid`, first); n != 3 {
		t.Fatalf("显式下一批改写了上批：%d", n)
	}
	if err := artistgraph.RefreshProfiles(f.ctx, f.tx, []string{first}); err != nil {
		t.Fatal(err)
	}
	if n := f.count(`SELECT applied_generation FROM artist_profiles WHERE artist_id=$1::uuid`, first); n != 4 {
		t.Fatalf("已有 profile 未更新代次：%d", n)
	}
}

func TestPostgreSQLArtistGraphScopedNamesPreserveRoutes(t *testing.T) {
	f := newGraphReviewFixture(t)
	const seed = "a0000000-0000-4000-8000-000000000001"
	const key = "a1000000-0000-4000-8000-000000000002"
	source, target, scoped := uuid.New(), uuid.New(), uuid.New()
	for _, id := range []string{source, target, scoped} {
		f.exec(`INSERT INTO artists(id,initial_name) VALUES($1::uuid,'保留初始名')`, id)
	}
	f.exec(`INSERT INTO artist_name_bindings(key_id,artist_id,operation_id) VALUES($1::uuid,$2::uuid,$3::uuid)`, key, source, f.operation)
	f.exec(`INSERT INTO artist_overrides(artist_id,display_name,redirect_artist_id,name_operation_id,redirect_operation_id)
 VALUES($1::uuid,'合并来源主名',$2::uuid,$3::uuid,$3::uuid)`, source, target, f.operation)
	f.exec(`INSERT INTO artist_overrides(artist_id,display_name,name_operation_id) VALUES($1::uuid,'目标人工主名',$2::uuid)`, target, f.operation)
	track := f.track("Jay Chou", "present")
	f.exec(`INSERT INTO artist_assignment_overrides(track_id,key_id,artist_id,operation_id) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid)`, track, key, scoped, f.operation)
	f.refresh()
	ids := []string{seed, source, target, scoped}
	if err := artistgraph.RefreshProfiles(f.ctx, f.tx, ids); err != nil {
		t.Fatal(err)
	}
	// 用原全局归属定义作等价对照：绑定优先、重定向一跳、作用域可共享同一名字。
	if n := f.count(`WITH owned AS (
 SELECT COALESCE(o.redirect_artist_id,COALESCE(b.artist_id,k.initial_artist_id)) AS artist_id,n.id AS name_id
 FROM artist_names n JOIN artist_name_keys k ON k.id=n.key_id LEFT JOIN artist_name_bindings b ON b.key_id=k.id
 LEFT JOIN artist_overrides o ON o.artist_id=COALESCE(b.artist_id,k.initial_artist_id)
 UNION SELECT artist_id,name_id FROM artist_effective_credits
 ), expected AS (SELECT artist_id,name_id FROM owned WHERE artist_id=ANY($1::uuid[])), actual AS (
 SELECT artist_id,name_id FROM artist_search_names WHERE artist_id=ANY($1::uuid[]) AND name_id IS NOT NULL
 ) SELECT count(*) FROM ((SELECT artist_id,name_id FROM expected EXCEPT SELECT artist_id,name_id FROM actual)
 UNION ALL (SELECT artist_id,name_id FROM actual EXCEPT SELECT artist_id,name_id FROM expected)) difference`, ids); n != 0 {
		t.Fatalf("收窄归属改变了别名集合：%d", n)
	}
	for id, want := range map[string]string{seed: "周杰伦", source: "保留初始名", target: "Jay Chou", scoped: "Jay Chou"} {
		if got := f.text(`SELECT auto_display_name FROM artist_profiles WHERE artist_id=$1::uuid`, id); got != want {
			t.Fatalf("身份 %s 自动主名：%q，预期 %q", id, got, want)
		}
	}
	if got := f.text(`SELECT effective_display_name FROM artist_profiles WHERE artist_id=$1::uuid`, target); got != "目标人工主名" {
		t.Fatalf("人工主名丢失：%q", got)
	}
	if got := f.text(`SELECT original_value FROM artist_search_names WHERE artist_id=$1::uuid AND source_artist_id=$2::uuid`, target, source); got != "合并来源主名" {
		t.Fatalf("重定向来源人工别名丢失：%q", got)
	}
}
