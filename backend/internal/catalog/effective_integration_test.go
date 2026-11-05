package catalog

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// effectiveFixture 在合成发行之上直接写修正行，验证 release_effective 视图与读取投影；
// 写入修正的受控流程（接口、版本号与记录）在后续步骤里测试，这里只需要满足修正表的延迟外键。
type effectiveFixture struct {
	*placementFixture
	actorID string
}

func newEffectiveFixture(t *testing.T) *effectiveFixture {
	t.Helper()
	fixture := &effectiveFixture{placementFixture: newPlacementFixture(t), actorID: uuid.New()}
	fixture.exec(`INSERT INTO users(id,username,password_hash) VALUES($1::uuid,'admin','x')`, fixture.actorID)
	return fixture
}

// visibleRelease 是带一首 present 曲目（因此可见）的发行。
func (fixture *effectiveFixture) visibleRelease(title string) placementRelease {
	fixture.t.Helper()
	release := fixture.release(title)
	fixture.track(release, "曲目", "present", 100.0) // 曲目标题与发行标题无关：搜索也会命中曲目标题
	return release
}

// correct 设下一条修正意图（替换该字段已有的意图）：value 是 nil 表示明确设为空，int 是年份，string 是文本值。
func (fixture *effectiveFixture) correct(releaseID, field string, value any) {
	fixture.t.Helper()
	transaction, err := fixture.database.Begin()
	if err != nil {
		fixture.t.Fatalf("begin correction: %v", err)
	}
	defer transaction.Rollback()
	// 一个字段只有一个当前意图：设下新的意图等于替换旧的。
	if _, err := transaction.Exec(`DELETE FROM release_corrections WHERE release_id=$1::uuid AND field=$2`, releaseID, field); err != nil {
		fixture.t.Fatalf("replace correction: %v", err)
	}
	operationID := uuid.New()
	if _, err := transaction.Exec(`INSERT INTO operations(id,actor_id,operation_type,target_type,target_id,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id)
		VALUES($1::uuid,$2::uuid,'release_correction','release',$3::uuid,$4::uuid,'f',1,2,'{}','{}','{}','test')`, operationID, fixture.actorID, releaseID, uuid.New()); err != nil {
		fixture.t.Fatalf("insert operation: %v", err)
	}
	statement, arguments := "", []any{releaseID, field, value, operationID}
	switch value.(type) {
	case nil:
		statement, arguments = `INSERT INTO release_corrections(release_id,field,mode,operation_id) VALUES($1::uuid,$2,'null',$3::uuid)`, []any{releaseID, field, operationID}
	case int:
		statement = `INSERT INTO release_corrections(release_id,field,mode,year_value,operation_id) VALUES($1::uuid,$2,'set',$3,$4::uuid)`
	default:
		statement = `INSERT INTO release_corrections(release_id,field,mode,text_value,operation_id) VALUES($1::uuid,$2,'set',$3,$4::uuid)`
	}
	if _, err := transaction.Exec(statement, arguments...); err != nil {
		fixture.t.Fatalf("insert correction %s: %v", field, err)
	}
	if err := transaction.Commit(); err != nil {
		fixture.t.Fatalf("commit correction: %v", err)
	}
}

func (fixture *effectiveFixture) clearCorrection(releaseID, field string) {
	fixture.t.Helper()
	fixture.exec(`DELETE FROM release_corrections WHERE release_id=$1::uuid AND field=$2`, releaseID, field)
}

// effectiveColumns 读取视图里所有可修正的列（NULL 读成 nil）。
func (fixture *effectiveFixture) effectiveColumns(releaseID string) map[string]any {
	fixture.t.Helper()
	var title, artist string
	var albumArtist, genre, catalogNumber, edition, label, barcode sql.NullString
	var year sql.NullInt64
	if err := fixture.database.QueryRow(`SELECT title,artist,album_artist,year,genre,catalog_number,edition,label,barcode FROM release_effective WHERE id=$1::uuid`, releaseID).
		Scan(&title, &artist, &albumArtist, &year, &genre, &catalogNumber, &edition, &label, &barcode); err != nil {
		fixture.t.Fatalf("read effective columns: %v", err)
	}
	nullable := func(value sql.NullString) any {
		if !value.Valid {
			return nil
		}
		return value.String
	}
	columns := map[string]any{"title": title, "artist": artist, "album_artist": nullable(albumArtist), "genre": nullable(genre),
		"catalog_number": nullable(catalogNumber), "edition": nullable(edition), "label": nullable(label), "barcode": nullable(barcode), "year": nil}
	if year.Valid {
		columns["year"] = int(year.Int64)
	}
	return columns
}

