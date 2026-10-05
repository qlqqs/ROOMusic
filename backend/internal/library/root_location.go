package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrRootNotFound 表示没有这个登记根目录。
var ErrRootNotFound = errors.New("library root not found")

// RootLocation 是登记根目录读取源文件所需的两个事实：磁盘路径与是否仍启用。
// 停用的目录不再被扫描，也不再提供播放（见目录停用语义）。
type RootLocation struct {
	Path   string
	Active bool
}

// RootQuerier 是读取登记根目录所需的最小数据库能力；*sql.DB 与 *sql.Tx 都满足。
type RootQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// LoadRootLocation 读取登记根目录的路径与启用状态；rootID 必须是 UUID 文本。根目录不存在时返回 ErrRootNotFound。
func LoadRootLocation(ctx context.Context, querier RootQuerier, rootID string) (RootLocation, error) {
	var location RootLocation
	var status string
	err := querier.QueryRowContext(ctx, `SELECT path, status FROM library_roots WHERE id=$1::uuid`, rootID).Scan(&location.Path, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return RootLocation{}, ErrRootNotFound
	}
	if err != nil {
		return RootLocation{}, fmt.Errorf("load library root: %w", err)
	}
	location.Active = status == "active"
	return location, nil
}
