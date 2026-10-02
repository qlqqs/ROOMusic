import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import { fetchScanPage } from "../api/endpoints";
import { libraryKeys } from "../query-keys";

/** 扫描记录每页的条数（后端默认 20，上限 100）；显式传给后端，不依赖它的默认值。 */
export const SCAN_HISTORY_PAGE_SIZE = 20;

/**
 * 扫描记录的一页（仅管理员；非管理员不应调用，后端会拒绝）。翻页时保留上一页的数据直到新一页到达，表格不会闪空。
 * 记录本身不轮询：启动扫描、活动扫描结束与扫描详情到达终态时由对应的 hook 让它失效。
 */
export function useScanHistory(page: number) {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: libraryKeys.scanHistoryPage(generation, page),
    queryFn: ({ signal }) => fetchScanPage(page, SCAN_HISTORY_PAGE_SIZE, signal),
    select: (response) => response.data,
    placeholderData: keepPreviousData,
  });
}
