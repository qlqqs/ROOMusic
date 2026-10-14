// Package operations 是受治理写入的共同流程：操作者加幂等键的串行与重放、同键异义的冲突、
// 在同一个事务里执行业务变化并写入操作记录。它不含任何业务表与规则，也不含 HTTP：
// 业务由拥有对应表的包提供的执行器完成，传输层只负责鉴权、同源校验、解析请求并映射错误。
//
// 一次操作的流程（见 Run）：
//
//	BEGIN（READ COMMITTED）
//	取得“操作者 + 幂等键”的事务级 advisory lock          同键请求在这里串行
//	查已有记录：指纹相同则重放已存响应，不同则 409 idempotency_conflict
//	执行器在同一个事务里完成业务变化，返回目标、前后版本、前后状态与响应
//	写入操作记录，COMMIT                                  业务变化与记录一起提交或一起回滚
//
// 只记录成功提交的操作：被拒绝（403、404、409）与失败的尝试不写记录，与目录操作一致。
package operations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

const (
	// TypeReleaseCorrection、TypeReleaseGroupChange、TypeReleaseGroupDefault 与 TypeRevert 是 operations.operation_type 允许的取值。
	TypeReleaseCorrection   = "release_correction"
	TypeReleaseGroupChange  = "release_group_change"
	TypeReleaseGroupDefault = "release_group_default"
	TypeRevert              = "revert"

	// TargetRelease 与 TargetReleaseGroup 是 operations.target_type 允许的取值：发行，以及专辑（release_groups 的一行）。
	TargetRelease      = "release"
	TargetReleaseGroup = "release_group"

	// lockClass 是操作 advisory lock 的固定类别（ASCII 的 OPER）。两参数锁与迁移锁、扫描锁（单个 bigint 键）、
	// 目录操作锁（ROOT 类别）互不共用键空间。
	lockClass int32 = 0x4f504552
)

var (
	// ErrUnavailable 表示数据库暂时不可用（连接、开启事务、取锁或提交失败），调用方应映射成 503；
	// 请求是否已经提交不确定，客户端用同一个幂等键重试即可对账。
	ErrUnavailable = errors.New("operations: database unavailable")
	// ErrOperationFailed 表示业务变化或操作记录没能写入，事务已整体回滚，调用方应映射成 500。
	ErrOperationFailed = errors.New("operations: operation failed")
)

// Class 是分类失败的类别，由传输层映射成 HTTP 状态（见 Problem）。
type Class int

const (
	// ClassInvalid 表示请求内容本身无效（400）。
	ClassInvalid Class = iota + 1
	// ClassNotFound 表示目标不存在或不可见（404）。
	ClassNotFound
	// ClassConflict 表示与目标当前状态冲突（409）。
	ClassConflict
)

// Failure 是执行器或 Run 给出的分类失败：Code 是稳定的机器可读错误码，Message 是可以直接展示的说明。
type Failure struct {
	Class   Class
	Code    string
	Message string
}

func (failure *Failure) Error() string { return failure.Code + ": " + failure.Message }

// Invalid、NotFound 与 Conflict 构造对应类别的 Failure。
func Invalid(code, message string) *Failure { return &Failure{ClassInvalid, code, message} }

// NotFound 构造目标不存在的 Failure。
func NotFound(code, message string) *Failure { return &Failure{ClassNotFound, code, message} }

// Conflict 构造与当前状态冲突的 Failure。
func Conflict(code, message string) *Failure { return &Failure{ClassConflict, code, message} }

var errIdempotencyConflict = Conflict("idempotency_conflict", "幂等键已用于其他请求")

// Problem 把 Run 与执行器返回的错误翻译成传输层需要的三项：HTTP 状态码、稳定错误码与说明。
// 状态码只是整数，operations 本身不导入 net/http。500 与 503 由调用方记录日志，响应里不含内部细节。
func Problem(err error) (status int, code, message string) {
	var failure *Failure
	switch {
	case errors.As(err, &failure):
		switch failure.Class {
		case ClassInvalid:
			status = 400
		case ClassNotFound:
			status = 404
		default:
			status = 409
		}
		return status, failure.Code, failure.Message
	case errors.Is(err, ErrUnavailable):
		return 503, "database_unavailable", "服务暂不可用"
	default:
		return 500, "operation_failed", "操作未能完成"
	}
}

