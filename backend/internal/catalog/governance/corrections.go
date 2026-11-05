package governance

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/releasesql"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/writegate"
	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/operations"
)

// 发行字段的人工修正（阶段 M3）。修正单独存放在 release_corrections，扫描只写 releases：每次读取经视图
// release_effective 把修正叠加在扫描值上，所以重扫不会覆盖修正。一个字段至多有一条当前意图——改成某个值（set）或
// 明确设为空（null）；没有这一行就是“没有修正”，字段显示当前的扫描值。写入只经 operations.Run 的共同流程（鉴权、
// 幂等、预期版本与操作记录由它和传输层负责），这里提供它的执行器与读取投影。

// maxCorrectionTextBytes 与表约束 release_corrections_value_ck 一致。
const maxCorrectionTextBytes = 4096

func correctableFieldNamed(name string) (catalog.CorrectableField, bool) {
	for _, field := range catalog.CorrectableFields {
		if field.Name == name {
			return field, true
		}
	}
	return catalog.CorrectableField{}, false
}

// RawCorrectionChange 是请求体里对一个字段的修改：Mode 是 set、null 或 clear，Value 只有 set 时有值
// （年份是整数，其余是字符串）。
type RawCorrectionChange struct {
	Mode  string          `json:"mode"`
	Value json.RawMessage `json:"value"`
}

// CorrectionChange 是校验并规范化之后的修改：set 时年份字段用 Year，其余用 Text（已去掉首尾空白、折叠内部空白）。
type CorrectionChange struct {
	Mode string
	Text string
	Year int
}

func invalidCorrection(format string, arguments ...any) *operations.Failure {
	return operations.Invalid("invalid_correction", fmt.Sprintf(format, arguments...))
}

// ParseCorrectionChanges 校验请求里的修改并规范化文本。键必须是可修正字段；set 的值按字段类型解释
// （文本去掉首尾空白并折叠内部空白后不能为空、不含控制字符、不超过 4096 字节；年份是 1 到 9999 的整数）；
// null 只用于可以留空的字段；clear 不带值。任何一项无效都整体拒绝，返回 invalid_correction。
func ParseCorrectionChanges(raw map[string]RawCorrectionChange) (map[string]CorrectionChange, error) {
	changes := make(map[string]CorrectionChange, len(raw))
	for name, item := range raw {
		field, known := correctableFieldNamed(name)
		if !known {
			return nil, invalidCorrection("不能修正这个字段")
		}
		hasValue := len(bytes.TrimSpace(item.Value)) > 0 && !bytes.Equal(bytes.TrimSpace(item.Value), []byte("null"))
		switch item.Mode {
		case catalog.CorrectionClear:
			if hasValue {
				return nil, invalidCorrection("%s：恢复为扫描值时不能带值", field.Label)
			}
			changes[name] = CorrectionChange{Mode: catalog.CorrectionClear}
		case catalog.CorrectionNull:
			if field.Required {
				return nil, invalidCorrection("%s不能留空", field.Label)
			}
			if hasValue {
				return nil, invalidCorrection("%s：留空时不能带值", field.Label)
			}
			changes[name] = CorrectionChange{Mode: catalog.CorrectionNull}
		case catalog.CorrectionSet:
			if !hasValue {
				return nil, invalidCorrection("%s需要一个值；要清空请选择留空", field.Label)
			}
			change, err := parseCorrectionValue(field, item.Value)
			if err != nil {
				return nil, err
			}
			changes[name] = change
		default:
			return nil, invalidCorrection("%s：修改方式无效", field.Label)
		}
	}
	return changes, nil
}

func parseCorrectionValue(field catalog.CorrectableField, raw json.RawMessage) (CorrectionChange, error) {
	if field.IsYear {
		var year int
		if err := json.Unmarshal(raw, &year); err != nil || year < 1 || year > 9999 {
			return CorrectionChange{}, invalidCorrection("年份必须是 1 到 9999 的整数")
		}
		return CorrectionChange{Mode: catalog.CorrectionSet, Year: year}, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return CorrectionChange{}, invalidCorrection("%s必须是文本", field.Label)
	}
	text = metadata.NormalizeValue(text)
	switch {
	case text == "":
		return CorrectionChange{}, invalidCorrection("%s不能为空；要清空请选择留空", field.Label)
	case !utf8.ValidString(text) || strings.IndexFunc(text, unicode.IsControl) >= 0:
		return CorrectionChange{}, invalidCorrection("%s含有不允许的字符", field.Label)
	case len(text) > maxCorrectionTextBytes:
		return CorrectionChange{}, invalidCorrection("%s超过 %d 字节", field.Label, maxCorrectionTextBytes)
	}
	return CorrectionChange{Mode: catalog.CorrectionSet, Text: text}, nil
}

