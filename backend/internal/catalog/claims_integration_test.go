package catalog

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// 认领的判定与推荐（阶段 M4 第三项第 1 步，只读）。发行、曲目与人工状态都是直接写入的合成行；
// 扫描造出的真实情形（原地替换、新增文件、文件改名）在 app 包的重扫测试里验证。

// hiddenRelease 是没有 present 曲目的发行（只有一首缺失的曲目），即扫描新建了别的发行之后的旧版本。
func (fixture *effectiveFixture) hiddenRelease(title string) placementRelease {
	fixture.t.Helper()
	release := fixture.release(title)
	fixture.track(release, "缺失的曲目", "missing", 100.0)
	return release
}

// claimable 返回这个发行是否满足待认领的判定。
func (fixture *effectiveFixture) claimable(releaseID string) bool {
	fixture.t.Helper()
	var result bool
	if err := fixture.database.QueryRow(`SELECT `+releaseClaimablePredicate+` FROM `+releaseSource+` WHERE releases.id = $1::uuid`, releaseID).Scan(&result); err != nil {
		fixture.t.Fatalf("evaluate the claimable predicate: %v", err)
	}
	return result
}

// operation 写入一条供修正、默认版本之外的状态引用的操作记录。
func (fixture *effectiveFixture) operation(releaseID string) string {
	fixture.t.Helper()
	operationID := uuid.New()
	fixture.exec(`INSERT INTO operations(id,actor_id,operation_type,target_type,target_id,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id)
		VALUES($1::uuid,$2::uuid,'release_correction','release',$3::uuid,$4::uuid,'f',1,2,'{}','{}','{}','test')`, operationID, fixture.actorID, releaseID, uuid.New())
	return operationID
}

func TestPostgreSQLClaimablePredicate(t *testing.T) {
	fixture := newEffectiveFixture(t)

	plain := fixture.hiddenRelease("什么都没有的旧版本")
	corrected := fixture.hiddenRelease("有修正")
	fixture.correct(corrected.releaseID, "title", "改过的标题")
	defaulted := fixture.hiddenRelease("是默认版本")
	fixture.exec(`UPDATE release_groups SET default_release_id=$1::uuid WHERE id=$2::uuid`, defaulted.releaseID, defaulted.groupID)
	reviewed := fixture.hiddenRelease("有核对")
	fixture.groupingConflict(reviewed.releaseID, `["a.flac"]`)
	fixture.reviewIssue(reviewed.releaseID, "grouping")

	// 与别的发行同在一张专辑里：对方可见或不可见都算（扫描从不把新发行放进已有的组）。
	withVisibleSibling := fixture.hiddenRelease("与可见版本同组")
	sibling := fixture.visibleRelease("同组的可见版本")
	fixture.exec(`UPDATE releases SET group_id=$1::uuid WHERE id=$2::uuid`, withVisibleSibling.groupID, sibling.releaseID)
	withHiddenSibling := fixture.hiddenRelease("与不可见版本同组")
	hiddenSibling := fixture.hiddenRelease("同组的不可见版本")
	fixture.exec(`UPDATE releases SET group_id=$1::uuid WHERE id=$2::uuid`, withHiddenSibling.groupID, hiddenSibling.releaseID)

	// 可见的发行即使留有人工状态也不是旧版本；没有任何曲目的发行（没有 present 曲目）同样不可见。
	visibleCorrected := fixture.visibleRelease("可见且有修正")
	fixture.correct(visibleCorrected.releaseID, "title", "改过")
	trackless := fixture.release("没有曲目")
	fixture.correct(trackless.releaseID, "genre", "古典")

	for name, testCase := range map[string]struct {
		release string
		want    bool
	}{
		"hidden without any manual state":       {plain.releaseID, false},
		"hidden with a correction":              {corrected.releaseID, true},
		"hidden and the default of its album":   {defaulted.releaseID, true},
		"hidden with a review":                  {reviewed.releaseID, true},
		"hidden in an album with a visible one": {withVisibleSibling.releaseID, true},
		"hidden in an album with a hidden one":  {withHiddenSibling.releaseID, true},
		"visible with a correction":             {visibleCorrected.releaseID, false},
		"visible without anything":              {sibling.releaseID, false},
		"without tracks but with a correction":  {trackless.releaseID, true},
	} {
		if got := fixture.claimable(testCase.release); got != testCase.want {
			t.Errorf("%s: claimable = %v, want %v", name, got, testCase.want)
		}
	}

	// 文件回来之后（出现 present 曲目）不再待认领，人工状态原样保留。
	fixture.exec(`UPDATE tracks SET source_status='present' WHERE medium_id=$1::uuid`, corrected.mediumID)
	if fixture.claimable(corrected.releaseID) {
		t.Error("a release whose files came back is still claimable")
	}
	// 人工状态被清除之后同样不再待认领。
	fixture.clearCorrection(trackless.releaseID, "genre")
	if fixture.claimable(trackless.releaseID) {
		t.Error("a release without manual state is claimable")
	}
}

