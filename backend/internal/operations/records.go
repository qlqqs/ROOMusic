package operations

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// ErrNotFound 表示操作记录不存在（或 ID 不是 UUID）。
var ErrNotFound = errors.New("operation not found")

// ErrQuery 表示查询本身没能执行（数据库不可用）；不带它的读取错误表示读取行失败。
var ErrQuery = errors.New("operations query failed")

// Queryer 是读取记录所需的最小数据库能力；*sql.DB 与 *sql.Tx 都满足，所以执行器可以在自己的事务里读取记录。
type Queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// Record 是一条已提交的操作记录。Before 与 After 是执行器写入的类型化状态，只有对应的执行器知道怎么解释；
// 记录里没有请求体、路径或密钥。
type Record struct {
	ID               string
	ActorID          string
	Type             string
	TargetType       string
	TargetID         string
	ExpectedRevision int64
	ResultRevision   int64
	Before, After    json.RawMessage
	RevertsID        string // 撤销记录指向的原操作；其余记录为空
	OriginalType     string // 撤销记录所撤销的操作的类型；其余记录为空
	RevertedByID     string // 撤销了这条操作的记录；没有被撤销时为空
	CreatedAt        time.Time
}

// ChangeType 返回这条记录的前后状态按哪种类型解释：撤销记录的状态与它所撤销的操作同类型，其余记录就是自己的类型。
func (record Record) ChangeType() string {
	if record.Type == TypeRevert {
		return record.OriginalType
	}
	return record.Type
}

// recordProjection 与 recordSource 一起读取一条记录及“谁撤销了它”；撤销关系由唯一约束保证至多一条。
const (
	recordProjection = `o.id::text, o.actor_id::text, o.operation_type, o.target_type, o.target_id::text,
		o.expected_revision, o.result_revision, o.before_state, o.after_state,
		COALESCE(o.reverts_operation_id::text, ''), COALESCE(original.operation_type, ''), COALESCE(reverted.id::text, ''), o.created_at`
	recordSource = ` FROM operations o LEFT JOIN operations reverted ON reverted.reverts_operation_id = o.id
		LEFT JOIN operations original ON original.id = o.reverts_operation_id`
)

func scanRecord(row interface{ Scan(...any) error }) (Record, error) {
	var record Record
	var before, after []byte
	if err := row.Scan(&record.ID, &record.ActorID, &record.Type, &record.TargetType, &record.TargetID,
		&record.ExpectedRevision, &record.ResultRevision, &before, &after, &record.RevertsID, &record.OriginalType, &record.RevertedByID, &record.CreatedAt); err != nil {
		return Record{}, err
	}
	record.Before, record.After = before, after
	return record, nil
}

// Load 读取一条记录；不存在返回 ErrNotFound。
func Load(ctx context.Context, database Queryer, id string) (Record, error) {
	if !uuid.Valid(id) {
		return Record{}, ErrNotFound
	}
	record, err := scanRecord(database.QueryRowContext(ctx, "SELECT "+recordProjection+recordSource+" WHERE o.id = $1::uuid", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, fmt.Errorf("%w: load operation: %w", ErrQuery, err)
	}
	return record, nil
}

// LoadForUpdate 在事务里锁住一条记录的行，再读取它（含“谁撤销了它”），使对同一条记录的并发撤销串行：后到的请求等先到的提交之后，
// 读到的就是已有 RevertedByID 的记录。锁和读取必须是两条语句：READ COMMITTED 下带连接的 FOR UPDATE 等锁之后只重新检查被锁的行，
// 连接进来的其他行仍取自语句开始时的快照，读不到刚提交的撤销记录。不存在返回 ErrNotFound。
func LoadForUpdate(ctx context.Context, tx *sql.Tx, id string) (Record, error) {
	if !uuid.Valid(id) {
		return Record{}, ErrNotFound
	}
	var locked string
	err := tx.QueryRowContext(ctx, `SELECT id::text FROM operations WHERE id = $1::uuid FOR UPDATE`, id).Scan(&locked)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, fmt.Errorf("%w: lock operation: %w", ErrQuery, err)
	}
	return Load(ctx, tx, id)
}

// ListParams 是记录列表的已校验参数：Page 与 PageSize 都大于 0；TargetID 非空时只列这个目标的记录。
type ListParams struct {
	TargetType string
	TargetID   string
	Page       int
	PageSize   int
}

// Page 是一页记录和满足条件的总数；Items 永远非 nil。
type Page struct {
	Items []Record
	Total int64
}

// List 按时间倒序（再按 ID 倒序）返回一页记录。
func List(ctx context.Context, database Queryer, params ListParams) (Page, error) {
	where := ""
	arguments := []any{}
	if params.TargetID != "" {
		where = " WHERE o.target_type = $1 AND o.target_id = $2::uuid"
		arguments = append(arguments, params.TargetType, params.TargetID)
	}
	var total int64
	if err := database.QueryRowContext(ctx, "SELECT COUNT(*) FROM operations o"+where, arguments...).Scan(&total); err != nil {
		return Page{}, fmt.Errorf("%w: count operations: %w", ErrQuery, err)
	}
	next := len(arguments) + 1
	query := "SELECT " + recordProjection + recordSource + where +
		" ORDER BY o.created_at DESC, o.id DESC LIMIT $" + strconv.Itoa(next) + " OFFSET $" + strconv.Itoa(next+1)
	listArguments := append(append([]any{}, arguments...), params.PageSize, (params.Page-1)*params.PageSize)
	rows, err := database.QueryContext(ctx, query, listArguments...)
	if err != nil {
		return Page{}, fmt.Errorf("%w: list operations: %w", ErrQuery, err)
	}
	defer rows.Close()
	items := make([]Record, 0, params.PageSize)
	for rows.Next() {
		record, scanErr := scanRecord(rows)
		if scanErr != nil {
			return Page{}, scanErr
		}
		items = append(items, record)
	}
	if err := rows.Err(); err != nil {
		return Page{}, err
	}
	return Page{Items: items, Total: total}, nil
}
