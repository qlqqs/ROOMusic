import { useQuery } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import { fetchReleaseGroupPage } from "../api/endpoints";
import { catalogKeys } from "../query-keys";

/**
 * 概览用的专辑数与需要关注的专辑数，取列表响应里的 pagination.total（每次只请求一项）。
 * 一个查询里并行取两个数，共用一次重试、一个缓存条目与一个错误状态。
 */
export function useLibraryTotals() {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: catalogKeys.totals(generation),
    queryFn: async ({ signal }) => {
      const [all, attention] = await Promise.all([
        fetchReleaseGroupPage({ page: 1, pageSize: 1, attentionRequired: false, signal }),
        fetchReleaseGroupPage({ page: 1, pageSize: 1, attentionRequired: true, signal }),
      ]);
      return { total: all.data.pagination.total, attention: attention.data.pagination.total };
    },
  });
}
