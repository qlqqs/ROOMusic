// Package dspsettings 拥有成员的 DSP 预设与离线校正资源；播放区文档由 zones 校验。
package dspsettings

import (
	"database/sql"
	"errors"

	"github.com/qlqq/roomusic/backend/internal/dspsettings/opra"
)

var (
	ErrInvalidInput    = errors.New("DSP 请求参数无效")
	ErrNotFound        = errors.New("DSP 资源不存在")
	ErrPresetNameTaken = errors.New("PEQ 预设名称已存在")
	ErrPresetLimit     = errors.New("PEQ 预设数量已达上限")
	ErrImportEmpty     = opra.ErrImportEmpty
)

const PresetLimit = 200

type Service struct{ database *sql.DB }

// NewService 不加载 OPRA；首次资源查询才解压内置目录。
func NewService(database *sql.DB) *Service { return &Service{database: database} }