// path 给一个发行名下的曲目指定路径；曲目是 present 或 missing 都按给定状态，持有来源身份（扫描里 present 的曲目都持有）。
func (fixture *effectiveFixture) trackAt(release placementRelease, path, status string) string {
	fixture.t.Helper()
	fixture.counter++
	id := uuid.New()
	var identity any
	if status == "present" {
		identity = fmt.Sprintf("%s:physical:v1:%s", fixture.rootID, path)
	}
	fixture.exec(`INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,source_kind,source_identity,duration_seconds)
		VALUES($1::uuid,$2::uuid,$3,$4,'艺术家',1,$5::uuid,$6,$7,NOW(),'physical',$8,100)`,
		id, release.mediumID, fixture.counter, "曲目 "+path, fixture.rootID, path, status, identity)
	return id
}

// suggested 返回推荐的标题、理由与命中数，按返回的顺序。
func (fixture *effectiveFixture) suggested(sourceID string) []string {
	fixture.t.Helper()
	suggestions, err := fixture.store.LoadClaimSuggestions(context.Background(), sourceID)
	if err != nil {
		fixture.t.Fatalf("load suggestions: %v", err)
	}
	result := []string{}
	for _, suggestion := range suggestions {
		result = append(result, fmt.Sprintf("%s|%v|%d", suggestion.Release.Title, suggestion.Reasons, suggestion.SameFiles))
	}
	return result
}

func TestPostgreSQLClaimSuggestions(t *testing.T) {
	fixture := newEffectiveFixture(t)

	// 旧版本：目录 old；名下一首缺失的曲目（原地替换）；归组依据里还记得另外几个文件（成员增加时原有曲目已改挂走）。
	old := fixture.hiddenRelease("旧版本")
	fixture.exec(`UPDATE releases SET relative_directory='old' WHERE id=$1::uuid`, old.releaseID)
	fixture.exec(`UPDATE tracks SET relative_path='old/01.flac' WHERE medium_id=$1::uuid`, old.mediumID)
	fixture.exec(`INSERT INTO release_grouping_evidence(release_id,candidate_kind,rule_id,source_refs,reason) VALUES($1::uuid,'ordinary_directory','organizer_v2',$2::jsonb,'')`,
		old.releaseID, `["old/02.flac","old/03.flac","old/04.flac","old/05.flac","old/06.flac","old/07.flac","../escape.flac","/abs.flac"]`)
	fixture.correct(old.releaseID, "title", "我改过的标题")

	// 原地替换：同一路径上的新曲目（新版本的目录就是 old）。
	replaced := fixture.release("原地替换的新版本")
	fixture.exec(`UPDATE releases SET relative_directory='old' WHERE id=$1::uuid`, replaced.releaseID)
	fixture.trackAt(replaced, "old/01.flac", "present")

	// 新增文件：原有的两个文件改挂到新版本，另外新增了两个（不在旧版本的路径集合里）；新版本的目录是 old，路径命中两个。
	grown := fixture.release("新增文件的新版本")
	fixture.exec(`UPDATE releases SET relative_directory='old' WHERE id=$1::uuid`, grown.releaseID)
	for _, path := range []string{"old/02.flac", "old/03.flac", "old/new-1.flac", "old/new-2.flac"} {
		fixture.trackAt(grown, path, "present")
	}

	// 只是同一目录：文件全换了名字。
	renamed := fixture.release("文件改名的新版本")
	fixture.exec(`UPDATE releases SET relative_directory='old' WHERE id=$1::uuid`, renamed.releaseID)
	fixture.trackAt(renamed, "old/renamed.flac", "present")

	// 只命中文件，而且命中数更多（四个）：版本的目录不同（例如整理成了多碟结构，版本的目录是上一级）。
	// 两个理由都命中的版本仍然排在它前面，哪怕命中数更少。
	moved := fixture.release("只有文件相同")
	fixture.exec(`UPDATE releases SET relative_directory='elsewhere' WHERE id=$1::uuid`, moved.releaseID)
	for _, path := range []string{"old/04.flac", "old/05.flac", "old/06.flac", "old/07.flac"} {
		fixture.trackAt(moved, path, "present")
	}

	// 不命中：别的目录、别的 root 里的同名路径，以及同路径但缺失的曲目。
	unrelated := fixture.release("无关的版本")
	fixture.trackAt(unrelated, "unrelated/01.flac", "present")
	otherRoot := uuid.New()
	fixture.exec(`INSERT INTO library_roots(id,path,status,revision) VALUES($1::uuid,'/other','active',1)`, otherRoot)
	crossRoot := fixture.release("别的目录里的同名路径")
	fixture.exec(`UPDATE releases SET source_root_id=$1::uuid, relative_directory='old' WHERE id=$2::uuid`, otherRoot, crossRoot.releaseID)
	fixture.exec(`INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,source_kind,source_identity,duration_seconds)
		VALUES($1::uuid,$2::uuid,1,'别的目录的曲目','艺术家',1,$3::uuid,'old/01.flac','present',NOW(),'physical',$4,100)`,
		uuid.New(), crossRoot.mediumID, otherRoot, fmt.Sprintf("%s:physical:v1:old/01.flac", otherRoot))
	gone := fixture.hiddenRelease("也不可见的版本")
	fixture.exec(`UPDATE tracks SET relative_path='old/01.flac' WHERE medium_id=$1::uuid`, gone.mediumID)

	// 排序：两个理由都命中的在前，按 same_files 命中数；然后只命中一个理由的（同样按命中数、标题）。
	// 路径集合是 old/01（名下的曲目）与 old/02…old/07（归组依据；越界与绝对路径不会命中任何曲目）。
	got := fixture.suggested(old.releaseID)
	want := []string{
		"新增文件的新版本|[same_files same_directory]|2",
		"原地替换的新版本|[same_files same_directory]|1",
		"只有文件相同|[same_files]|4",
		"文件改名的新版本|[same_directory]|0",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("suggestions = %q\nwant %q", got, want)
	}

	// missing 的曲目（原地替换后失去来源身份的旧曲目）不算命中：推荐只看 present 的曲目。
	stale := fixture.release("只有缺失曲目的版本")
	fixture.exec(`UPDATE releases SET relative_directory='stale' WHERE id=$1::uuid`, stale.releaseID)
	fixture.trackAt(stale, "old/02.flac", "missing")
	if got := fixture.suggested(old.releaseID); !reflect.DeepEqual(got, want) {
		t.Fatalf("a missing track changed the suggestions: %q\nwant %q", got, want)
	}

	// 旧版本自己的文件回来、变得可见之后（这里用一首独立路径的曲目代替；路径也在它的归组依据里，目录也与自己相同），
	// 它不会把自己推荐给自己。
	fixture.exec(`UPDATE release_grouping_evidence SET source_refs = source_refs || '["old/back.flac"]'::jsonb WHERE release_id=$1::uuid`, old.releaseID)
	back := fixture.trackAt(old, "old/back.flac", "present")
	selfSuggestions, err := fixture.store.LoadClaimSuggestions(context.Background(), old.releaseID)
	if err != nil {
		t.Fatalf("load suggestions for a release that is visible again: %v", err)
	}
	for _, suggestion := range selfSuggestions {
		if suggestion.Release.ID == old.releaseID {
			t.Fatalf("a release suggests itself: %+v", suggestion)
		}
	}
	fixture.exec(`DELETE FROM tracks WHERE id=$1::uuid`, back)

	// 一个不满足任何线索的旧版本没有推荐，结果是空数组而不是 nil。
	lonely := fixture.hiddenRelease("孤立的旧版本")
	fixture.exec(`UPDATE tracks SET relative_path='lonely/01.flac' WHERE medium_id=$1::uuid`, lonely.mediumID)
	suggestions, err := fixture.store.LoadClaimSuggestions(context.Background(), lonely.releaseID)
	if err != nil || suggestions == nil || len(suggestions) != 0 {
		t.Fatalf("suggestions for a release without clues = %#v, %v; want an empty non-nil slice", suggestions, err)
	}
}