func (fixture *effectiveFixture) decision(releaseID, field, action string) {
	fixture.t.Helper()
	fixture.exec(`INSERT INTO release_field_decisions(release_id,field_key,selected_value,selected_source,confidence,action,rule_id)
		VALUES($1::uuid,$2,to_jsonb($2::text),'tag','low',$3,'test_v1')`, releaseID, field, action)
}

// 门禁：没有任何修正时，视图的每一列都与 releases 表逐值相同（含空串、NULL、0 这些扫描值的各种形状）。
func TestPostgreSQLEffectiveViewEqualsTheTableWithoutCorrections(t *testing.T) {
	fixture := newEffectiveFixture(t)
	plain := fixture.visibleRelease("普通")
	quirky := fixture.visibleRelease("扫描值怪异")
	sparse := fixture.visibleRelease("稀疏")
	fixture.exec(`UPDATE releases SET album_artist='', year=0, genre=NULL, label='厂牌', catalog_number='  ', edition='再版', source_type='CD' WHERE id=$1::uuid`, quirky.releaseID)
	fixture.exec(`UPDATE releases SET album_artist=NULL, year=NULL, genre='古典', barcode='4988000000000', media_type='FLAC' WHERE id=$1::uuid`, sparse.releaseID)
	_ = plain

	var total, effective, different int
	if err := fixture.database.QueryRow(`SELECT (SELECT COUNT(*) FROM releases), (SELECT COUNT(*) FROM release_effective),
		(SELECT COUNT(*) FROM releases r JOIN release_effective e ON e.id = r.id WHERE
			(r.group_id, r.title, r.artist, r.created_at, r.source_root_id, r.relative_directory, r.candidate_anchor, r.candidate_kind, r.album_artist,
			 r.year, r.source_type, r.media_type, r.genre, r.catalog_number, r.edition, r.label, r.barcode, r.edit_revision)
			IS DISTINCT FROM
			(e.group_id, e.title, e.artist, e.created_at, e.source_root_id, e.relative_directory, e.candidate_anchor, e.candidate_kind, e.album_artist,
			 e.year, e.source_type, e.media_type, e.genre, e.catalog_number, e.edition, e.label, e.barcode, e.edit_revision))`).
		Scan(&total, &effective, &different); err != nil {
		t.Fatalf("compare the view with the table: %v", err)
	}
	if total != 3 || effective != total || different != 0 {
		t.Fatalf("releases %d, effective rows %d, differing rows %d; want 3 identical rows", total, effective, different)
	}
}

