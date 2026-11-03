// Package governance 提供发行修正、归组、默认版本、问题核对与认领的执行器和读取投影。
package governance

import "github.com/qlqq/roomusic/backend/internal/catalog"

// Store 提供治理相关的只读投影；写入由执行器经 operations.Run 完成，鉴权由传输层负责。
type Store struct{ database catalog.Queryer }

// NewStore 返回读取 database 的治理 Store。
func NewStore(database catalog.Queryer) *Store { return &Store{database: database} }
