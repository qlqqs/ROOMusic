import { useQuery } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import { fetchCatalogStats } from "../api/endpoints";
import { catalogKeys } from "../query-keys";

/** 概览顶部的资料库统计（所有登录用户）。扫描进行中与结束时由 library 让 catalog 整体失效，这里不自己轮询。 */
export function useCatalogStats() {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: catalogKeys.stats(generation),
    queryFn: async ({ signal }) => (await fetchCatalogStats(signal)).data,
  });
}