// CorrectionRequest 是一次已校验的字段修正：目标发行、预期版本与规范化的修改。
type CorrectionRequest struct {
	ReleaseID        string // 小写 UUID
	ExpectedRevision int64
	Changes          map[string]CorrectionChange
}

// Fingerprint 返回请求的规范化指纹：含操作类型、小写的目标 ID、预期版本与每个字段的完整语义，
// 所以 set、null、clear 与不同的值各不相同（map 的键按名排序，指纹不受字段顺序影响）。
func (request CorrectionRequest) Fingerprint() string {
	changes := make(map[string]any, len(request.Changes))
	for name, change := range request.Changes {
		entry := map[string]any{"mode": change.Mode}
		if change.Mode == catalog.CorrectionSet {
			if field, _ := correctableFieldNamed(name); field.IsYear {
				entry["value"] = change.Year
			} else {
				entry["value"] = change.Text
			}
		}
		changes[name] = entry
	}
	return operations.Fingerprint(map[string]any{
		"type": operations.TypeReleaseCorrection, "release_id": strings.ToLower(request.ReleaseID),
		"expected_revision": request.ExpectedRevision, "changes": changes,
	})
}

// CorrectionField 是编辑发行信息时一个字段的状态：扫描值、当前的修正意图（没有时为 null）、有效值，
// 以及扫描对这个字段的处理是否不确定。年份的值是整数，其余是字符串；没有值时是 null。
type CorrectionField struct {
	Field          string                    `json:"field"`
	ScanValue      any                       `json:"scan_value"`
	Correction     *catalog.CorrectionIntent `json:"correction"`
	EffectiveValue any                       `json:"effective_value"`
	Uncertain      bool                      `json:"uncertain"`
}

// CorrectionState 是一张发行的修正状态：当前人工修改版本号与八个字段（固定顺序）。
type CorrectionState struct {
	ReleaseID string            `json:"release_id"`
	Revision  int64             `json:"revision"`
	Fields    []CorrectionField `json:"fields"`
}

// CorrectionResult 是修正成功的响应：与读取相同的状态，加上这次操作的 ID（撤销用）。
type CorrectionResult struct {
	CorrectionState
	OperationID string `json:"operation_id"`
}

// correctionValueProjection 读取八个字段的值，列顺序与 catalog.CorrectableFields 一致。专辑艺人与页面显示一致：
// 没有专辑艺人时是展示艺人。读 releases 得到扫描值，读 release_effective 得到有效值。
const correctionValueProjection = `releases.title,
		COALESCE(NULLIF(BTRIM(releases.album_artist), ''), releases.artist),
		NULLIF(releases.year, 0),
		NULLIF(BTRIM(releases.genre), ''), NULLIF(BTRIM(releases.label), ''), NULLIF(BTRIM(releases.catalog_number), ''),
		NULLIF(BTRIM(releases.edition), ''), NULLIF(BTRIM(releases.barcode), '')`

// loadFieldValues 读取八个字段的值（字符串、整数或 nil）；source 是 releases（扫描值）或 releasesql.ReleaseSource（有效值）。
func loadFieldValues(ctx context.Context, database catalog.Queryer, source, releaseID string) ([]any, error) {
	var title, albumArtist string
	var year sql.NullInt64
	var genre, label, catalogNumber, edition, barcode sql.NullString
	if err := database.QueryRowContext(ctx, "SELECT "+correctionValueProjection+" FROM "+source+" WHERE releases.id=$1::uuid", releaseID).
		Scan(&title, &albumArtist, &year, &genre, &label, &catalogNumber, &edition, &barcode); err != nil {
		return nil, err
	}
	text := func(value sql.NullString) any {
		if !value.Valid {
			return nil
		}
		return value.String
	}
	var yearValue any
	if year.Valid {
		yearValue = int(year.Int64)
	}
	return []any{title, albumArtist, yearValue, text(genre), text(label), text(catalogNumber), text(edition), text(barcode)}, nil
}

