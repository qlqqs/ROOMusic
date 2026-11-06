import { useQuery } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import { fetchBackupOverview } from "../api/endpoints";
import { systemKeys } from "../query-keys";

/** 正在备份时的刷新间隔；页面不可见时 TanStack Query 会暂停。 */
export const BACKUP_POLL_MS = 2_000;

/** 备份状态与备份文件列表：状态区与文件区共用这一个查询。只在有备份进行时轮询，结束后一次读到新文件。 */
export function useBackupOverview() {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: systemKeys.backups(generation),
    queryFn: ({ signal }) => fetchBackupOverview(signal),
    select: (response) => response.data,
    refetchInterval: (current) => (current.state.data?.data.status.running ? BACKUP_POLL_MS : false),
  });
}
