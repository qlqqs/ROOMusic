package library

import (
	"context"
	"encoding/json"
	"path/filepath"
	"time"
)

// RootInfo 是目录列表与排除操作共用的安全投影；Path 只含目录名。
type RootInfo struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Path          string    `json:"path"`
	Status        string    `json:"status"`
	Revision      int64     `json:"revision"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	ExcludedPaths []string  `json:"excluded_paths"`
}

// 数组先转 JSON 再解码，避免 database/sql 把 PostgreSQL text[] 当作原生切片扫描。
const rootResponseQuery = `SELECT r.id::text,r.path,r.status,r.revision,r.created_at,r.updated_at,
	COALESCE(to_json(array_agg(e.relative_path ORDER BY e.relative_path COLLATE "C")
		FILTER (WHERE e.relative_path IS NOT NULL)), '[]'::json)
	FROM library_roots r LEFT JOIN library_root_exclusions e ON e.root_id=r.id`

func scanRootResponse(row interface{ Scan(...any) error }) (RootInfo, error) {
	var root RootInfo
	var path string
	var exclusions []byte
	if err := row.Scan(&root.ID, &path, &root.Status, &root.Revision, &root.CreatedAt, &root.UpdatedAt, &exclusions); err != nil {
		return RootInfo{}, err
	}
	if err := json.Unmarshal(exclusions, &root.ExcludedPaths); err != nil {
		return RootInfo{}, err
	}
	root.Path = filepath.Base(path)
	root.Name = root.Path
	return root, nil
}

// ListRoots 返回按登记路径排序的目录及排除项。
func (service *Service) ListRoots(ctx context.Context) ([]RootInfo, error) {
	rows, err := service.database.QueryContext(ctx, rootResponseQuery+" GROUP BY r.id ORDER BY r.path")
	if err != nil {
		return nil, &RootOperationError{Code: "database_unavailable", Message: "服务暂不可用", Err: err}
	}
	defer rows.Close()
	roots := []RootInfo{}
	for rows.Next() {
		root, scanErr := scanRootResponse(rows)
		if scanErr != nil {
			return nil, &RootOperationError{Code: "database_unavailable", Message: "服务暂不可用", Err: scanErr}
		}
		roots = append(roots, root)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, &RootOperationError{Code: "database_unavailable", Message: "服务暂不可用", Err: rowsErr}
	}
	return roots, nil
}