// loadCorrectionState 读取发行的修正状态；发行不存在或没有可见曲目时返回 ErrReleaseNotFound。第一条查询先读版本号：
// 读取期间若有修改提交，返回的版本号比数据旧，提交修改时会得到版本冲突，不会基于过期的状态覆盖新状态。
func loadCorrectionState(ctx context.Context, database catalog.Queryer, releaseID string) (CorrectionState, error) {
	state := CorrectionState{ReleaseID: strings.ToLower(releaseID)}
	var visible bool
	err := database.QueryRowContext(ctx, `SELECT releases.edit_revision, `+releasesql.ReleaseVisiblePredicate+` FROM releases WHERE releases.id=$1::uuid`, releaseID).Scan(&state.Revision, &visible)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !visible) {
		return CorrectionState{}, catalog.ErrReleaseNotFound
	}
	if err != nil {
		return CorrectionState{}, err
	}
	scanned, err := loadFieldValues(ctx, database, "releases", releaseID)
	if err != nil {
		return CorrectionState{}, err
	}
	effective, err := loadFieldValues(ctx, database, releasesql.ReleaseSource, releaseID)
	if err != nil {
		return CorrectionState{}, err
	}
	intents, err := catalog.LoadCorrectionIntents(ctx, database, releaseID)
	if err != nil {
		return CorrectionState{}, err
	}
	uncertain, err := loadUncertainFields(ctx, database, releaseID)
	if err != nil {
		return CorrectionState{}, err
	}
	state.Fields = make([]CorrectionField, len(catalog.CorrectableFields))
	for index, field := range catalog.CorrectableFields {
		item := CorrectionField{Field: field.Name, ScanValue: scanned[index], EffectiveValue: effective[index],
			// 展示艺人的不确定由专辑艺人的修正覆盖，所以两个字段的不确定都算作专辑艺人的。
			Uncertain: uncertain[field.Name] || (field.Name == "album_artist" && uncertain["artist"])}
		if intent, corrected := intents[field.Name]; corrected {
			item.Correction = &intent
		}
		state.Fields[index] = item
	}
	return state, nil
}

// loadUncertainFields 返回扫描对哪些字段的处理是 uncertain_apply。
func loadUncertainFields(ctx context.Context, database catalog.Queryer, releaseID string) (map[string]bool, error) {
	rows, err := database.QueryContext(ctx, `SELECT DISTINCT field_key FROM release_field_decisions WHERE release_id=$1::uuid AND action='uncertain_apply'`, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	uncertain := map[string]bool{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		uncertain[key] = true
	}
	return uncertain, rows.Err()
}

// LoadCorrectionState 读取发行的修正状态（管理员编辑发行信息用）；不存在或不可见时返回 ErrReleaseNotFound。
func (s *Store) LoadCorrectionState(ctx context.Context, releaseID string) (CorrectionState, error) {
	state, err := loadCorrectionState(ctx, s.database, releaseID)
	if err != nil && !errors.Is(err, catalog.ErrReleaseNotFound) {
		return CorrectionState{}, fmt.Errorf("%w: load corrections: %w", catalog.ErrQuery, err)
	}
	return state, err
}

// correctionRecordState 是操作记录里的类型化状态：被改字段的修正意图（null 表示没有修正）与这些字段当时的有效值。
// 撤销用前态的意图恢复，不把历史有效值写回扫描列；有效值只供操作记录显示。
type correctionRecordState struct {
	Fields    map[string]*catalog.CorrectionIntent `json:"fields"`
	Effective map[string]any                       `json:"effective"`
}

// lockedRelease 是被锁住的发行行里受控操作关心的两项：当前的人工修改版本号与所在的组。
type lockedRelease struct {
	revision int64
	groupID  string // 小写 UUID
}

// lockVisibleRelease 锁住发行行并返回它的人工修改版本号与所在的组；发行不存在或没有可见曲目时返回 404 分类错误。
// 所有会改发行的受控操作（修正、归组、撤销）都先锁发行行，所以同一发行上的这些操作互相串行。
func lockVisibleRelease(ctx context.Context, tx *sql.Tx, releaseID string) (lockedRelease, error) {
	if err := writegate.Lock(ctx, tx); err != nil {
		return lockedRelease{}, err
	}
	var locked lockedRelease
	var visible bool
	err := tx.QueryRowContext(ctx, `SELECT releases.edit_revision, releases.group_id::text, `+releasesql.ReleaseVisiblePredicate+` FROM releases WHERE releases.id=$1::uuid FOR UPDATE`, releaseID).
		Scan(&locked.revision, &locked.groupID, &visible)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !visible) {
		return lockedRelease{}, operations.NotFound("not_found", "发行版本不存在")
	}
	return locked, err
}

// errRevisionConflict 表示预期版本与发行当前的人工修改版本号不一致。
func errRevisionConflict() error {
	return operations.Conflict("revision_conflict", "这张专辑刚被修改过，请刷新后再试")
}