func TestPostgreSQLEffectiveViewAppliesAndRemovesCorrectionsPerField(t *testing.T) {
	fixture := newEffectiveFixture(t)
	release := fixture.visibleRelease("原标题")
	fixture.exec(`UPDATE releases SET artist='扫描艺人', album_artist='扫描专辑艺人', year=1998, genre='扫描流派', catalog_number='SC-1', edition='扫描版本', label='扫描厂牌', barcode='111' WHERE id=$1::uuid`, release.releaseID)
	scanned := map[string]any{"title": "原标题", "artist": "扫描艺人", "album_artist": "扫描专辑艺人", "year": 1998, "genre": "扫描流派",
		"catalog_number": "SC-1", "edition": "扫描版本", "label": "扫描厂牌", "barcode": "111"}
	if got := fixture.effectiveColumns(release.releaseID); !reflect.DeepEqual(got, scanned) {
		t.Fatalf("effective columns without corrections = %v, want the scanned values %v", got, scanned)
	}

	expect := func(label string, overrides map[string]any) {
		t.Helper()
		want := map[string]any{}
		for key, value := range scanned {
			want[key] = value
		}
		for key, value := range overrides {
			want[key] = value
		}
		if got := fixture.effectiveColumns(release.releaseID); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: effective columns = %v, want %v", label, got, want)
		}
	}

	// 设值：只有被修正的列变化；等于扫描值的设值也是有效修正（之后扫描值变化它仍然钉住）。
	fixture.correct(release.releaseID, "title", "新标题")
	expect("title set", map[string]any{"title": "新标题"})
	fixture.correct(release.releaseID, "year", 1975)
	expect("year set", map[string]any{"title": "新标题", "year": 1975})
	for field, value := range map[string]string{"genre": "新流派", "catalog_number": "NEW-9", "edition": "新版本", "label": "新厂牌", "barcode": "999"} {
		fixture.correct(release.releaseID, field, value)
		scanned2 := map[string]any{"title": "新标题", "year": 1975, field: value}
		expect("text field "+field, scanned2)
		fixture.clearCorrection(release.releaseID, field)
	}

	// 设空：明确的空值，不是“没有修正”。
	for _, field := range []string{"year", "genre", "catalog_number", "edition", "label", "barcode"} {
		fixture.correct(release.releaseID, field, nil)
	}
	expect("nullable fields set to empty", map[string]any{"title": "新标题", "year": nil, "genre": nil, "catalog_number": nil, "edition": nil, "label": nil, "barcode": nil})

	// 专辑艺人修正同时改变展示艺人。
	fixture.correct(release.releaseID, "album_artist", "新专辑艺人")
	expect("album artist set", map[string]any{"title": "新标题", "year": nil, "genre": nil, "catalog_number": nil, "edition": nil, "label": nil, "barcode": nil,
		"album_artist": "新专辑艺人", "artist": "新专辑艺人"})

	// 重扫：扫描只更新 releases 本表，修正继续生效，没有修正的字段读到新的扫描值。
	fixture.exec(`UPDATE releases SET title='重扫后的标题', artist='重扫艺人', album_artist='重扫专辑艺人', year=2020, genre='重扫流派', label='重扫厂牌', source_type='LP' WHERE id=$1::uuid`, release.releaseID)
	rescanned := fixture.effectiveColumns(release.releaseID)
	if rescanned["title"] != "新标题" || rescanned["album_artist"] != "新专辑艺人" || rescanned["artist"] != "新专辑艺人" ||
		rescanned["year"] != nil || rescanned["genre"] != nil || rescanned["label"] != nil {
		t.Fatalf("corrections must survive a rescan, got %v", rescanned)
	}

	// 清除修正：回到当前的扫描值（重扫之后的值），不是修正之前的旧扫描值。
	for _, field := range []string{"title", "album_artist", "year", "genre", "catalog_number", "edition", "label", "barcode"} {
		fixture.clearCorrection(release.releaseID, field)
	}
	if got, want := fixture.effectiveColumns(release.releaseID), (map[string]any{"title": "重扫后的标题", "artist": "重扫艺人", "album_artist": "重扫专辑艺人", "year": 2020,
		"genre": "重扫流派", "catalog_number": "SC-1", "edition": "扫描版本", "label": "重扫厂牌", "barcode": "111"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("after clearing every correction: %v, want the current scanned values %v", got, want)
	}
}

