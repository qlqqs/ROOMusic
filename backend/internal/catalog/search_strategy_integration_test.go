package catalog

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
)

// 同一实体有超过策略探测阈值的重复命中：探测只能选执行路径，不能截掉后续实体。
// 三个等级都触发密集分支，并跨页验证完整标题（包括空标题和相同128字符前缀）。
func TestPostgreSQLSearchDenseTiersDoNotCapCandidates(t *testing.T) {
	f := newPlacementFixture(t)
	prefix := strings.Repeat("长", 130)
	titles := []string{"A only", "B only", "C only", "", prefix + "b", prefix + "a"}
	tracks, groups := []string{}, []string{}
	for i, title := range titles {
		release := f.release(title)
		id := f.track(release, title, "present", nil)
		tracks, groups = append(tracks, id), append(groups, release.groupID)
		if i < 3 {
			name := []string{"Tone", "Tone suffix", "inside tone"}[i]
			f.exec(`INSERT INTO track_credits(track_id,role,name,position)
 SELECT $1::uuid,'composer',chr(8192+n%11)||chr(8192+(n/11)%11)||chr(8192+(n/121)%11)||chr(8192+(n/1331)%11)||$2,n
 FROM generate_series(1,$3::integer)n`, id, name, rawHitProbeLimit+1)
		} else {
			f.exec(`UPDATE tracks SET artist='Tone' WHERE id=$1::uuid`, id)
		}
	}
	missing := f.release("Tone")
	f.track(missing, "Tone", "missing", nil)
	publishSearchFixture(t, f)
	postgrestest.AnalyzeSchema(t, context.Background(), f.database)
	order := []int{3, 0, 5, 4, 1, 2}
	for _, kind := range []string{"albums", "tracks"} {
		want := make([]string, len(order))
		for i, source := range order {
			want[i] = groups[source]
			if kind == "tracks" {
				want[i] = tracks[source]
			}
		}
		all := []string{}
		for page := 1; page <= 4; page++ {
			got, err := f.store.Search(context.Background(), SearchParams{Query: "tone", Type: kind, Page: page, PageSize: 2})
			if err != nil {
				t.Fatal(err)
			}
			if got.Pagination.Total != 6 || got.Pagination.TotalRelation != "eq" || got.Pagination.HasMore != (page < 3) {
				t.Fatalf("%s/%d 分页：%+v", kind, page, got.Pagination)
			}
			if kind == "tracks" {
				for _, item := range got.Items.([]SearchTrackItem) {
					all = append(all, item.TrackID)
				}
			} else {
				for _, item := range got.Items.([]SearchAlbumItem) {
					all = append(all, item.Group.ID)
				}
			}
		}
		if !reflect.DeepEqual(all, want) {
			t.Fatalf("%s 排序或候选丢失：%v，期望%v", kind, all, want)
		}
	}
}

