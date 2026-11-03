package catalog

import (
	"context"
	"database/sql"
)

// 修正的三种修改方式：set 与 null 是存下来的意图，clear 删除意图（回到当前的扫描值）。
const (
	CorrectionSet   = "set"
	CorrectionNull  = "null"
	CorrectionClear = "clear"
)

// correctableField 描述首批可修正的一个发行字段（G2）。
type correctableField struct {
	name     string
	label    string // 用于错误说明的中文名
	year     bool   // 年份是整数，其余是文本
	required bool   // 不能设为空：标题与专辑艺人
}

// correctableFields 的顺序就是接口里 fields 的顺序，也是 correctionValueProjection 的列顺序。
var correctableFields = []correctableField{
	{name: "title", label: "标题", required: true},
	{name: "album_artist", label: "专辑艺人", required: true},
	{name: "year", label: "年份", year: true},
	{name: "genre", label: "流派"},
	{name: "label", label: "厂牌"},
	{name: "catalog_number", label: "编号"},
	{name: "edition", label: "版本说明"},
	{name: "barcode", label: "条码"},
}

// CorrectableFieldNames 返回可修正字段的名称，按接口里的固定顺序。
func CorrectableFieldNames() []string {
	names := make([]string, len(correctableFields))
	for index, field := range correctableFields {
		names[index] = field.name
	}
	return names
}

// CorrectionIntent 是一个字段当前的修正意图（没有修正时没有它）：set 带值，null 表示明确设为空。
type CorrectionIntent struct {
	Mode  string `json:"mode"`
	Value any    `json:"value,omitempty"`
}

// loadCorrectionIntents 读取发行当前的修正意图，键是字段名。
func loadCorrectionIntents(ctx context.Context, database Queryer, releaseID string) (map[string]CorrectionIntent, error) {
	rows, err := database.QueryContext(ctx, `SELECT field, mode, text_value, year_value FROM release_corrections WHERE release_id=$1::uuid`, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	intents := map[string]CorrectionIntent{}
	for rows.Next() {
		var name, mode string
		var text sql.NullString
		var year sql.NullInt64
		if err := rows.Scan(&name, &mode, &text, &year); err != nil {
			return nil, err
		}
		intent := CorrectionIntent{Mode: mode}
		switch {
		case mode != CorrectionSet:
		case year.Valid:
			intent.Value = int(year.Int64)
		default:
			intent.Value = text.String
		}
		intents[name] = intent
	}
	return intents, rows.Err()
}

// loadCorrectedFieldNames 返回发行有修正的字段名，按 correctableFields 的顺序；没有修正时是空切片。
func loadCorrectedFieldNames(ctx context.Context, database Queryer, releaseID string) ([]string, error) {
	intents, err := loadCorrectionIntents(ctx, database, releaseID)
	if err != nil {
		return nil, err
	}
	names := []string{}
	for _, field := range correctableFields {
		if _, corrected := intents[field.name]; corrected {
			names = append(names, field.name)
		}
	}
	return names, nil
}
