package governance

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/releasesql"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/writegate"
	"github.com/qlqq/roomusic/backend/internal/operations"
)

// NormalizeAutoFields 供调用方在计算指纹、过滤以及写入之前统一规范化字段。
func NormalizeAutoFields(fields map[string]catalog.CorrectionIntent) (map[string]catalog.CorrectionIntent, error) {
	return normalizeAutoFields(fields, catalog.CorrectableFields)
}

func NormalizeTrackAutoFields(fields map[string]catalog.CorrectionIntent) (map[string]catalog.CorrectionIntent, error) {
	return normalizeAutoFields(fields, catalog.TrackCorrectableFields)
}

func normalizeAutoFields(fields map[string]catalog.CorrectionIntent, definitions []catalog.CorrectableField) (map[string]catalog.CorrectionIntent, error) {
	if len(fields) == 0 {
		return nil, invalidCorrection("至少需要一个自动整理字段")
	}
	raw := make(map[string]RawCorrectionChange, len(fields))
	for name, intent := range fields {
		if intent.Mode == catalog.CorrectionClear {
			return nil, invalidCorrection("自动整理只能设值或留空")
		}
		value, err := json.Marshal(intent.Value)
		if err != nil {
			return nil, invalidCorrection("字段值无效")
		}
		raw[name] = RawCorrectionChange{Mode: intent.Mode, Value: value}
	}
	changes, err := parseCorrectionChanges(raw, definitions)
	if err != nil {
		return nil, err
	}
	intents := make(map[string]catalog.CorrectionIntent, len(changes))
	for name, change := range changes {
		field, _ := findCorrectionField(definitions, name)
		intents[name] = *nextIntent(field, change)
	}
	return intents, nil
}

// AutoValueKey 对已规范化的意图编码，年份是整数，流派保留列表顺序。
// 字段名另存为不再提出标记的 subject，哈希只涵盖下面返回的 JSON 字节。
func AutoValueKey(field string, intent catalog.CorrectionIntent) (json.RawMessage, string, error) {
	value, err := json.Marshal(intent)
	if err != nil {
		return nil, "", fmt.Errorf("编码字段 %s 的自动意图: %w", field, err)
	}
	digest := sha256.Sum256(value)
	return value, hex.EncodeToString(digest[:]), nil
}

// LockedAutoRelease 只在取得目录写锁与发行行锁的事务内使用。
type LockedAutoRelease struct {
	tx           *sql.Tx
	id           string
	revision     int64
	editRevision int64
	current      map[string]catalog.AutoValue
}

// LockAutoRelease 锁定可见发行，供调用方随后在同一事务内核对设置、批次与不再提出标记。
func LockAutoRelease(ctx context.Context, tx *sql.Tx, releaseID string) (*LockedAutoRelease, error) {
	return lockAutoRelease(ctx, tx, releaseID, true)
}

func lockAutoRelease(ctx context.Context, tx *sql.Tx, releaseID string, requireVisible bool) (*LockedAutoRelease, error) {
	if err := writegate.Lock(ctx, tx); err != nil {
		return nil, err
	}
	return lockAutoReleaseRow(ctx, tx, releaseID, requireVisible)
}

func lockAutoReleaseRow(ctx context.Context, tx *sql.Tx, releaseID string, requireVisible bool) (*LockedAutoRelease, error) {
	locked := &LockedAutoRelease{tx: tx, id: releaseID}
	var err error
	if requireVisible {
		err = tx.QueryRowContext(ctx, `SELECT auto_revision,edit_revision FROM releases WHERE releases.id=$1::uuid AND `+
			releasesql.ReleaseVisiblePredicate+` FOR UPDATE`, releaseID).Scan(&locked.revision, &locked.editRevision)
	} else {
		err = tx.QueryRowContext(ctx, `SELECT auto_revision, edit_revision FROM releases WHERE id=$1::uuid FOR UPDATE`, releaseID).
			Scan(&locked.revision, &locked.editRevision)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, operations.NotFound("not_found", "发行版本不存在")
	}
	if err != nil {
		return nil, err
	}
	locked.current, err = catalog.LoadAutoValues(ctx, tx, releaseID)
	if err != nil {
		return nil, err
	}
	return locked, nil
}

