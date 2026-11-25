package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/releasesql"
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

type fieldNames struct {
	corrected []string
	automatic []string
}

// loadReleaseFieldNames 批量读取两个来源的字段名，按 CorrectableFields 排序，人工修正优先。
func loadReleaseFieldNames(ctx context.Context, database Queryer, releaseIDs []string) (map[string]fieldNames, error) {
	return loadFieldNames(ctx, database, releaseIDs, CorrectableFields, `SELECT release_id::text, field, true FROM release_corrections
		WHERE release_id=ANY($1::text[]::uuid[])
		UNION ALL
		SELECT release_id::text, field, false FROM release_auto_values
		WHERE release_id=ANY($1::text[]::uuid[])`)
}

func loadTrackFieldNames(ctx context.Context, database Queryer, ids []string) (map[string]fieldNames, error) {
	return loadFieldNames(ctx, database, ids, TrackCorrectableFields, `SELECT track_id::text, field, true FROM track_corrections
		WHERE track_id=ANY($1::text[]::uuid[])
		UNION ALL
		SELECT track_id::text, field, false FROM track_auto_values
		WHERE track_id=ANY($1::text[]::uuid[])`)
}

func loadFieldNames(ctx context.Context, database Queryer, ids []string, order []CorrectableField, query string) (map[string]fieldNames, error) {
	rows, err := database.QueryContext(ctx, query, ids)
	if err != nil {
		return nil, fmt.Errorf("%w: load field names: %w", ErrQuery, err)
	}
	defer rows.Close()
	fieldsByID := make(map[string]map[string]bool, len(ids))
	for rows.Next() {
		var id, field string
		var corrected bool
		if err := rows.Scan(&id, &field, &corrected); err != nil {
			return nil, err
		}
		fields := fieldsByID[id]
		if fields == nil {
			fields = make(map[string]bool, len(order))
			fieldsByID[id] = fields
		}
		fields[field] = fields[field] || corrected
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	namesByID := make(map[string]fieldNames, len(ids))
	for _, id := range ids {
		fields := fieldsByID[id]
		names := fieldNames{corrected: []string{}}
		for _, field := range order {
			switch corrected, present := fields[field.Name]; {
			case corrected:
				names.corrected = append(names.corrected, field.Name)
			case present:
				names.automatic = append(names.automatic, field.Name)
			}
		}
		namesByID[id] = names
	}
	return namesByID, nil
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

type AutoWriterLevel int16

const (
	LevelModelLocal AutoWriterLevel = iota + 1
	LevelModelReference
	LevelModelExternal
	LevelProgramExternal
)

// EffectiveLevel 只在读取缺少级别的旧自动行时使用。
func (v AutoValue) EffectiveLevel() AutoWriterLevel {
	if v.WriterLevel != 0 {
		return v.WriterLevel
	}
	if v.ProviderKind == "source" {
		return LevelProgramExternal
	}
	return LevelModelExternal
}

// AutoValue 是自动层当前的意图与来源。明确留空时只记录模式。
type AutoValue struct {
	WriterLevel  AutoWriterLevel `json:"-"`
	ScanBasis    json.RawMessage `json:"-"`
	Mode         string          `json:"mode"`
	Value        any             `json:"value,omitempty"`
	OperationID  string          `json:"operation_id"`
	ProviderKind string          `json:"provider_kind"`
	Provider     string          `json:"provider"`
}

// Intent 返回可与规范化请求比较的意图。
func (v AutoValue) Intent() CorrectionIntent {
	return CorrectionIntent{Mode: v.Mode, Value: v.Value}
}

func LoadAutoValues(ctx context.Context, database Queryer, releaseID string) (map[string]AutoValue, error) {
	rows, err := database.QueryContext(ctx, `SELECT a.field, a.mode, a.text_value, a.year_value, to_jsonb(a.list_value)::text,
 a.operation_id::text, o.origin->>'provider_kind', o.origin->>'provider', COALESCE(a.writer_level,0),a.scan_basis
 FROM release_auto_values a JOIN operations o ON o.id=a.operation_id WHERE a.release_id=$1::uuid`, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := map[string]AutoValue{}
	for rows.Next() {
		var name string
		var value AutoValue
		var basis []byte
		var text, list sql.NullString
		var year sql.NullInt64
		if err := rows.Scan(&name, &value.Mode, &text, &year, &list, &value.OperationID, &value.ProviderKind, &value.Provider, &value.WriterLevel, &basis); err != nil {
			return nil, err
		}
		intent, err := decodeIntent(name, value.Mode, text, list, year)
		if err != nil {
			return nil, err
		}
		value.Value = intent.Value
		value.ScanBasis = basis
		values[name] = value
	}
	return values, rows.Err()
}

var TrackCorrectableFields = []CorrectableField{
	{Name: "title", Label: "曲目标题", Required: true},
	{Name: "artist", Label: "曲目艺人", Required: true},
}

func LoadTrackCorrectionIntents(ctx context.Context, database Queryer, trackID string) (map[string]CorrectionIntent, error) {
	rows, err := database.QueryContext(ctx, `SELECT field, mode, text_value FROM track_corrections WHERE track_id=$1::uuid`, trackID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	intents := map[string]CorrectionIntent{}
	for rows.Next() {
		var field, mode, text string
		if err := rows.Scan(&field, &mode, &text); err != nil {
			return nil, err
		}
		intents[field] = CorrectionIntent{Mode: mode, Value: text}
	}
	return intents, rows.Err()
}

func LoadTrackAutoValues(ctx context.Context, database Queryer, trackID string) (map[string]AutoValue, error) {
	rows, err := database.QueryContext(ctx, `SELECT a.field, a.mode, a.text_value, a.operation_id::text,
		o.origin->>'provider_kind', o.origin->>'provider', COALESCE(a.writer_level,0),a.scan_basis
		FROM track_auto_values a JOIN operations o ON o.id=a.operation_id WHERE a.track_id=$1::uuid`, trackID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := map[string]AutoValue{}
	for rows.Next() {
		var field, text string
		var value AutoValue
		var basis []byte
		if err := rows.Scan(&field, &value.Mode, &text, &value.OperationID, &value.ProviderKind, &value.Provider, &value.WriterLevel, &basis); err != nil {
			return nil, err
		}
		value.Value = text
		value.ScanBasis = basis
		values[field] = value
	}
	return values, rows.Err()
}

// ReleaseScanBasis 返回可修正字段在同一基础表行上的扫描值，包括 JSON null。
func ReleaseScanBasis(ctx context.Context, q Queryer, id string) (map[string]json.RawMessage, error) {
	var raw []byte
	if err := q.QueryRowContext(ctx, "SELECT "+releasesql.ScanFieldsJSON+" FROM releases WHERE id=$1", id).Scan(&raw); err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	err := json.Unmarshal(raw, &fields)
	return fields, err
}