func TestPostgreSQLCorrectionRowsRejectInvalidStates(t *testing.T) {
	fixture := newEffectiveFixture(t)
	release := fixture.visibleRelease("专辑")
	operationID := uuid.New()
	fixture.exec(`INSERT INTO operations(id,actor_id,operation_type,target_type,target_id,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id)
		VALUES($1::uuid,$2::uuid,'release_correction','release',$3::uuid,$4::uuid,'f',1,2,'{}','{}','{}','test')`, operationID, fixture.actorID, release.releaseID, uuid.New())
	for name, statement := range map[string]string{
		"title cannot be set to empty":        `INSERT INTO release_corrections(release_id,field,mode,operation_id) VALUES($1::uuid,'title','null',$2::uuid)`,
		"album artist cannot be set to empty": `INSERT INTO release_corrections(release_id,field,mode,operation_id) VALUES($1::uuid,'album_artist','null',$2::uuid)`,
		"unknown field":                       `INSERT INTO release_corrections(release_id,field,mode,text_value,operation_id) VALUES($1::uuid,'source_type','set','CD',$2::uuid)`,
		"blank text":                          `INSERT INTO release_corrections(release_id,field,mode,text_value,operation_id) VALUES($1::uuid,'genre','set','   ',$2::uuid)`,
		"text over the byte limit":            `INSERT INTO release_corrections(release_id,field,mode,text_value,operation_id) VALUES($1::uuid,'genre','set',repeat('x',4097),$2::uuid)`,
		"year with text":                      `INSERT INTO release_corrections(release_id,field,mode,text_value,operation_id) VALUES($1::uuid,'year','set','1990',$2::uuid)`,
		"year out of range":                   `INSERT INTO release_corrections(release_id,field,mode,year_value,operation_id) VALUES($1::uuid,'year','set',10000,$2::uuid)`,
		"text field with a year value":        `INSERT INTO release_corrections(release_id,field,mode,year_value,operation_id) VALUES($1::uuid,'genre','set',1990,$2::uuid)`,
		"empty with a leftover value":         `INSERT INTO release_corrections(release_id,field,mode,text_value,operation_id) VALUES($1::uuid,'genre','null','x',$2::uuid)`,
		"unknown mode":                        `INSERT INTO release_corrections(release_id,field,mode,text_value,operation_id) VALUES($1::uuid,'genre','clear','x',$2::uuid)`,
	} {
		if _, err := fixture.database.Exec(statement, release.releaseID, operationID); err == nil {
			t.Errorf("%s: the insert was accepted", name)
		}
	}
	// 同一字段至多一行；没有对应操作记录的修正在提交时被拒绝。
	fixture.correct(release.releaseID, "genre", "古典")
	if _, err := fixture.database.Exec(`INSERT INTO release_corrections(release_id,field,mode,text_value,operation_id) VALUES($1::uuid,'genre','set','爵士',$2::uuid)`, release.releaseID, operationID); err == nil {
		t.Error("a second correction row for the same field was accepted")
	}
	if _, err := fixture.database.Exec(`INSERT INTO release_corrections(release_id,field,mode,text_value,operation_id) VALUES($1::uuid,'label','set','厂牌',$2::uuid)`, release.releaseID, uuid.New()); err == nil {
		t.Error("a correction referencing an operation that does not exist was committed")
	}
}

