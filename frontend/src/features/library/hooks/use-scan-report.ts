import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { failureOf, isHttpFailure, shouldRetryQuery } from "../../../shared/api";
import { useViewerScope } from "../../auth";
import { fetchScanReport } from "../api/endpoints";
import { libraryKeys } from "../query-keys";

/** 报告里新增专辑每页的条数（后端默认 20，上限 100）；显式传给后端。 */
export const SCAN_REPORT_PAGE_SIZE = 20;

/** 报告没能生成（503 scan_report_unavailable）是确定的结果，重试也不会变，不按普通 503 重试。 */
export function isReportUnavailable(error: unknown): boolean {
  return isHttpFailure(failureOf(error), 503, "scan_report_unavailable");
}

/**
 * 扫描报告的一页（仅管理员）。enabled 只在扫描已结束、report_status 为 ready 或 partial 时为真。
 * 报告不轮询：计数在扫描结束时已冻结；当前专辑与“可能接替了……”在认领、撤销、归组之后随 scanReports 前缀失效。
 * 翻页时保留上一页直到新一页到达。
 */
export function useScanReport(scanId: string, page: number, enabled: boolean) {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: libraryKeys.scanReportPage(generation, scanId, page, SCAN_REPORT_PAGE_SIZE),
    queryFn: ({ signal }) => fetchScanReport(scanId, page, SCAN_REPORT_PAGE_SIZE, signal),
    select: (response) => response.data,
    enabled,
    placeholderData: keepPreviousData,
    retry: (failureCount, error) => !isReportUnavailable(error) && shouldRetryQuery(failureCount, error),
  });
}