func TestPostgreSQLSearchPrefiltersUseDatabaseCharacterClasses(t *testing.T) {
	f := newPlacementFixture(t)
	publishSearchFixture(t, f)
	for _, tc := range []struct {
		query   string
		trigram bool
	}{
		{"交响", false}, {"爱", false}, {"%_~", false}, {"a b", false}, {"交响曲", true}, {"Ｂｅｙｏｎｃé", true}, {"a", false},
	} {
		_, err := searchSnapshot(context.Background(), f.store, func(s *Store, state CatalogState, generation int64) (bool, error) {
			plan, err := s.prepareSearchPlan(context.Background(), newSearchPlan(tc.query, "tracks", state.SearchMode, generation))
			if err != nil {
				return false, err
			}
			fallback := strings.Contains(plan.matchPredicate, `(v.normalized_value || '') LIKE '%' || $2`)
			if fallback == tc.trigram {
				t.Errorf("%q 的预筛策略错误：%s", tc.query, plan.matchPredicate)
			}
			return true, nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// 密集路径必须按当前代表标题排组，而不是命中成员或发行ID；默认版本/归组变化无需重写搜索值。
func TestPostgreSQLSearchDenseGroupsUseCurrentRepresentative(t *testing.T) {
	f := newPlacementFixture(t)
	roots, members := []placementRelease{}, []placementRelease{}
	for i, title := range []string{"Z representative", "A representative", "A representative"} {
		root := f.release(title)
		f.track(root, "unmatched root", "present", nil)
		member := f.release("0 member")
		track := f.track(member, "unmatched member", "present", nil)
		f.exec(`UPDATE releases SET group_id=$1::uuid,year=2020 WHERE id=$2::uuid`, root.groupID, member.releaseID)
		f.exec(`UPDATE releases SET year=2000 WHERE id=$1::uuid`, root.releaseID)
		if i == 0 {
			f.exec(`UPDATE releases SET year=2040 WHERE id=$1::uuid`, root.releaseID)
			f.exec(`UPDATE release_groups SET default_release_id=$1::uuid WHERE id=$2::uuid`, root.releaseID, root.groupID)
			f.exec(`INSERT INTO track_credits(track_id,role,name,position)
 SELECT $1::uuid,'composer',chr(8192+n%11)||chr(8192+(n/11)%11)||chr(8192+(n/121)%11)||chr(8192+(n/1331)%11)||'Tone',n
 FROM generate_series(1,$2::integer)n`, track, rawHitProbeLimit+1)
		} else {
			f.exec(`UPDATE tracks SET artist='Tone' WHERE id=$1::uuid`, track)
		}
		roots, members = append(roots, root), append(members, member)
	}
	// 固定同标题两组的group ID顺序与代表release ID顺序相反，避免随机UUID让错误排序偶然通过。
	low, high := "00000000-0000-4000-8000-000000000001", "ffffffff-ffff-4fff-8fff-ffffffffffff"
	groupOrder := []string{low, high}
	if roots[1].releaseID < roots[2].releaseID {
		groupOrder = []string{high, low}
	}
	for i, group := range groupOrder {
		f.exec(`INSERT INTO release_groups(id) VALUES($1::uuid)`, group)
		f.exec(`UPDATE releases SET group_id=$1::uuid WHERE group_id=$2::uuid`, group, roots[i+1].groupID)
		roots[i+1].groupID = group
	}
	missing := f.release("! invisible default")
	f.track(missing, "Tone", "missing", nil)
	f.exec(`UPDATE releases SET group_id=$1::uuid,year=1900 WHERE id=$2::uuid`, roots[1].groupID, missing.releaseID)
	f.exec(`UPDATE release_groups SET default_release_id=$1::uuid WHERE id=$2::uuid`, missing.releaseID, roots[1].groupID)
	publishSearchFixture(t, f)
	postgrestest.AnalyzeSchema(t, context.Background(), f.database)
	check := func(want []string, representatives map[string]string) {
		t.Helper()
		actual := []string{}
		for page := 1; page <= len(want)+1; page++ {
			got, err := f.store.Search(context.Background(), SearchParams{Query: "tone", Type: "albums", Page: page, PageSize: 1})
			if err != nil {
				t.Fatal(err)
			}
			if got.Pagination.Total != int64(len(want)) || got.Pagination.HasMore != (page < len(want)) {
				t.Fatalf("第%d页分页：%+v", page, got.Pagination)
			}
			for _, item := range got.Items.([]SearchAlbumItem) {
				if item.Group.Representative.ID != representatives[item.Group.ID] || item.Match.Kind != "exact" || item.MatchedReleaseID == item.Group.Representative.ID {
					t.Fatalf("当前代表/实际命中成员不符：%+v", item)
				}
				actual = append(actual, item.Group.ID)
			}
		}
		if !reflect.DeepEqual(actual, want) {
			t.Fatalf("密集组排序=%v，期望%v", actual, want)
		}
	}
	tied := []string{roots[1].groupID, roots[2].groupID}
	sort.Strings(tied)
	representatives := map[string]string{}
	for _, r := range roots {
		representatives[r.groupID] = r.releaseID
	}
	check(append(append([]string{}, tied...), roots[0].groupID), representatives)
	// 命中成员移到另一组后，旧组立即消失，原默认版本不会把该成员的命中留在旧组。
	f.exec(`UPDATE releases SET group_id=$1::uuid WHERE id=$2::uuid`, roots[1].groupID, members[0].releaseID)
	check(tied, representatives)
}
