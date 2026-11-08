import { viewerQueryKey } from "../auth";

export const libraryKeys = {
  /** 后台音频分析的进度：不在 library 前缀下，和扫描、目录的缓存互不牵连。 */
  analysisProgress: (generation: number) => viewerQueryKey(generation, "audio-analysis", "progress"),
  roots: (generation: number) => viewerQueryKey(generation, "library", "roots"),
  rootOperations: (generation: number) => viewerQueryKey(generation, "library", "root-operations"),
  /** 浏览的文件夹（前缀）：新增目录之后失效，“已添加”跟着更新。 */
  folderListings: (generation: number) => viewerQueryKey(generation, "library", "folders"),
  /** rootId 不为 null 时是某个目录之内的浏览（选不扫描的子文件夹）。 */
  folders: (generation: number, path: string | null, rootId: string | null) => viewerQueryKey(generation, "library", "folders", rootId, path),
  /** 每天自动扫描的设置。 */
  scanSchedule: (generation: number) => viewerQueryKey(generation, "library", "scan-schedule"),
  activeScan: (generation: number) => viewerQueryKey(generation, "library", "scans", "active"),
  /** 扫描记录的全部页：启动扫描或扫描结束时整体失效。 */
  scanHistory: (generation: number) => viewerQueryKey(generation, "library", "scans", "history"),
  scanHistoryPage: (generation: number, page: number) => viewerQueryKey(generation, "library", "scans", "history", page),
  scan: (generation: number, scanId: string) => viewerQueryKey(generation, "library", "scans", scanId),
  diagnostics: (generation: number, scanId: string) => viewerQueryKey(generation, "library", "scans", scanId, "diagnostics"),
  /** 全部已缓存的扫描报告：认领、撤销、归组之后报告里的当前专辑与“可能接替了……”会变，整体失效（历史计数不变）。 */
  scanReports: (generation: number) => viewerQueryKey(generation, "library", "scan-reports"),
  /** 一个扫描报告的全部页。 */
  scanReport: (generation: number, scanId: string) => viewerQueryKey(generation, "library", "scan-reports", scanId),
  scanReportPage: (generation: number, scanId: string, page: number, pageSize: number) =>
    viewerQueryKey(generation, "library", "scan-reports", scanId, { page, pageSize }),
};

/** 全部已缓存的扫描报告：认领、撤销、归组改变了报告里的当前专辑与认领提示，由 app 在这些写操作之后让它失效。 */
export const scanReportsQueryKey = libraryKeys.scanReports;
