package dspsettings

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/dsp/convolution"
	"github.com/qlqq/roomusic/backend/internal/dspsettings/irload"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

const IRLimit = 50

var ErrIRLimitReached = errors.New("IR 文件数量已达上限")

type IRFile struct {
	ID         string    `json:"ir_id"`
	Name       string    `json:"name"`
	FileName   string    `json:"file_name"`
	Container  string    `json:"container"`
	SampleRate int       `json:"sample_rate"`
	Channels   int       `json:"channels"`
	Paths      int       `json:"paths"`
	Taps       int       `json:"taps"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type LoadedIR struct {
	Name          string
	ResampledFrom int
	Config        convolution.Config
}

// ConfigureIR 仅由装配根在接收请求前调用。
func (s *Service) ConfigureIR(loader *irload.Loader) { s.irLoader = loader }

func (s *Service) ListIRs(ctx context.Context, user string) ([]IRFile, error) {
	if !uuid.Valid(user) {
		return nil, ErrInvalidInput
	}
	return (irStore{s.database}).list(ctx, user)
}

func (s *Service) UploadIR(ctx context.Context, user, name, fileName string, source io.Reader) (IRFile, error) {
	name, err := normalizePresetName(name)
	fileName = path.Base(strings.ReplaceAll(fileName, "\\", "/"))
	if err != nil || !uuid.Valid(user) || source == nil || !utf8.ValidString(fileName) || fileName == "." || fileName == "/" || strings.ContainsRune(fileName, 0) || utf8.RuneCountInString(fileName) > 255 {
		return IRFile{}, ErrInvalidInput
	}
	if s.irLoader == nil {
		return IRFile{}, irload.ErrDecodeFailed
	}
	ctx, cancel := context.WithTimeout(ctx, irload.ProcessingTimeout)
	defer cancel()
	data, err := io.ReadAll(io.LimitReader(irContextReader{ctx, source}, irload.MaxFileBytes+1))
	if err != nil {
		return IRFile{}, err
	}
	if len(data) > irload.MaxFileBytes {
		return IRFile{}, irload.ErrTooLarge
	}
	metadata, err := s.irLoader.Validate(ctx, fileName, data)
	if err != nil {
		return IRFile{}, err
	}
	tx, err := s.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return IRFile{}, err
	}
	defer tx.Rollback()
	store := irStore{tx}
	if err := store.lockOwner(ctx, strings.ToLower(user)); err != nil {
		return IRFile{}, err
	}
	f, err := store.create(ctx, user, IRFile{ID: uuid.New(), Name: name, FileName: fileName, Container: metadata.Container, SampleRate: metadata.SampleRate, Channels: metadata.Channels, Paths: metadata.Paths, Taps: metadata.Taps}, data)
	if err != nil {
		return IRFile{}, err
	}
	if err := tx.Commit(); err != nil {
		return IRFile{}, err
	}
	return f, nil
}

func (s *Service) RenameIR(ctx context.Context, user, id, name string) (IRFile, error) {
	name, err := normalizePresetName(name)
	if err != nil || !uuid.Valid(user) || !uuid.Valid(id) {
		return IRFile{}, ErrInvalidInput
	}
	f, err := scanIR(s.database.QueryRowContext(ctx, `UPDATE dsp_ir_files SET name=$3,updated_at=now() WHERE user_id=$1 AND id=$2 RETURNING `+irColumns, user, id, name))
	if err == nil {
		s.InvalidateIR(user, id)
	}
	return f, err
}

// CheckIR 的写事务检查加共享行锁，保证删除不能穿过引用写入。
func (s *Service) CheckIR(ctx context.Context, tx *sql.Tx, user, id string) error {
	if !uuid.Valid(user) || !uuid.Valid(id) {
		return ErrInvalidInput
	}
	var db presetDB = s.database
	suffix := ""
	if tx != nil {
		db = tx
		suffix = " FOR SHARE"
	}
	var found string
	err := db.QueryRowContext(ctx, `SELECT id::text FROM dsp_ir_files WHERE user_id=$1 AND id=$2`+suffix, user, id).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// DeleteIR 只参加 zones 提供的事务；提交后的失效也由该用例显式调用。
func (s *Service) DeleteIR(ctx context.Context, tx *sql.Tx, user, id string) error {
	if tx == nil || !uuid.Valid(user) || !uuid.Valid(id) {
		return ErrInvalidInput
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM dsp_ir_files WHERE user_id=$1 AND id=$2`, user, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

type irContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r irContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
