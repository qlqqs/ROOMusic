package governance

import (
	"database/sql"
	"reflect"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/personal"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPostgreSQLRatingReleaseMergeAndRevert(t *testing.T) {
	f := newCorrectionFixture(t)
	source, target := f.visibleRelease("评分来源 A"), f.visibleRelease("评分目标 B")
	service := personal.NewService(f.database)

	seedTime := time.Date(2026, 11, 1, 12, 0, 0, 765432000, time.UTC)
	set := func(user, group string, rating int) {
		t.Helper()
		if _, err := service.SetRating(t.Context(), user, "album", group, rating); err != nil {
			t.Fatalf("设置专辑 %s 的评分：%v", group, err)
		}
	}
	capture := func(user, group string, rating int) *albumRatingSnapshot {
		t.Helper()
		got := readRating(t, f.database, user, group)
		if got == nil || got.rating != rating {
			t.Fatalf("用户 %s 在组 %s 的评分为 %+v，期望 %d 星", user, group, got, rating)
		}
		return got
	}
	check := func(stage, user, group string, want *albumRatingSnapshot) {
		t.Helper()
		got := readRating(t, f.database, user, group)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s：用户 %s 在组 %s 的评分为 %+v，期望 %+v", stage, user, group, got, want)
		}
	}

	users := []string{uuid.New(), uuid.New(), uuid.New()}
	sourceRatings := make([]*albumRatingSnapshot, len(users))
	for i, user := range users {
		f.exec(`INSERT INTO users(id,username,password_hash) VALUES($1,$1::uuid::text,'test-only')`, user)
		set(user, source.groupID, i+2)
		f.exec(`UPDATE rating_albums SET rated_at=$3 WHERE user_id=$1 AND group_id=$2`, user, source.groupID, seedTime)
		sourceRatings[i] = capture(user, source.groupID, i+2)
	}
	set(users[1], target.groupID, 5)
	targetBefore := capture(users[1], target.groupID, 5)

	op := f.mergeRelease(target.releaseID, source.releaseID, operations.TypeReleaseMerge)
	for i, user := range users {
		check("合并后来源", user, source.groupID, nil)
		want := sourceRatings[i]
		if i == 1 {
			want = targetBefore
		}
		check("合并后目标", user, target.groupID, want)
	}
	set(users[2], target.groupID, 1)
	modified := capture(users[2], target.groupID, 1)
	f.mustRevert(ReleaseMergeOperations(operations.TypeReleaseMerge, mergeTestSuppressions{}), op.OperationID)
	targetRatings := []*albumRatingSnapshot{nil, targetBefore, modified}
	for i, user := range users {
		check("撤销后来源", user, source.groupID, sourceRatings[i])
		check("撤销后目标", user, target.groupID, targetRatings[i])
	}
}

type albumRatingSnapshot struct {
	rating        int
	ratedAt       time.Time
	title, artist string
}

func readRating(t *testing.T, db *sql.DB, user, group string) *albumRatingSnapshot {
	t.Helper()
	var got albumRatingSnapshot
	err := db.QueryRowContext(t.Context(), `SELECT rating,rated_at,saved_title,saved_artist FROM rating_albums WHERE user_id=$1 AND group_id=$2`, user, group).
		Scan(&got.rating, &got.ratedAt, &got.title, &got.artist)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		t.Fatalf("查询用户 %s 在组 %s 的评分：%v", user, group, err)
	}
	got.ratedAt = got.ratedAt.UTC()
	return &got
}

func TestPostgreSQLRatingRevertLegacyReleaseMerge(t *testing.T) {
	f := newCorrectionFixture(t)
	source, target := f.visibleRelease("旧评分格式来源 A"), f.visibleRelease("旧评分格式目标 B")
	user := uuid.New()
	f.exec(`INSERT INTO users(id,username,password_hash) VALUES($1,$1::uuid::text,'test-only')`, user)
	service := personal.NewService(f.database)
	if _, err := service.SetFavorite(t.Context(), user, "album", source.groupID, true); err != nil {
		t.Fatal(err)
	}
	op := f.mergeRelease(target.releaseID, source.releaseID, operations.TypeReleaseMerge)
	var hasRatings bool
	if err := f.database.QueryRowContext(t.Context(), `SELECT before_state ? 'ratings' FROM operations WHERE id=$1`, op.OperationID).
		Scan(&hasRatings); err != nil || hasRatings {
		t.Fatalf("旧格式前态不应含 ratings，存在 %t，查询错误 %v", hasRatings, err)
	}

	if _, err := service.SetRating(t.Context(), user, "album", target.groupID, 5); err != nil {
		t.Fatal(err)
	}
	targetBefore := readRating(t, f.database, user, target.groupID)
	if targetBefore == nil {
		t.Fatal("撤销前缺少目标评分")
	}
	f.mustRevert(ReleaseMergeOperations(operations.TypeReleaseMerge, mergeTestSuppressions{}), op.OperationID)

	page, err := service.FavoriteAlbums(t.Context(), user, personal.Page{Number: 1, Size: 10})
	if err != nil || len(page.Items) != 1 || page.Items[0].Album.GroupID != source.groupID {
		t.Fatalf("撤销旧记录后收藏为 %+v，期望仅收藏来源组 %s，错误 %v", page, source.groupID, err)
	}
	if got := readRating(t, f.database, user, target.groupID); !reflect.DeepEqual(got, targetBefore) {
		t.Fatalf("撤销旧记录改动了目标评分，得到 %+v，期望 %+v", got, targetBefore)
	}
	if got := readRating(t, f.database, user, source.groupID); got != nil {
		t.Fatalf("旧记录撤销新增了来源评分 %+v", got)
	}
}