func LockAutoReleaseOfTrack(ctx context.Context, tx *sql.Tx, trackID string) (*LockedAutoRelease, error) {
	if err := writegate.Lock(ctx, tx); err != nil {
		return nil, err
	}
	var releaseID string
	err := tx.QueryRowContext(ctx, `SELECT m.release_id::text FROM tracks t JOIN media m ON m.id=t.medium_id
 WHERE t.id=$1::uuid AND t.source_status='present'`, trackID).Scan(&releaseID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, operations.NotFound("not_found", "曲目不存在")
	}
	if err != nil {
		return nil, err
	}
	return lockAutoReleaseRow(ctx, tx, releaseID, true)
}

func LockAutoReleaseForRevert(ctx context.Context, tx *sql.Tx, releaseID string) (*LockedAutoRelease, error) {
	return lockAutoRelease(ctx, tx, releaseID, false)
}

func (r *LockedAutoRelease) ID() string          { return r.id }
func (r *LockedAutoRelease) EditRevision() int64 { return r.editRevision }

func (r *LockedAutoRelease) Bump(ctx context.Context) (expected, result int64, err error) {
	expected = r.revision
	err = r.tx.QueryRowContext(ctx, "UPDATE releases SET auto_revision=auto_revision+1 WHERE id=$1::uuid RETURNING auto_revision", r.id).Scan(&r.revision)
	return expected, r.revision, err
}

// Current 返回锁定时该字段的自动值；没有记录与明确设空可以区分。
func (r *LockedAutoRelease) Current(field string) (catalog.AutoValue, bool) {
	value, ok := r.current[field]
	return value, ok
}

type ownedAutoIntent struct {
	catalog.CorrectionIntent
	OperationID string                  `json:"operation_id"`
	WriterLevel catalog.AutoWriterLevel `json:"writer_level,omitempty"`
	ScanBasis   json.RawMessage         `json:"scan_basis,omitempty"`
}

func ownedIntent(v catalog.AutoValue) *ownedAutoIntent {
	return &ownedAutoIntent{CorrectionIntent: v.Intent(), OperationID: v.OperationID, WriterLevel: v.WriterLevel, ScanBasis: v.ScanBasis}
}

type autoFieldState struct {
	InvalidatingScan json.RawMessage  `json:"scan_value,omitempty"`
	Auto             *ownedAutoIntent `json:"auto"`
	Effective        any              `json:"effective"`
}

// 普通补全的展示沿用原合同，作废记录另展示作废依据及其变化。
func (s autoFieldState) display() autoFieldState {
	if s.Auto != nil && s.InvalidatingScan == nil {
		s.Auto = &ownedAutoIntent{CorrectionIntent: s.Auto.CorrectionIntent, OperationID: s.Auto.OperationID}
	}
	return s
}

type autoRecordState struct {
	Fields map[string]autoFieldState `json:"fields"`
}

// Write 写入已由 NormalizeAutoFields 规范化、由调用方过滤后的字段，返回供 Run 保存的前后态。
func (r *LockedAutoRelease) Write(ctx context.Context, operationID string, fields map[string]catalog.CorrectionIntent, level catalog.AutoWriterLevel, expected map[string]json.RawMessage) (operations.Outcome, error) {
	return writeAutoFields(ctx, r, operationID, fields, level, expected)
}