// 旧版本名下的曲目路径是独立于归组依据的线索：归组依据最多 100 条，没有它（或被截断）时仍能靠名下的曲目命中。
func TestPostgreSQLClaimSuggestionsUseTheReleasesOwnTracksWithoutEvidence(t *testing.T) {
	fixture := newEffectiveFixture(t)
	old := fixture.release("旧版本")
	fixture.exec(`UPDATE releases SET relative_directory='old' WHERE id=$1::uuid`, old.releaseID)
	fixture.trackAt(old, "old/01.flac", "missing")
	fixture.trackAt(old, "old/02.flac", "missing")
	// 旧版本没有归组依据；新版本在另一个目录（版本的目录是上一级），只持有同一个文件。
	replaced := fixture.release("新版本")
	fixture.exec(`UPDATE releases SET relative_directory='boxed' WHERE id=$1::uuid`, replaced.releaseID)
	fixture.trackAt(replaced, "old/02.flac", "present")
	if got, want := fixture.suggested(old.releaseID), []string{"新版本|[same_files]|1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("suggestions = %q, want %q", got, want)
	}
}

func TestPostgreSQLClaimSuggestionsAreBoundedAndCapped(t *testing.T) {
	fixture := newEffectiveFixture(t)
	old := fixture.hiddenRelease("旧版本")
	fixture.exec(`UPDATE releases SET relative_directory='old' WHERE id=$1::uuid`, old.releaseID)
	// 七个候选都在同一目录：最多返回 5 个，同样的理由按标题排序。
	for index := 1; index <= 7; index++ {
		candidate := fixture.release(fmt.Sprintf("候选 %d", index))
		fixture.exec(`UPDATE releases SET relative_directory='old' WHERE id=$1::uuid`, candidate.releaseID)
		fixture.trackAt(candidate, fmt.Sprintf("old/%d.flac", index), "present")
	}
	got := fixture.suggested(old.releaseID)
	want := []string{
		"候选 1|[same_directory]|0", "候选 2|[same_directory]|0", "候选 3|[same_directory]|0",
		"候选 4|[same_directory]|0", "候选 5|[same_directory]|0",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("suggestions = %q\nwant %q", got, want)
	}
}