// CorrectionExecutor 返回字段修正的执行器，由 operations.Run 在事务里调用：锁发行行、核对预期版本、
// 只处理真正改变状态的条目（没有修正时 clear、与现有意图相同的 set 或 null 被忽略，全部被忽略返回 400 no_changes）、
// 写或删修正行、人工修改版本号加一，并把前后意图与有效值快照交给操作记录。
func CorrectionExecutor(request CorrectionRequest) operations.Executor {
	return func(ctx context.Context, tx *sql.Tx, operationID string) (operations.Outcome, error) {
		locked, err := lockVisibleRelease(ctx, tx, request.ReleaseID)
		if err != nil {
			return operations.Outcome{}, err
		}
		revision := locked.revision
		if revision != request.ExpectedRevision {
			return operations.Outcome{}, errRevisionConflict()
		}
		current, err := catalog.LoadCorrectionIntents(ctx, tx, request.ReleaseID)
		if err != nil {
			return operations.Outcome{}, err
		}
		effectiveBefore, err := loadFieldValues(ctx, tx, releasesql.ReleaseSource, request.ReleaseID)
		if err != nil {
			return operations.Outcome{}, err
		}

		before := correctionRecordState{Fields: map[string]*catalog.CorrectionIntent{}, Effective: map[string]any{}}
		after := correctionRecordState{Fields: map[string]*catalog.CorrectionIntent{}, Effective: map[string]any{}}
		var changedFields []string
		for index, field := range catalog.CorrectableFields {
			change, requested := request.Changes[field.Name]
			if !requested {
				continue
			}
			next := nextIntent(field, change)
			previous, hadPrevious := current[field.Name]
			if sameIntent(previous, hadPrevious, next) {
				continue
			}
			changedFields = append(changedFields, field.Name)
			if hadPrevious {
				before.Fields[field.Name] = &previous
			} else {
				before.Fields[field.Name] = nil
			}
			before.Effective[field.Name] = effectiveBefore[index]
			after.Fields[field.Name] = next
			if err := writeIntent(ctx, tx, request.ReleaseID, field, next, operationID); err != nil {
				return operations.Outcome{}, err
			}
		}
		if len(changedFields) == 0 {
			return operations.Outcome{}, operations.Invalid("no_changes", "没有需要修改的内容")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE releases SET edit_revision = edit_revision + 1 WHERE id=$1::uuid`, request.ReleaseID); err != nil {
			return operations.Outcome{}, err
		}
		if err := refreshGovernedRelease(ctx, tx, request.ReleaseID); err != nil {
			return operations.Outcome{}, err
		}
		state, err := loadCorrectionState(ctx, tx, request.ReleaseID)
		if err != nil {
			return operations.Outcome{}, err
		}
		for index, field := range catalog.CorrectableFields {
			if _, changed := after.Fields[field.Name]; changed {
				after.Effective[field.Name] = state.Fields[index].EffectiveValue
			}
		}
		return operations.Outcome{
			TargetType: operations.TargetRelease, TargetID: request.ReleaseID,
			ExpectedRevision: revision, ResultRevision: revision + 1,
			Before: before, After: after,
			Response: CorrectionResult{CorrectionState: state, OperationID: operationID},
		}, nil
	}
}

// nextIntent 把一个修改翻译成它之后的修正意图；nil 表示没有修正（clear）。
func nextIntent(field catalog.CorrectableField, change CorrectionChange) *catalog.CorrectionIntent {
	switch change.Mode {
	case catalog.CorrectionSet:
		if field.IsYear {
			return &catalog.CorrectionIntent{Mode: catalog.CorrectionSet, Value: change.Year}
		}
		return &catalog.CorrectionIntent{Mode: catalog.CorrectionSet, Value: change.Text}
	case catalog.CorrectionNull:
		return &catalog.CorrectionIntent{Mode: catalog.CorrectionNull}
	default:
		return nil
	}
}

func sameIntent(previous catalog.CorrectionIntent, hadPrevious bool, next *catalog.CorrectionIntent) bool {
	if next == nil || !hadPrevious {
		return next == nil && !hadPrevious
	}
	return previous.Mode == next.Mode && previous.Value == next.Value
}

// writeIntent 写下字段的新意图：next 为 nil 删除修正行，否则插入或替换（operation_id 记下设下它的操作）。
func writeIntent(ctx context.Context, tx *sql.Tx, releaseID string, field catalog.CorrectableField, next *catalog.CorrectionIntent, operationID string) error {
	if next == nil {
		_, err := tx.ExecContext(ctx, `DELETE FROM release_corrections WHERE release_id=$1::uuid AND field=$2`, releaseID, field.Name)
		return err
	}
	var text sql.NullString
	var year sql.NullInt64
	if next.Mode == catalog.CorrectionSet {
		if value, isYear := next.Value.(int); isYear {
			year = sql.NullInt64{Int64: int64(value), Valid: true}
		} else {
			text = sql.NullString{String: next.Value.(string), Valid: true}
		}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO release_corrections(release_id, field, mode, text_value, year_value, operation_id)
		VALUES($1::uuid, $2, $3, $4, $5, $6::uuid)
		ON CONFLICT (release_id, field) DO UPDATE
		SET mode = EXCLUDED.mode, text_value = EXCLUDED.text_value, year_value = EXCLUDED.year_value, operation_id = EXCLUDED.operation_id`,
		releaseID, field.Name, next.Mode, text, year, operationID)
	return err
}