func TestPostgreSQLReadsReturnEffectiveValues(t *testing.T) {
	fixture := newEffectiveFixture(t)
	ctx := context.Background()
	store := fixture.store
	// 扫描标题的顺序是 Beta、Zulu，修正之后有效标题的顺序是 Alpha (corrected)、Beta：只有读取有效值才能通过排序断言。
	release := fixture.visibleRelease("Zulu")
	other := fixture.visibleRelease("Beta")
	fixture.exec(`UPDATE releases SET artist='Artist Z', album_artist='Album Artist Z', year=2000, genre='Classical', label='Label Z', edition='First', catalog_number='A-1', barcode='1' WHERE id=$1::uuid`, release.releaseID)
	fixture.exec(`UPDATE releases SET artist='Artist B', album_artist='Album Artist B', year=2001 WHERE id=$1::uuid`, other.releaseID)
	fixture.artwork(release, "cover-"+strings.Repeat("b", 64)+".jpg")

	fixture.correct(release.releaseID, "title", "Alpha (corrected)")
	fixture.correct(release.releaseID, "album_artist", "Corrected Artist")
	fixture.correct(release.releaseID, "year", 2050)
	fixture.correct(release.releaseID, "genre", nil)
	fixture.correct(release.releaseID, "label", "Corrected Label")

	// 摘要与详情头部：被修正的字段是有效值，其余（含封面与计数）照旧。
	summary, err := store.LoadReleaseSummary(ctx, release.releaseID)
	if err != nil || summary.Title != "Alpha (corrected)" || summary.Artist != "Corrected Artist" || summary.AlbumArtist == nil || *summary.AlbumArtist != "Corrected Artist" ||
		summary.Year == nil || *summary.Year != 2050 || summary.Artwork == nil || summary.TrackCount != 1 {
		t.Fatalf("LoadReleaseSummary = %+v, %v; want the corrected title, album artist and year with the unchanged artwork and counts", summary, err)
	}
	header, err := store.LoadReleaseHeader(ctx, release.releaseID)
	if err != nil || header.Genre != nil || header.Label == nil || *header.Label != "Corrected Label" || header.Edition == nil || *header.Edition != "First" ||
		header.CatalogNumber == nil || *header.CatalogNumber != "A-1" {
		t.Fatalf("LoadReleaseHeader = %+v, %v; want genre empty, the corrected label and the scanned edition and catalog number", header, err)
	}
	// 设空的年份读成“没有年份”。
	fixture.correct(other.releaseID, "year", nil)
	if summary, err := store.LoadReleaseSummary(ctx, other.releaseID); err != nil || summary.Year != nil {
		t.Fatalf("LoadReleaseSummary with the year set to empty = %+v, %v; want no year", summary, err)
	}
	fixture.clearCorrection(other.releaseID, "year")

	// 列表：按有效标题排序；旧标题与旧专辑艺人搜不到，修正后的值搜得到。
	page, err := store.ListReleases(ctx, ListParams{Page: 1, PageSize: 10})
	if err != nil || page.Total != 2 || page.Items[0].Title != "Alpha (corrected)" || page.Items[1].Title != "Beta" {
		t.Fatalf("ListReleases order = %+v, %v; want Alpha (corrected) then Beta", page, err)
	}
	for search, wantTotal := range map[string]int64{"Zulu": 0, "Alpha": 1, "Corrected Artist": 1, "Album Artist Z": 0, "Artist B": 1} {
		result, err := store.ListReleases(ctx, ListParams{Search: search, Page: 1, PageSize: 10})
		if err != nil || result.Total != wantTotal {
			t.Errorf("ListReleases(%q) total = %d, %v; want %d", search, result.Total, err, wantTotal)
		}
	}

	// 按组读取：搜索与排序用代表版本的有效值。
	groups, err := store.ListReleaseGroups(ctx, ListParams{Search: "corrected", Page: 1, PageSize: 10})
	if err != nil || groups.Total != 1 || groups.Items[0].Representative.ID != release.releaseID || groups.Items[0].Representative.Title != "Alpha (corrected)" {
		t.Fatalf("ListReleaseGroups(search) = %+v, %v; want the corrected release", groups, err)
	}
	groups, err = store.ListReleaseGroups(ctx, ListParams{Page: 1, PageSize: 10})
	if err != nil || groups.Total != 2 || groups.Items[0].Representative.Title != "Alpha (corrected)" || groups.Items[1].Representative.Title != "Beta" {
		t.Fatalf("ListReleaseGroups order = %+v, %v; want the effective titles Alpha (corrected), Beta", groups, err)
	}

	// 代表版本规则用有效年份：把乙并入甲所在的组。乙 2001 早于修正后的甲 2050，所以乙是代表版本；按扫描年份甲（2000）才会排第一。
	fixture.exec(`UPDATE releases SET group_id=$1::uuid WHERE id=$2::uuid`, release.groupID, other.releaseID)
	detail, err := store.LoadReleaseGroup(ctx, release.groupID)
	if err != nil || detail.ReleaseCount != 2 || detail.Representative.ID != other.releaseID || len(detail.Releases) != 2 ||
		detail.Releases[0].ID != other.releaseID || detail.Releases[1].ID != release.releaseID {
		t.Fatalf("LoadReleaseGroup = %+v, %v; want the release with the earlier effective year (Beta, 2001) first", detail, err)
	}
	fixture.correct(other.releaseID, "year", 2100)
	detail, err = store.LoadReleaseGroup(ctx, release.groupID)
	if err != nil || detail.Representative.ID != release.releaseID || detail.Releases[0].ID != release.releaseID {
		t.Fatalf("LoadReleaseGroup after moving Beta's year to 2100 = %+v, %v; want the corrected release as the representative", detail, err)
	}
	if representative := detail.Representative; representative.Title != "Alpha (corrected)" || representative.Artist != "Corrected Artist" || representative.Year == nil || *representative.Year != 2050 {
		t.Fatalf("the group detail's representative summary = %+v, want the effective title, artist and year", representative)
	}

	// 按曲目读取的位置（最近播放）：与列表同一个有效值。
	trackID := fixture.track(release, "第二首", "present", 50.0)
	placements, err := store.LoadTrackPlacements(ctx, []string{trackID})
	if err != nil || placements[trackID].Release.Title != "Alpha (corrected)" || placements[trackID].Release.Artist != "Corrected Artist" {
		t.Fatalf("LoadTrackPlacements = %+v, %v; want the corrected release summary", placements, err)
	}
}