// NormalizeKey 校验 Idempotency-Key 请求头：必须是 UUID，规范成小写（键的大小写不影响幂等判断）。
// 缺失或不是 UUID 时 ok 为 false，传输层应返回 400 invalid_input。
func NormalizeKey(header string) (key string, ok bool) {
	header = strings.TrimSpace(header)
	if !uuid.Valid(header) {
		return "", false
	}
	return strings.ToLower(header), true
}

// Spec 是一次操作的请求侧信息，由传输层在鉴权与校验之后构造。
type Spec struct {
	ActorID     string // 操作者（会话中的用户）
	Type        string // Type* 常量之一
	Key         string // Idempotency-Key（UUID）
	Fingerprint string // 规范化请求的指纹（见 Fingerprint），必须含操作类型、目标、预期版本与完整语义
	RequestID   string // X-Request-ID，写入记录便于关联日志
}

// Outcome 是执行器在事务内完成业务变化之后交给 Run 记录的结果。
type Outcome struct {
	TargetType       string
	TargetID         string
	ExpectedRevision int64
	ResultRevision   int64 // 必须等于 ExpectedRevision + 1
	Before, After    any   // 类型化的前后状态，序列化成 JSON；撤销依据它们
	RevertsID        string
	Response         any // 序列化成 JSON；原样返回给客户端，也是同键重放时的响应
}

// Executor 在 Run 提供的事务里完成业务变化。operationID 是本次操作预先生成的 ID：业务行（例如修正行）写入时就带上它，
// 记录在执行器之后才插入，所以引用它的外键是延迟检查的。执行器不得提交或回滚事务；
// 目标不存在、版本冲突、输入无效等分类错误返回 *Failure，其余错误按 500 或 503 处理。
type Executor func(ctx context.Context, tx *sql.Tx, operationID string) (Outcome, error)

// Result 是 Run 成功的结果。
type Result struct {
	OperationID string
	Response    json.RawMessage
	Replayed    bool // 为真表示这是同键同义请求的重放，没有重新执行
}

// Fingerprint 返回规范化请求的指纹：把 request 序列化成 JSON 后取 SHA-256。request 应该是 map（键按名排序）
// 或字段顺序固定的结构体，并且包含操作类型、目标 ID（小写）、预期版本与完整语义，不能只含请求体。
func Fingerprint(request any) string {
	encoded, _ := json.Marshal(request)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

// lockKey 把同一操作者与幂等键映射到同一把锁。碰撞只会让无关请求多排一次队，
// 是否重放仍由事务内完整的“操作者 + 键”查询决定。
func lockKey(actorID, key string) int32 {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(actorID + ":" + key))
	return int32(hash.Sum32())
}

