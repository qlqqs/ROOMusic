package personal

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func playlistState(t *testing.T, f *personalFixture, user, id string) PlaylistState {
	t.Helper()
	p, err := scanPlaylist(f.database.QueryRow(`SELECT `+playlistColumns+` FROM personal_playlists WHERE user_id=$1::uuid AND id=$2::uuid`, user, id))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPostgreSQLPlaylistCanonicalReceiptsAllCommands(t *testing.T) {
	f := newPersonalFixture(t)
	ctx := context.Background()
	u := f.user(t)
	release := f.release(t, "稳定回执")
	ids := []string{f.track(t, release, "甲", 1), f.track(t, release, "乙", 1), f.track(t, release, "丙", 1)}
	f.setStatus(t, ids[2], "missing")
	type receipt struct {
		key     string
		command Command
		result  Result
	}
	receipts := []receipt{}
	run := func(c Command) Result {
		t.Helper()
		key := uuid.New()
		r, err := f.service.Execute(ctx, u, key, c)
		if err != nil {
			t.Fatalf("%s: %v", c.Kind, err)
		}
		receipts = append(receipts, receipt{key, c, r})
		return r
	}
	var added AddResult
	raw := run(Command{Kind: "create", Name: "  晚间  ", TrackIDs: []string{ids[0], ids[1], ids[1]}})
	if err := json.Unmarshal(raw.Body, &added); err != nil {
		t.Fatal(err)
	}
	p := added.Playlist
	refs := playlistRefs(t, f, u, p.ID)
	rename := run(Command{Kind: "rename", PlaylistID: p.ID, ExpectedRevision: p.Revision, Name: "  新名  "})
	var renamed RenameResult
	if err := json.Unmarshal(rename.Body, &renamed); err != nil {
		t.Fatal(err)
	}
	p = renamed.Playlist
	anchor := refs[0].ID
	moved := run(Command{Kind: "move_item", PlaylistID: p.ID, ItemID: refs[1].ID, ExpectedRevision: p.Revision, Action: "before", BeforeItemID: &anchor})
	var movement MoveResult
	if err := json.Unmarshal(moved.Body, &movement); err != nil {
		t.Fatal(err)
	}
	p = movement.Playlist
	add := run(Command{Kind: "add_items", PlaylistID: p.ID, ExpectedRevision: p.Revision, TrackIDs: []string{ids[2], ids[0], ids[2]}})
	if err := json.Unmarshal(add.Body, &added); err != nil {
		t.Fatal(err)
	}
	if added.AddedCount != 1 || added.ExistingCount != 1 || added.DuplicateCount != 1 {
		t.Fatalf("add counts: %+v", added)
	}
	p = added.Playlist
	remove := run(Command{Kind: "remove_item", PlaylistID: p.ID, ItemID: refs[0].ID, ExpectedRevision: p.Revision})
	var removed RemoveResult
	if err := json.Unmarshal(remove.Body, &removed); err != nil {
		t.Fatal(err)
	}
	restore := run(Command{Kind: "restore_item", PlaylistID: p.ID, Token: removed.Undo.Token})
	var restored RestoreResult
	if err := json.Unmarshal(restore.Body, &restored); err != nil {
		t.Fatal(err)
	}
	run(Command{Kind: "delete", PlaylistID: p.ID, ExpectedRevision: restored.Playlist.Revision})
	for _, r := range receipts {
		t.Run(r.command.Kind, func(t *testing.T) {
			normalized := r.command
			normalized.PlaylistID = strings.ToUpper(normalized.PlaylistID)
			normalized.ItemID = strings.ToUpper(normalized.ItemID)
			normalized.Token = strings.ToUpper(normalized.Token)
			normalized.Name = strings.TrimSpace(normalized.Name)
			normalized.TrackIDs = append([]string(nil), normalized.TrackIDs...)
			for i := range normalized.TrackIDs {
				normalized.TrackIDs[i] = strings.ToUpper(normalized.TrackIDs[i])
			}
			if normalized.BeforeItemID != nil {
				id := strings.ToUpper(*normalized.BeforeItemID)
				normalized.BeforeItemID = &id
			}
			got, err := f.service.Execute(ctx, strings.ToUpper(u), strings.ToUpper(r.key), normalized)
			if err != nil || got.Status != r.result.Status || !bytes.Equal(got.Body, r.result.Body) {
				t.Fatalf("normalized replay after deletion: %v", err)
			}
			different := r.command
			switch different.Kind {
			case "create":
				different.TrackIDs = []string{ids[1], ids[0], ids[1]}
			case "rename", "delete", "add_items":
				different.ExpectedRevision++
			case "remove_item", "move_item":
				different.ItemID = uuid.New()
			case "restore_item":
				different.Token = uuid.New()
			}
			if _, err = f.service.Execute(ctx, u, r.key, different); !errors.Is(err, ErrIdempotencyConflict) {
				t.Fatalf("different request: %v", err)
			}
			different = r.command
			if different.Kind == "create" {
				different.Kind = "rename"
				different.PlaylistID = p.ID
				different.ExpectedRevision = 1
			} else {
				different.PlaylistID = uuid.New()
			}
			if _, err = f.service.Execute(ctx, u, r.key, different); !errors.Is(err, ErrIdempotencyConflict) {
				t.Fatalf("command/route fingerprint: %v", err)
			}
		})
	}
	if f.count(t, `SELECT count(*) FROM personal_playlists WHERE user_id=$1`, u) != 0 || f.count(t, `SELECT count(*) FROM personal_playlist_requests WHERE user_id=$1`, u) != len(receipts) {
		t.Fatal("replay changed lifecycle")
	}
	key := uuid.New()
	a, err := f.service.Execute(ctx, u, key, Command{Kind: "create", Name: "空"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.service.Execute(ctx, u, key, Command{Kind: "create", Name: " 空 ", TrackIDs: []string{}})
	if err != nil || !bytes.Equal(a.Body, b.Body) {
		t.Fatalf("empty tracks normalization: %v", err)
	}
}

func TestPostgreSQLPlaylistNoopsAndScopedTargets(t *testing.T) {
	f := newPersonalFixture(t)
	ctx := context.Background()
	u, other := f.user(t), f.user(t)
	f.exec(t, `UPDATE users SET role='admin' WHERE id=$1`, other)
	r := f.release(t, "边界")
	ids := []string{f.track(t, r, "甲", 1), f.track(t, r, "乙", 1), f.track(t, r, "丙", 1), f.track(t, r, "丁", 1)}
	p := createPersonal(t, f, u, ids[:3]...).Playlist
	refs := playlistRefs(t, f, u, p.ID)
	commands := []Command{
		{Kind: "rename", Name: p.Name},
		{Kind: "add_items", TrackIDs: []string{ids[1], ids[1]}},
		{Kind: "move_item", ItemID: refs[0].ID, Action: "up"},
		{Kind: "move_item", ItemID: refs[0].ID, Action: "first"},
		{Kind: "move_item", ItemID: refs[2].ID, Action: "down"},
		{Kind: "move_item", ItemID: refs[2].ID, Action: "last"},
		{Kind: "move_item", ItemID: refs[1].ID, Action: "before", BeforeItemID: &refs[1].ID},
		{Kind: "move_item", ItemID: refs[0].ID, Action: "before", BeforeItemID: &refs[1].ID},
		{Kind: "move_item", ItemID: refs[2].ID, Action: "before"},
	}
	for _, c := range commands {
		c.PlaylistID, c.ExpectedRevision = p.ID, p.Revision
		result, err := f.service.Execute(ctx, u, uuid.New(), c)
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			Playlist PlaylistState `json:"playlist"`
			Changed  bool          `json:"changed"`
		}
		if err = json.Unmarshal(result.Body, &got); err != nil {
			t.Fatal(err)
		}
		if got.Changed || !reflect.DeepEqual(got.Playlist, p) || !reflect.DeepEqual(playlistRefs(t, f, u, p.ID), refs) {
			t.Fatalf("noop mutated state: %+v", c)
		}
	}
	executePersonal[RenameResult](t, f, u, Command{Kind: "rename", PlaylistID: p.ID, ExpectedRevision: 1, Name: "已更新"})
	for _, c := range commands {
		c.PlaylistID, c.ExpectedRevision = p.ID, 1
		if _, err := f.service.Execute(ctx, u, uuid.New(), c); !errors.Is(err, ErrRevisionConflict) {
			t.Fatalf("stale noop accepted: %+v: %v", c, err)
		}
	}
	p = playlistState(t, f, u, p.ID)
	for _, owner := range []string{u, other} {
		foreign := createPersonal(t, f, owner, ids[0]).Playlist
		foreignRef := playlistRefs(t, f, owner, foreign.ID)[0]
		removed := executePersonal[RemoveResult](t, f, owner, Command{Kind: "remove_item", PlaylistID: foreign.ID, ItemID: foreignRef.ID, ExpectedRevision: 1})
		executePersonal[RestoreResult](t, f, owner, Command{Kind: "restore_item", PlaylistID: foreign.ID, Token: removed.Undo.Token})
		for _, c := range []Command{
			{Kind: "remove_item", ItemID: foreignRef.ID},
			{Kind: "move_item", ItemID: foreignRef.ID, Action: "first"},
			{Kind: "move_item", ItemID: refs[0].ID, Action: "before", BeforeItemID: &foreignRef.ID},
			{Kind: "restore_item", Token: removed.Undo.Token},
			{Kind: "restore_item", Token: uuid.New()},
		} {
			c.PlaylistID, c.ExpectedRevision = p.ID, p.Revision
			key := uuid.New()
			if _, err := f.service.Execute(ctx, u, key, c); !errors.Is(err, ErrNotFound) {
				t.Fatalf("foreign target accepted: %+v: %v", c, err)
			}
			if f.count(t, `SELECT count(*) FROM personal_playlist_requests WHERE user_id=$1 AND idempotency_key=$2`, u, key) != 0 {
				t.Fatal("failed request consumed key")
			}
		}
	}
	for _, kind := range []string{"rename", "delete", "add_items", "remove_item", "restore_item", "move_item"} {
		c := Command{Kind: kind, PlaylistID: p.ID, ExpectedRevision: p.Revision, Name: "越权", TrackIDs: ids[:1], ItemID: refs[0].ID, Token: uuid.New(), Action: "first"}
		if _, err := f.service.Execute(ctx, other, uuid.New(), c); !errors.Is(err, ErrNotFound) {
			t.Fatalf("admin %s bypass: %v", kind, err)
		}
	}
	// 真正未知的批量输入整体回滚；拒绝请求不占用幂等键。
	key := uuid.New()
	c := Command{Kind: "add_items", PlaylistID: p.ID, ExpectedRevision: p.Revision, TrackIDs: []string{ids[3], uuid.New()}}
	if _, err := f.service.Execute(ctx, u, key, c); !errors.Is(err, ErrNotFound) {
		t.Fatalf("partial add: %v", err)
	}
	if !reflect.DeepEqual(playlistRefs(t, f, u, p.ID), refs) || !reflect.DeepEqual(playlistState(t, f, u, p.ID), p) {
		t.Fatal("failed batch changed state")
	}
	c.TrackIDs = ids[3:]
	if _, err := f.service.Execute(ctx, u, key, c); err != nil {
		t.Fatalf("failed key not reusable: %v", err)
	}
}

// 写收据失败必须回滚业务行、内部重排和撤销凭证，不能只测事务开始前的校验错误。
func TestPostgreSQLPlaylistReceiptFailureRollsBackAllCommands(t *testing.T) {
	f := newPersonalFixture(t)
	ctx := context.Background()
	u := f.user(t)
	r := f.release(t, "回滚")
	ids := []string{f.track(t, r, "甲", 1), f.track(t, r, "乙", 1), f.track(t, r, "丙", 1), f.track(t, r, "丁", 1)}
	for _, kind := range []string{"create", "rename", "delete", "add_items", "remove_item", "move_item", "restore_item"} {
		t.Run(kind, func(t *testing.T) {
			p := createPersonal(t, f, u, ids[:3]...).Playlist
			refs := playlistRefs(t, f, u, p.ID)
			c := Command{Kind: kind, PlaylistID: p.ID, ExpectedRevision: p.Revision}
			switch kind {
			case "create":
				c = Command{Kind: kind, Name: "新建", TrackIDs: ids}
			case "rename":
				c.Name = "改名"
			case "add_items":
				c.TrackIDs = ids[3:]
			case "remove_item":
				c.ItemID = refs[1].ID
			case "move_item":
				c.ItemID = refs[2].ID
				c.Action = "before"
				c.BeforeItemID = &refs[1].ID
			case "restore_item":
				removed := executePersonal[RemoveResult](t, f, u, Command{Kind: "remove_item", PlaylistID: p.ID, ItemID: refs[1].ID, ExpectedRevision: p.Revision})
				c.Token = removed.Undo.Token
				p = executePersonal[RenameResult](t, f, u, Command{Kind: "rename", PlaylistID: p.ID, ExpectedRevision: removed.Playlist.Revision, Name: "已修改"}).Playlist
			}
			if kind == "move_item" || kind == "restore_item" {
				f.exec(t, `WITH x AS (SELECT id,row_number() OVER(ORDER BY order_key) n FROM personal_playlist_items WHERE playlist_id=$1) UPDATE personal_playlist_items i SET order_key=x.n FROM x WHERE i.id=x.id`, p.ID)
			}
			if kind == "add_items" {
				f.exec(t, `UPDATE personal_playlist_items SET order_key=$2 WHERE id=$1`, refs[2].ID, int64(math.MaxInt64))
			}
			before := playlistWriteSnapshot(t, f, u)
			key := uuid.New()
			// UUID 来自测试内部，不含任何外部输入；只改变本测试隔离 schema。
			f.exec(t, fmt.Sprintf(`ALTER TABLE personal_playlist_requests ADD CONSTRAINT reject_test_receipt CHECK(idempotency_key<>'%s'::uuid)`, key))
			_, err := f.service.Execute(ctx, u, key, c)
			requirePlaylistSQLState(t, err, "23514")
			if after := playlistWriteSnapshot(t, f, u); !bytes.Equal(before, after) {
				t.Fatalf("%s leaked partial state", kind)
			}
			f.exec(t, `ALTER TABLE personal_playlist_requests DROP CONSTRAINT reject_test_receipt`)
			result, err := f.service.Execute(ctx, u, key, c)
			if err != nil {
				t.Fatalf("same-key retry after rollback: %v", err)
			}
			again, err := f.service.Execute(ctx, u, key, c)
			if err != nil || !bytes.Equal(result.Body, again.Body) || result.Status != again.Status {
				t.Fatal("retry receipt changed")
			}
		})
	}
}

func playlistWriteSnapshot(t *testing.T, f *personalFixture, user string) []byte {
	t.Helper()
	var snapshot []byte
	err := f.database.QueryRow(`SELECT jsonb_build_object(
 'playlists',(SELECT coalesce(jsonb_agg(to_jsonb(p) ORDER BY id),'[]') FROM personal_playlists p WHERE user_id=$1),
 'items',(SELECT coalesce(jsonb_agg(to_jsonb(i) ORDER BY id),'[]') FROM personal_playlist_items i WHERE user_id=$1),
 'undo',(SELECT coalesce(jsonb_agg(to_jsonb(u) ORDER BY token),'[]') FROM personal_playlist_undo u WHERE user_id=$1),
 'receipts',(SELECT coalesce(jsonb_agg(to_jsonb(r) ORDER BY idempotency_key),'[]') FROM personal_playlist_requests r WHERE user_id=$1))`, user).Scan(&snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestPostgreSQLPlaylistUndoFirstItemAndConsumedToken(t *testing.T) {
	f := newPersonalFixture(t)
	ctx := context.Background()
	u := f.user(t)
	r := f.release(t, "凭证")
	ids := []string{f.track(t, r, "甲", 1), f.track(t, r, "乙", 1), f.track(t, r, "丙", 1)}
	p := createPersonal(t, f, u, ids...).Playlist
	refs := playlistRefs(t, f, u, p.ID)
	removed := executePersonal[RemoveResult](t, f, u, Command{Kind: "remove_item", PlaylistID: p.ID, ItemID: refs[0].ID, ExpectedRevision: p.Revision})
	p = executePersonal[MoveResult](t, f, u, Command{Kind: "move_item", PlaylistID: p.ID, ItemID: refs[2].ID, ExpectedRevision: removed.Playlist.Revision, Action: "first"}).Playlist
	key := uuid.New()
	command := Command{Kind: "restore_item", PlaylistID: p.ID, Token: removed.Undo.Token}
	first, err := f.service.Execute(ctx, u, key, command)
	if err != nil {
		t.Fatal(err)
	}
	var restored RestoreResult
	if err = json.Unmarshal(first.Body, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Placement != "original_index" || restored.Item.Position != 1 || restored.Item.ID != refs[0].ID || !restored.Item.AddedAt.Equal(refs[0].AddedAt) {
		t.Fatalf("first-item restore: %+v", restored)
	}
	later := executePersonal[RemoveResult](t, f, u, Command{Kind: "remove_item", PlaylistID: p.ID, ItemID: restored.Item.ID, ExpectedRevision: restored.Playlist.Revision})
	before := playlistRefs(t, f, u, p.ID)
	for _, retryKey := range []string{key, uuid.New()} {
		replay, err := f.service.Execute(ctx, u, retryKey, command)
		if err != nil || replay.Status != first.Status || !bytes.Equal(replay.Body, first.Body) {
			t.Fatalf("consumed token replay: %v", err)
		}
	}
	if !reflect.DeepEqual(before, playlistRefs(t, f, u, p.ID)) || !reflect.DeepEqual(playlistState(t, f, u, p.ID), later.Playlist) {
		t.Fatal("old token resurrected a later removal")
	}
	executePersonal[RestoreResult](t, f, u, Command{Kind: "restore_item", PlaylistID: p.ID, Token: later.Undo.Token})
}

func TestPostgreSQLPlaylistUndoReferenceConstraints(t *testing.T) {
	f := newPersonalFixture(t)
	u, v := f.user(t), f.user(t)
	r := f.release(t, "引用")
	a, b, c := f.track(t, r, "前驱", 1), f.track(t, r, "移除", 1), f.track(t, r, "新项", 1)
	p := createPersonal(t, f, u, a, b).Playlist
	refs := playlistRefs(t, f, u, p.ID)
	removed := executePersonal[RemoveResult](t, f, u, Command{Kind: "remove_item", PlaylistID: p.ID, ItemID: refs[1].ID, ExpectedRevision: p.Revision})
	// 仅保留原前驱在 undo 中的引用，排除 items/收藏遮蔽 RESTRICT 测试。
	f.exec(t, `DELETE FROM personal_playlist_items WHERE id=$1`, refs[0].ID)
	f.exec(t, `UPDATE personal_playlists SET track_count=0 WHERE id=$1`, p.ID)
	for _, id := range []string{a, b} {
		_, err := f.database.Exec(`DELETE FROM tracks WHERE id=$1`, id)
		requirePlaylistSQLState(t, err, "23001")
	}
	_, err := f.database.Exec(`UPDATE personal_playlist_undo SET user_id=$1 WHERE token=$2`, v, removed.Undo.Token)
	requirePlaylistSQLState(t, err, "23503")
	otherKey := uuid.New()
	if _, err = f.service.Execute(context.Background(), v, otherKey, Command{Kind: "create", Name: "别人"}); err != nil {
		t.Fatal(err)
	}
	// 可延迟的恢复收据外键也必须包含 owner，且在提交时生效。
	tx, err := f.database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE personal_playlist_undo SET restore_request_key=$1 WHERE token=$2`, otherKey, removed.Undo.Token); err != nil {
		t.Fatal(err)
	}
	requirePlaylistSQLState(t, tx.Commit(), "23503")
	if f.count(t, `SELECT count(*) FROM personal_playlist_undo WHERE token=$1 AND restore_request_key IS NULL`, removed.Undo.Token) != 1 {
		t.Fatal("invalid FK persisted")
	}
	// 排序唯一约束独立于曲目唯一约束，不能用同一首曲目掩盖遗漏的排序约束。
	f.exec(t, `INSERT INTO personal_playlist_items(id,user_id,playlist_id,track_id,order_key) VALUES(gen_random_uuid(),$1,$2,$3,1)`, u, p.ID, a)
	_, err = f.database.Exec(`INSERT INTO personal_playlist_items(id,user_id,playlist_id,track_id,order_key) VALUES(gen_random_uuid(),$1,$2,$3,1)`, u, p.ID, c)
	requirePlaylistSQLState(t, err, "23505")
	f.exec(t, `UPDATE personal_playlists SET track_count=1 WHERE id=$1`, p.ID)
	var absent sql.NullString
	if err = f.database.QueryRow(`SELECT restore_request_key::text FROM personal_playlist_undo WHERE token=$1`, removed.Undo.Token).Scan(&absent); err != nil || absent.Valid {
		t.Fatal("failed restore consumed token")
	}
	executePersonal[struct{}](t, f, u, Command{Kind: "delete", PlaylistID: p.ID, ExpectedRevision: removed.Playlist.Revision})
	for _, table := range []string{"personal_playlist_items", "personal_playlist_undo"} {
		if f.count(t, `SELECT count(*) FROM `+table+` WHERE playlist_id=$1`, p.ID) != 0 {
			t.Fatalf("delete did not cascade %s", table)
		}
	}
	// 歌单与 undo 级联清除后，目录身份再无这些引用。
	f.exec(t, `DELETE FROM tracks WHERE id=ANY($1::text[]::uuid[])`, []string{a, b, c})
	if f.count(t, `SELECT count(*) FROM personal_playlist_requests WHERE user_id=$1 AND playlist_id=$2`, u, p.ID) != 3 {
		t.Fatal("playlist deletion removed receipts")
	}
}

func TestPostgreSQLPlaylistSingleItemNoopsAndRevisionExhaustionRollback(t *testing.T) {
	f := newPersonalFixture(t)
	ctx := context.Background()
	u := f.user(t)
	r := f.release(t, "安全整数")
	a, b := f.track(t, r, "甲", 1), f.track(t, r, "乙", 1)
	p := createPersonal(t, f, u, a).Playlist
	ref := playlistRefs(t, f, u, p.ID)[0]
	for _, action := range []string{"up", "down", "first", "last", "before"} {
		got := executePersonal[MoveResult](t, f, u, Command{Kind: "move_item", PlaylistID: p.ID, ItemID: ref.ID, ExpectedRevision: p.Revision, Action: action})
		if got.Changed || !reflect.DeepEqual(got.Playlist, p) || got.Moved.Position != 1 {
			t.Fatalf("single-item noop: %+v", got)
		}
	}
	removed := executePersonal[RemoveResult](t, f, u, Command{Kind: "remove_item", PlaylistID: p.ID, ItemID: ref.ID, ExpectedRevision: p.Revision})
	if _, err := f.service.Execute(ctx, u, uuid.New(), Command{Kind: "move_item", PlaylistID: p.ID, ItemID: ref.ID, ExpectedRevision: removed.Playlist.Revision, Action: "first"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty playlist move: %v", err)
	}
	// 到达安全整数上限时恢复仍是基础设施错误；凭证与空歌单必须完整回滚。
	f.exec(t, `UPDATE personal_playlists SET revision=$2 WHERE id=$1`, p.ID, MaxRevision)
	key := uuid.New()
	restore := Command{Kind: "restore_item", PlaylistID: p.ID, Token: removed.Undo.Token}
	before := playlistWriteSnapshot(t, f, u)
	if _, err := f.service.Execute(ctx, u, key, restore); err == nil || errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("restore revision exhaustion: %v", err)
	}
	if !bytes.Equal(before, playlistWriteSnapshot(t, f, u)) {
		t.Fatal("exhausted restore left item or consumed token")
	}
	f.exec(t, `UPDATE personal_playlists SET revision=$2 WHERE id=$1`, p.ID, MaxRevision-1)
	if _, err := f.service.Execute(ctx, u, key, restore); err != nil {
		t.Fatal(err)
	}
	p = playlistState(t, f, u, p.ID)
	for _, c := range []Command{
		{Kind: "rename", Name: "新名"},
		{Kind: "add_items", TrackIDs: []string{b}},
		{Kind: "remove_item", ItemID: ref.ID},
	} {
		c.PlaylistID, c.ExpectedRevision = p.ID, p.Revision
		before = playlistWriteSnapshot(t, f, u)
		if _, err := f.service.Execute(ctx, u, uuid.New(), c); err == nil {
			t.Fatalf("exhausted %s accepted", c.Kind)
		}
		if !bytes.Equal(before, playlistWriteSnapshot(t, f, u)) {
			t.Fatalf("exhausted %s partially persisted", c.Kind)
		}
	}
	// 不改变状态的有效命令不需要额外 revision，不能因已到上限误拒绝。
	noChange := executePersonal[RenameResult](t, f, u, Command{Kind: "rename", PlaylistID: p.ID, ExpectedRevision: p.Revision, Name: p.Name})
	if noChange.Changed || !reflect.DeepEqual(noChange.Playlist, p) {
		t.Fatal("exhausted rename noop changed state")
	}
	duplicate := executePersonal[AddResult](t, f, u, Command{Kind: "add_items", PlaylistID: p.ID, ExpectedRevision: p.Revision, TrackIDs: []string{a, a}})
	if duplicate.AddedCount != 0 || duplicate.ExistingCount != 1 || duplicate.DuplicateCount != 1 || !reflect.DeepEqual(duplicate.Playlist, p) {
		t.Fatal("exhausted add noop changed state")
	}
}