func TestPostgreSQLReleaseCreditsFollowTheAlbumArtistCorrection(t *testing.T) {
	fixture := newEffectiveFixture(t)
	ctx := context.Background()
	scanned, bare := fixture.visibleRelease("有署名"), fixture.visibleRelease("没有署名")
	fixture.exec(`INSERT INTO release_credits(release_id,role,name,position) VALUES($1::uuid,'album_artist','扫描专辑艺人',1),($1::uuid,'producer','制作人',2)`, scanned.releaseID)

	credits := func(releaseID string) []ReleaseCredit {
		t.Helper()
		result, err := fixture.store.LoadReleaseCredits(ctx, releaseID)
		if err != nil {
			t.Fatalf("LoadReleaseCredits: %v", err)
		}
		return result
	}
	if got, want := credits(scanned.releaseID), []ReleaseCredit{{Role: "album_artist", Name: "扫描专辑艺人", ArtistRefs: []ArtistRef{}}, {Role: "producer", Name: "制作人", ArtistRefs: []ArtistRef{}}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("credits without corrections = %v, want %v", got, want)
	}
	if got := credits(bare.releaseID); len(got) != 0 {
		t.Fatalf("a release without scanned credits has %v", got)
	}

	fixture.correct(scanned.releaseID, "album_artist", "修正专辑艺人")
	fixture.correct(bare.releaseID, "album_artist", "补上的专辑艺人")
	if got, want := credits(scanned.releaseID), []ReleaseCredit{{Role: "album_artist", Name: "修正专辑艺人", ArtistRefs: []ArtistRef{}}, {Role: "producer", Name: "制作人", ArtistRefs: []ArtistRef{}}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("credits with a corrected album artist = %v, want the scanned credit replaced: %v", got, want)
	}
	if got, want := credits(bare.releaseID), []ReleaseCredit{{Role: "album_artist", Name: "补上的专辑艺人", ArtistRefs: []ArtistRef{}}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("credits for a release that had none = %v, want %v", got, want)
	}
	// 其他字段的修正不影响署名。
	fixture.correct(scanned.releaseID, "title", "改标题不改署名")
	fixture.clearCorrection(scanned.releaseID, "album_artist")
	if got, want := credits(scanned.releaseID), []ReleaseCredit{{Role: "album_artist", Name: "扫描专辑艺人", ArtistRefs: []ArtistRef{}}, {Role: "producer", Name: "制作人", ArtistRefs: []ArtistRef{}}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("credits after clearing the correction = %v, want the scanned credits back: %v", got, want)
	}
}