// Run 执行一次受治理操作，见包注释。database 必须是真实的 *sql.DB：Run 自己开启事务并保证加锁、查记录、执行和写记录都在其中。
// 成功返回的 Response 是应写给客户端的 JSON（首次执行时是执行器响应的序列化，重放时是已存响应）。
// 失败时返回 *Failure（分类错误，含同键异义的 idempotency_conflict），或包装了 ErrUnavailable、ErrOperationFailed 的错误。
func Run(ctx context.Context, database *sql.DB, spec Spec, execute Executor) (Result, error) {
	actorID, key := strings.ToLower(spec.ActorID), strings.ToLower(spec.Key)
	// 加锁后的查询必须看到前一个持锁事务已提交的记录，所以显式要求 READ COMMITTED。
	tx, err := database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return Result{}, unavailable("begin", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1::int, $2::int)`, lockClass, lockKey(actorID, key)); err != nil {
		return Result{}, unavailable("lock idempotency key", err)
	}

	var recordedID, recordedFingerprint string
	var recordedResponse []byte
	err = tx.QueryRowContext(ctx, `SELECT id::text, fingerprint, response FROM operations WHERE actor_id = $1::uuid AND idempotency_key = $2::uuid`,
		actorID, key).Scan(&recordedID, &recordedFingerprint, &recordedResponse)
	switch {
	case err == nil:
		if recordedFingerprint != spec.Fingerprint {
			return Result{}, errIdempotencyConflict
		}
		return Result{OperationID: recordedID, Response: recordedResponse, Replayed: true}, nil
	case !errors.Is(err, sql.ErrNoRows):
		return Result{}, unavailable("read operation record", err)
	}

	operationID := uuid.New()
	outcome, err := execute(ctx, tx, operationID)
	if err != nil {
		return Result{}, classify("execute", err)
	}
	response, err := json.Marshal(outcome.Response)
	if err != nil {
		return Result{}, fmt.Errorf("%w: encode response: %w", ErrOperationFailed, err)
	}
	before, err := json.Marshal(outcome.Before)
	if err != nil {
		return Result{}, fmt.Errorf("%w: encode before state: %w", ErrOperationFailed, err)
	}
	after, err := json.Marshal(outcome.After)
	if err != nil {
		return Result{}, fmt.Errorf("%w: encode after state: %w", ErrOperationFailed, err)
	}
	var reverts any
	if outcome.RevertsID != "" {
		reverts = strings.ToLower(outcome.RevertsID)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO operations(id, actor_id, operation_type, target_type, target_id, idempotency_key, fingerprint,
			expected_revision, result_revision, before_state, after_state, response, reverts_operation_id, request_id)
		VALUES($1::uuid, $2::uuid, $3, $4, $5::uuid, $6::uuid, $7, $8, $9, $10::jsonb, $11::jsonb, $12::jsonb, $13::uuid, $14)`,
		operationID, actorID, spec.Type, outcome.TargetType, strings.ToLower(outcome.TargetID), key, spec.Fingerprint,
		outcome.ExpectedRevision, outcome.ResultRevision, string(before), string(after), string(response), reverts, spec.RequestID)
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.Code == "23505" && postgresError.ConstraintName == "operations_actor_key_uq" {
			return Result{}, errIdempotencyConflict
		}
		return Result{}, classify("insert operation record", err)
	}
	if err = tx.Commit(); err != nil {
		return Result{}, classify("commit", err)
	}
	return Result{OperationID: operationID, Response: response}, nil
}

func unavailable(step string, err error) error {
	return fmt.Errorf("%w: %s: %w", ErrUnavailable, step, err)
}

// classify 区分执行器、记录写入与提交的错误：*Failure 原样返回；连接层面的错误（上下文取消、网络、
// PostgreSQL 的连接与资源类错误）是 ErrUnavailable；服务器报告的其余错误（约束、触发器）与程序错误是 ErrOperationFailed。
func classify(step string, err error) error {
	var failure *Failure
	if errors.As(err, &failure) {
		return failure
	}
	if isConnectionError(err) {
		return unavailable(step, err)
	}
	return fmt.Errorf("%w: %s: %w", ErrOperationFailed, step, err)
}

func isConnectionError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		// 08 连接异常，53 资源不足（连接数等），57 操作员干预（关闭、崩溃恢复），58 系统错误；其余是约束、触发器等
		// 服务器明确报告的失败。
		if len(postgresError.Code) < 2 {
			return false
		}
		switch postgresError.Code[:2] {
		case "08", "53", "57", "58":
			return true
		}
		return false
	}
	// 不是服务器报告的错误：驱动或网络层的失败（连接断开、读写超时）。
	var connectError *pgconn.ConnectError
	var networkError net.Error
	return errors.As(err, &connectError) || errors.As(err, &networkError) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, driver.ErrBadConn) || errors.Is(err, sql.ErrConnDone)
}