func writeAutoFields(ctx context.Context, target lockedAutoFields, operationID string, fields map[string]catalog.CorrectionIntent, level catalog.AutoWriterLevel, expected map[string]json.RawMessage) (operations.Outcome, error) {
	if len(fields) == 0 {
		return operations.Outcome{}, operations.Conflict("nothing_to_apply", "没有需要写入的自动值")
	}
	scan, err := target.scanBasis(ctx)
	if err != nil {
		return operations.Outcome{}, err
	}
	next := make(map[string]*ownedAutoIntent, len(fields))
	for name, intent := range fields {
		if expected != nil {
			same, err := sameScanValue(expected[name], scan[name])
			if err != nil {
				return operations.Outcome{}, err
			}
			if !same {
				return operations.Outcome{}, operations.Conflict("item_stale", "计划依据的扫描值已变更")
			}
		}
		next[name] = &ownedAutoIntent{CorrectionIntent: intent, OperationID: operationID, WriterLevel: level, ScanBasis: scan[name]}
	}
	return target.replace(ctx, next)
}

// replace 在改写前后读取有效值。撤销时 next 携带原写入者，空指针表示原先没有自动行。
func (r *LockedAutoRelease) replace(ctx context.Context, next map[string]*ownedAutoIntent) (operations.Outcome, error) {
	effective, err := loadFieldValues(ctx, r.tx, releasesql.ReleaseSource, r.id)
	if err != nil {
		return operations.Outcome{}, err
	}
	before := autoRecordState{Fields: make(map[string]autoFieldState, len(next))}
	after := autoRecordState{Fields: make(map[string]autoFieldState, len(next))}
	for i, field := range catalog.CorrectableFields {
		value, changed := next[field.Name]
		if !changed {
			continue
		}
		previous := autoFieldState{Effective: effective[i]}
		if old, exists := r.current[field.Name]; exists {
			previous.Auto = ownedIntent(old)
		}
		before.Fields[field.Name] = previous
		if err := r.writeIntent(ctx, field, value); err != nil {
			return operations.Outcome{}, err
		}
	}
	if _, err := r.tx.ExecContext(ctx, "UPDATE releases SET auto_revision=auto_revision+1 WHERE id=$1::uuid", r.id); err != nil {
		return operations.Outcome{}, err
	}
	if err := refreshGovernedRelease(ctx, r.tx, r.id); err != nil {
		return operations.Outcome{}, err
	}
	effective, err = loadFieldValues(ctx, r.tx, releasesql.ReleaseSource, r.id)
	if err != nil {
		return operations.Outcome{}, err
	}
	for i, field := range catalog.CorrectableFields {
		if value, ok := next[field.Name]; ok {
			after.Fields[field.Name] = autoFieldState{Auto: value, Effective: effective[i]}
		}
	}
	return operations.Outcome{TargetType: operations.TargetRelease, TargetID: r.id, ExpectedRevision: r.revision, ResultRevision: r.revision + 1, Before: before, After: after}, nil
}

func (r *LockedAutoRelease) scanBasis(ctx context.Context) (map[string]json.RawMessage, error) {
	return catalog.ReleaseScanBasis(ctx, r.tx, r.id)
}

func (r *LockedAutoRelease) writeIntent(ctx context.Context, field catalog.CorrectableField, value *ownedAutoIntent) error {
	if value == nil {
		_, err := r.tx.ExecContext(ctx, "DELETE FROM release_auto_values WHERE release_id=$1 AND field=$2", r.id, field.Name)
		return err
	}
	text, year, genres := intentColumns(field, &value.CorrectionIntent)
	_, err := r.tx.ExecContext(ctx, `INSERT INTO release_auto_values(release_id,field,mode,text_value,year_value,list_value,operation_id,writer_level,scan_basis)
		VALUES($1,$2,$3,$4,$5,$6::text[],$7,NULLIF($8::smallint,0),$9::jsonb) ON CONFLICT(release_id,field) DO UPDATE SET
		mode=excluded.mode,text_value=excluded.text_value,year_value=excluded.year_value,list_value=excluded.list_value,
		operation_id=excluded.operation_id,writer_level=excluded.writer_level,scan_basis=excluded.scan_basis`,
		r.id, field.Name, value.Mode, text, year, genres, value.OperationID, value.WriterLevel, []byte(value.ScanBasis))
	return err
}