func TestPostgreSQLAttentionIgnoresCorrectedFields(t *testing.T) {
	fixture := newEffectiveFixture(t)
	ctx := context.Background()
	store := fixture.store
	release := fixture.visibleRelease("待确认的专辑")
	untouched := fixture.visibleRelease("没有修正的专辑")
	for _, field := range []string{"title", "artist", "genre", "source_type"} {
		fixture.decision(release.releaseID, field, "uncertain_apply")
	}
	fixture.decision(release.releaseID, "year", "auto_apply") // 高置信度的字段从来不计入
	fixture.decision(untouched.releaseID, "title", "uncertain_apply")
	fixture.exec(`INSERT INTO release_grouping_evidence(release_id,candidate_kind,rule_id,source_refs,reason) VALUES($1::uuid,'ordinary_directory','organizer_v2','[]','same_directory_conflict')`, release.releaseID)
	fixture.track(release, "缺失的曲目", "missing", 10.0)

	attention := func(releaseID string) int64 {
		t.Helper()
		summary, err := store.LoadReleaseSummary(ctx, releaseID)
		if err != nil {
			t.Fatalf("LoadReleaseSummary: %v", err)
		}
		return summary.AttentionCount
	}
	// 4 个不确定字段 + 1 个归组原因 + 1 首缺失曲目。
	if got := attention(release.releaseID); got != 6 {
		t.Fatalf("attention before corrections = %d, want 6", got)
	}
	groupAttention := func() int64 {
		t.Helper()
		detail, err := store.LoadReleaseGroup(ctx, release.groupID)
		if err != nil {
			t.Fatalf("LoadReleaseGroup: %v", err)
		}
		return detail.AttentionCount
	}
	if got := groupAttention(); got != 6 {
		t.Fatalf("group attention before corrections = %d, want 6", got)
	}

	fixture.correct(release.releaseID, "title", "确认过的标题")
	if got := attention(release.releaseID); got != 5 {
		t.Fatalf("attention after correcting the title = %d, want 5", got)
	}
	// 展示艺人的不确定由专辑艺人的修正覆盖；设空的流派同样不再计入。来源类型没有修正入口，仍然计入。
	fixture.correct(release.releaseID, "album_artist", "确认过的专辑艺人")
	fixture.correct(release.releaseID, "genre", nil)
	if got := attention(release.releaseID); got != 3 {
		t.Fatalf("attention after correcting album artist and genre = %d, want 3 (source_type, the grouping reason and the missing track)", got)
	}
	if got := groupAttention(); got != 3 {
		t.Fatalf("group attention after the corrections = %d, want 3", got)
	}
	// 清除修正后，扫描的不确定重新计入。
	fixture.clearCorrection(release.releaseID, "title")
	fixture.clearCorrection(release.releaseID, "album_artist")
	fixture.clearCorrection(release.releaseID, "genre")
	if got := attention(release.releaseID); got != 6 {
		t.Fatalf("attention after clearing the corrections = %d, want 6 again", got)
	}

	// “需要关注”筛选：只有一个不确定字段的发行修正之后不再命中，清除后重新命中。
	listed := func(attentionRequired bool) map[string]bool {
		t.Helper()
		result, err := store.ListReleases(ctx, ListParams{AttentionRequired: attentionRequired, Page: 1, PageSize: 10})
		if err != nil {
			t.Fatalf("ListReleases: %v", err)
		}
		ids := map[string]bool{}
		for _, item := range result.Items {
			ids[item.ID] = true
		}
		return ids
	}
	if ids := listed(true); !ids[untouched.releaseID] || !ids[release.releaseID] || len(ids) != 2 {
		t.Fatalf("attention filter lists %v, want both releases", ids)
	}
	fixture.correct(untouched.releaseID, "title", "确认过的标题")
	if ids := listed(true); ids[untouched.releaseID] || !ids[release.releaseID] {
		t.Fatalf("attention filter after correcting the only uncertain field lists %v, want only the release that still needs attention", ids)
	}
	if ids := listed(false); !ids[untouched.releaseID] {
		t.Fatalf("an unfiltered list lost the corrected release: %v", ids)
	}
	groups, err := store.ListReleaseGroups(ctx, ListParams{AttentionRequired: true, Page: 1, PageSize: 10})
	if err != nil || groups.Total != 1 || groups.Items[0].ID != release.groupID {
		t.Fatalf("group attention filter = %+v, %v; want only the group that still needs attention", groups, err)
	}
}
