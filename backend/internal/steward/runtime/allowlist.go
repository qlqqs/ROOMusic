package runtime

import (
	"context"
	"net/netip"
	"strings"
	"time"
)

// AllowlistEntry 是实例级出站放行项。
type AllowlistEntry struct {
	// Host 是不含端口和 IPv6 方括号的主机名或 IP。
	Host string
	// Port 是有效端口，范围为 1 至 65535。
	Port int
	// AddedBy 是添加者；用户删除后为 nil，放行项仍然保留。
	AddedBy *string
	// CreatedAt 是首次添加时间。
	CreatedAt time.Time
}

// ListAllowlist 返回全部放行项，按主机和端口排序，供管理界面或出站策略装配使用。
func (s *Store) ListAllowlist(ctx context.Context) ([]AllowlistEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT host,port,added_by::text,created_at
 FROM steward_egress_allowlist ORDER BY host,port`)
	if err != nil {
		return nil, storeError("读取管家放行名单", err)
	}
	defer rows.Close()
	entries := []AllowlistEntry{}
	for rows.Next() {
		entry, err := scanAllowlistEntry(rows)
		if err != nil {
			return nil, storeError("读取管家放行项", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, storeError("读取管家放行名单", err)
	}
	return entries, storeError("关闭管家放行名单读取", rows.Close())
}

// AddAllowlistEntry 添加放行项，重复添加保留原添加者与时间；调用方负责管理员鉴权。
// host 使用 URL.Hostname() 的形式，域名转为小写并移除末尾的根域点，IP 统一为标准文本。
func (s *Store) AddAllowlistEntry(ctx context.Context, actorID, host string, port int) (AllowlistEntry, error) {
	entry, err := scanAllowlistEntry(s.db.QueryRowContext(ctx, `INSERT INTO steward_egress_allowlist (host,port,added_by)
 VALUES ($1,$2,$3::uuid) ON CONFLICT (host,port) DO UPDATE SET host=EXCLUDED.host
 RETURNING host,port,added_by::text,created_at`, normalizeHost(host), port, actorID))
	return entry, storeError("添加管家放行项", err)
}

// DeleteAllowlistEntry 删除指定主机与端口的放行项；调用方负责管理员鉴权。
func (s *Store) DeleteAllowlistEntry(ctx context.Context, host string, port int) error {
	err := requireAffected(s.db.ExecContext(ctx, `DELETE FROM steward_egress_allowlist WHERE host=$1 AND port=$2`, normalizeHost(host), port))
	return storeError("删除管家放行项", err)
}

// IsEgressAllowed 判断主机与端口是否在放行名单中，不执行 DNS 解析或网络请求。
func (s *Store) IsEgressAllowed(ctx context.Context, host string, port int) (bool, error) {
	var allowed bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS (
 SELECT 1 FROM steward_egress_allowlist WHERE host=$1 AND port=$2)`, normalizeHost(host), port).Scan(&allowed)
	return allowed, storeError("查询管家放行项", err)
}

func normalizeHost(host string) string {
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.Unmap().String()
	}
	return strings.TrimSuffix(strings.ToLower(host), ".")
}

func scanAllowlistEntry(row scanner) (AllowlistEntry, error) {
	var entry AllowlistEntry
	err := row.Scan(&entry.Host, &entry.Port, &entry.AddedBy, &entry.CreatedAt)
	entry.CreatedAt = entry.CreatedAt.UTC()
	return entry, err
}
