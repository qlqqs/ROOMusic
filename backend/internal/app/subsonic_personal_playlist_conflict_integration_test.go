package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/personal"
	"github.com/qlqq/roomusic/backend/internal/subsonic/protocol"
)

// 用 personal 的请求锁停住已读取歌单版本的请求，为每次竞争固定提交顺序。
const subsonicPlaylistReceiptLock = 0x504b4559

func subsonicPlaylistReceiptLockID(t *testing.T, user string, cmd personal.Command) int32 {
	t.Helper()
	data, err := json.Marshal(cmd)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(append([]byte("subsonic-playlist:"+user+":"), data...))
	key := fmt.Sprintf("%x-%x-%x-%x-%x", digest[:4], digest[4:6], digest[6:8], digest[8:10], digest[10:16])
	lockDigest := sha256.Sum256([]byte(user + ":" + key))
	return int32(binary.BigEndian.Uint32(lockDigest[:4]))
}

func holdSubsonicPlaylistReceipt(t *testing.T, ctx context.Context, db *sql.DB, lockID int32) *sql.Tx {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1::integer,$2::integer)`, subsonicPlaylistReceiptLock, lockID); err != nil {
		t.Fatal(err)
	}
	return tx
}

func waitSubsonicPlaylistReceipt(t *testing.T, ctx context.Context, db *sql.DB, lockID int32) {
	t.Helper()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		var waiting bool
		err := db.QueryRowContext(ctx, `SELECT EXISTS (
 SELECT 1 FROM pg_locks WHERE locktype='advisory' AND classid=$1::oid AND objid=$2::oid AND objsubid=2 AND NOT granted
)`, subsonicPlaylistReceiptLock, uint32(lockID)).Scan(&waiting)
		if err != nil {
			t.Fatalf("等待请求锁 %d：%v", lockID, err)
		}
		if waiting {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("请求未等待锁 %d：%v", lockID, ctx.Err())
		case <-tick.C:
		}
	}
}

func TestPostgreSQLSubsonicPersonalPlaylistRevisionConflicts(t *testing.T) {
	for _, tc := range []struct {
		name      string
		conflicts int
	}{{"RetryWithLatestOrder", 1}, {"PersistentConflict", 2}} {
		t.Run(tc.name, func(t *testing.T) {
			app := newSubsonicTestApplication(t)
			admin := setupSubsonicAdmin(t, app)
			f := newPersonalFixtureForUser(t, app, admin, 4)
			a, b, c, d := f.tracks[0], f.tracks[1], f.tracks[2], f.tracks[3]
			const original, renamed = "并发前的歌单", "并发修改后的歌单"
			p := f.create(original, f.tracks[:3])
			initial := webSubsonicPlaylistItems(t, f, p.ID)
			countReceipts := func() int {
				return countRows(t, app, `SELECT count(*) FROM personal_playlist_requests WHERE playlist_id=$1`, p.ID)
			}
			receipts := countReceipts()

			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			blockers := make([]*sql.Tx, 0, tc.conflicts)
			lockIDs := make([]int32, 0, tc.conflicts)
			for i, tracks := range [][]string{{a, c, d}, {b, a, d}}[:tc.conflicts] {
				lockID := subsonicPlaylistReceiptLockID(t, admin.id, personal.Command{
					Kind: "replace_items", PlaylistID: p.ID, Name: renamed, ExpectedRevision: p.Revision + int64(i), TrackIDs: tracks,
				})
				lockIDs = append(lockIDs, lockID)
				blockers = append(blockers, holdSubsonicPlaylistReceipt(t, ctx, app.database, lockID))
			}

			params := subsonicParameters(subsonicAdmin, subsonicPassword, "token")
			params.Set("playlistId", p.ID)
			params.Set("name", renamed)
			params.Set("songIndexToRemove", "1")
			params.Set("songIdToAdd", d)
			request := httptest.NewRequestWithContext(ctx, http.MethodGet, "/rest/updatePlaylist?"+params.Encode(), nil)
			response := httptest.NewRecorder()
			done := make(chan struct{})
			go func() {
				defer close(done)
				app.handler.ServeHTTP(response, request)
			}()
			defer func() {
				cancel()
				<-done
			}()

			var lastWebWrite personal.PlaylistItemsPage
			for i, order := range [][]string{{b, c, a}, {c, a, b}}[:tc.conflicts] {
				waitSubsonicPlaylistReceipt(t, ctx, app.database, lockIDs[i])
				path := playlistsURL + "/" + p.ID + "/items/" + initial.Items[i].ID + "/move"
				moved := f.call(http.MethodPost, path, map[string]any{"expected_revision": p.Revision + int64(i), "action": "last"})
				personalOK(t, moved, http.StatusOK)
				lastWebWrite = requireSubsonicWebPlaylist(t, f, p.ID, original, p.Revision+int64(i+1), order)
				if err := blockers[i].Commit(); err != nil {
					t.Fatalf("释放第 %d 次尝试的请求锁：%v", i+1, err)
				}
			}

			select {
			case <-done:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if tc.conflicts == 1 {
				requireSubsonicOK(t, response)
				// 网页先把 A 移到末尾，新顺序的第二首是 C。
				requireSubsonicWebPlaylist(t, f, p.ID, renamed, p.Revision+2, []string{b, a, d})
			} else {
				requireSubsonicFailure(t, response, http.StatusOK, protocol.General)
				after := webSubsonicPlaylistItems(t, f, p.ID)
				if !reflect.DeepEqual(after, lastWebWrite) {
					t.Fatalf("两次冲突后未保持最后一次网页写入的状态：%+v → %+v", lastWebWrite, after)
				}
			}
			if got := countReceipts(); got != receipts+2 {
				t.Fatalf("竞争结束后收据为 %d，期望 %d", got, receipts+2)
			}
		})
	}
}
