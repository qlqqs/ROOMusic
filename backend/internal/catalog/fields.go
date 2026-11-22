package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// 修正的三种修改方式：set 与 null 是存下来的意图，clear 删除意图（显示下一层的值）。
const (
	CorrectionSet   = "set"
	CorrectionNull  = "null"
	CorrectionClear = "clear"
)

// CorrectableField 描述首批可修正的一个发行字段（G2）。
type CorrectableField struct {
	Name     string
	Label    string // 用于错误说明的中文名
	IsYear   bool   // 年份是整数，流派是文本列表，其余是文本
	Required bool   // 不能设为空：标题与专辑艺人
}

// CorrectableFields 的顺序就是接口里 fields 的顺序，也是 correctionValueProjection 的列顺序。
var CorrectableFields = []CorrectableField{
	{Name: "title", Label: "标题", Required: true},
	{Name: "album_artist", Label: "专辑艺人", Required: true},
	{Name: "year", Label: "年份", IsYear: true},
	{Name: "genre", Label: "流派"},
	{Name: "label", Label: "厂牌"},
	{Name: "catalog_number", Label: "编号"},
	{Name: "edition", Label: "版本说明"},
	{Name: "barcode", Label: "条码"},
}

// CorrectableFieldNames 返回可修正字段的名称，按接口里的固定顺序。
func CorrectableFieldNames() []string {
	names := make([]string, len(CorrectableFields))
	for index, field := range CorrectableFields {
		names[index] = field.Name
	}
	return names
}

// CorrectionIntent 是一个字段当前的修正意图（没有修正时没有它）：set 带值，null 表示明确设为空。
type CorrectionIntent struct {
	Mode  string `json:"mode"`
	Value any    `json:"value,omitempty"`
}

// LoadCorrectionIntents 读取发行当前的修正意图，键是字段名。
func LoadCorrectionIntents(ctx context.Context, database Queryer, releaseID string) (map[string]CorrectionIntent, error) {
	rows, err := database.QueryContext(ctx, `SELECT field, mode, text_value, year_value, to_jsonb(list_value)::text FROM release_corrections WHERE release_id=$1::uuid`, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	intents := map[string]CorrectionIntent{}
	for rows.Next() {
		var name, mode string
		var text, listJSON sql.NullString
		var year sql.NullInt64
		if err := rows.Scan(&name, &mode, &text, &year, &listJSON); err != nil {
			return nil, err
		}
		intent, err := decodeIntent(name, mode, text, listJSON, year)
		if err != nil {
			return nil, err
		}
		intents[name] = intent
	}
	return intents, rows.Err()
}

type releaseFieldNames struct {
	corrected []string
	automatic []string
}

// loadReleaseFieldNames 批量读取两个来源的字段名，按 CorrectableFields 排序，人工修正优先。
func loadReleaseFieldNames(ctx context.Context, database Queryer, releaseIDs []string) (map[string]releaseFieldNames, error) {
	rows, err := database.QueryContext(ctx, `SELECT release_id::text, field, true FROM release_corrections
		WHERE release_id=ANY($1::text[]::uuid[])
		UNION ALL
		SELECT release_id::text, field, false FROM release_auto_values
		WHERE release_id=ANY($1::text[]::uuid[])`, releaseIDs)
	if err != nil {
		return nil, fmt.Errorf("%w: load release field names: %w", ErrQuery, err)
	}
	defer rows.Close()
	fieldsByRelease := make(map[string]map[string]bool, len(releaseIDs))
	for rows.Next() {
		var releaseID, field string
		var corrected bool
		if err := rows.Scan(&releaseID, &field, &corrected); err != nil {
			return nil, err
		}
		fields := fieldsByRelease[releaseID]
		if fields == nil {
			fields = make(map[string]bool, len(CorrectableFields))
			fieldsByRelease[releaseID] = fields
		}
		fields[field] = fields[field] || corrected
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	namesByRelease := make(map[string]releaseFieldNames, len(releaseIDs))
	for _, releaseID := range releaseIDs {
		fields := fieldsByRelease[releaseID]
		names := releaseFieldNames{corrected: []string{}}
		for _, field := range CorrectableFields {
			switch corrected, present := fields[field.Name]; {
			case corrected:
				names.corrected = append(names.corrected, field.Name)
			case present:
				names.automatic = append(names.automatic, field.Name)
			}
		}
		namesByRelease[releaseID] = names
	}
	return namesByRelease, nil
}

func decodeIntent(name, mode string, text, listJSON sql.NullString, year sql.NullInt64) (CorrectionIntent, error) {
	intent := CorrectionIntent{Mode: mode}
	switch {
	case mode != CorrectionSet:
	case name == "genre":
		var genres []string
		if err := json.Unmarshal([]byte(listJSON.String), &genres); err != nil {
			return CorrectionIntent{}, err
		}
		intent.Value = genres
	case year.Valid:
		intent.Value = int(year.Int64)
	default:
		intent.Value = text.String
	}
	return intent, nil
}

// AutoValue 是自动层当前的意图与来源。明确留空时只记录模式。
type AutoValue struct {
	Mode         string `json:"mode"`
	Value        any    `json:"value,omitempty"`
	OperationID  string `json:"operation_id"`
	ProviderKind string `json:"provider_kind"`
	Provider     string `json:"provider"`
}

// Intent 返回可与规范化请求比较的意图。
func (v AutoValue) Intent() CorrectionIntent {
	return CorrectionIntent{Mode: v.Mode, Value: v.Value}
}

func LoadAutoValues(ctx context.Context, database Queryer, releaseID string) (map[string]AutoValue, error) {
	rows, err := database.QueryContext(ctx, `SELECT a.field, a.mode, a.text_value, a.year_value, to_jsonb(a.list_value)::text,
 a.operation_id::text, o.origin->>'provider_kind', o.origin->>'provider'
 FROM release_auto_values a JOIN operations o ON o.id=a.operation_id WHERE a.release_id=$1::uuid`, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := map[string]AutoValue{}
	for rows.Next() {
		var name string
		var value AutoValue
		var text, list sql.NullString
		var year sql.NullInt64
		if err := rows.Scan(&name, &value.Mode, &text, &year, &list, &value.OperationID, &value.ProviderKind, &value.Provider); err != nil {
			return nil, err
		}
		intent, err := decodeIntent(name, value.Mode, text, list, year)
		if err != nil {
			return nil, err
		}
		value.Value = intent.Value
		values[name] = value
	}
	return values, rows.Err()
}
